package api

import (
	"encoding/json"
	"strings"
	"testing"

	"openloc/internal/errtrack"
)

// TestProxyRedactsTokenPaths — a crash on the invite or password-reset page
// puts the page URL, token included, in request.url, and an inline script's
// backtrace frame names that URL too. The SDK copies document.referrer into
// cgi_data.HTTP_REFERER after the client-side scrubber has run, so for that
// field the proxy is the only scrubber.
func TestProxyRedactsTokenPaths(t *testing.T) {
	const (
		inviteToken = "live-invite-token-do-not-leak"
		resetToken  = "live-reset-token-do-not-leak"
	)
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	postNotice(t, p, "application/json", `{
		"error": {
			"class": "Error",
			"message": "GET /api/invites/lookup/`+inviteToken+` failed",
			"backtrace": [{"file": "https://app.example.com/invite/`+inviteToken+`", "method": "eval", "number": 1}]
		},
		"request": {
			"url": "https://app.example.com/invite/`+inviteToken+`?error=email_mismatch",
			"cgi_data": {"HTTP_REFERER": "https://app.example.com/reset-password/`+resetToken+`"}
		}
	}`)

	req, _ := hb.lastBody["request"].(map[string]any)
	if got, want := req["url"], "https://app.example.com/invite/"+errtrack.Redacted; got != want {
		t.Errorf("request.url = %v, want %q", got, want)
	}
	cgi, _ := req["cgi_data"].(map[string]any)
	if got, want := cgi["HTTP_REFERER"], "https://app.example.com/reset-password/"+errtrack.Redacted; got != want {
		t.Errorf("HTTP_REFERER = %v, want %q", got, want)
	}
	errObj, _ := hb.lastBody["error"].(map[string]any)
	if got, want := errObj["message"], "GET /api/invites/lookup/"+errtrack.Redacted+" failed"; got != want {
		t.Errorf("error.message = %v, want %q", got, want)
	}
	frames, _ := errObj["backtrace"].([]any)
	if len(frames) != 1 {
		t.Fatalf("backtrace has %d frames, want 1", len(frames))
	}
	if got, want := frames[0].(map[string]any)["file"], "https://app.example.com/invite/"+errtrack.Redacted; got != want {
		t.Errorf("backtrace[0].file = %v, want %q", got, want)
	}

	forwarded, err := json.Marshal(hb.lastBody)
	if err != nil {
		t.Fatalf("re-encoding the forwarded notice: %v", err)
	}
	for _, token := range []string{inviteToken, resetToken} {
		if strings.Contains(string(forwarded), token) {
			t.Errorf("forwarded notice still carries %q:\n%s", token, forwarded)
		}
	}
}

// TestProxyScrubsErrorCauses — the browser SDK builds error.causes[] (each
// Error.cause's message and backtrace) after the client-side scrubber has run,
// so the proxy applies the browser's message rules, JWTs and long hex runs
// included, and the backtrace rules to every cause.
func TestProxyScrubsErrorCauses(t *testing.T) {
	const (
		token = "live-invite-token-do-not-leak"
		jwt   = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJl" // gitleaks:allow
		key   = "0123456789abcdef0123456789abcdef01234567"                      // gitleaks:allow
	)
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	postNotice(t, p, "application/json", `{
		"error": {
			"class": "Error",
			"message": "outer failure",
			"causes": [{
				"class": "TypeError",
				"message": "GET https://app.example.com/api/tm/lookup?source_text=customer-text failed on /invite/`+token+`",
				"backtrace": [{"file": "https://app.example.com/invite/`+token+`?x=1", "method": "eval", "number": 1}]
			}, {
				"class": "Error",
				"message": "bearer `+jwt+` with key `+key+` rejected"
			}]
		},
		"request": {"url": "https://app.example.com/projects"}
	}`)

	errObj, _ := hb.lastBody["error"].(map[string]any)
	causes, _ := errObj["causes"].([]any)
	if len(causes) != 2 {
		t.Fatalf("causes = %v, want two causes", errObj["causes"])
	}
	cause, _ := causes[0].(map[string]any)
	if got, want := cause["message"], "GET https://app.example.com/api/tm/lookup failed on /invite/"+errtrack.Redacted; got != want {
		t.Errorf("causes[0].message = %v, want %q", got, want)
	}
	second, _ := causes[1].(map[string]any)
	if got, want := second["message"], "bearer "+errtrack.Redacted+" with key "+errtrack.Redacted+" rejected"; got != want {
		t.Errorf("causes[1].message = %v, want %q", got, want)
	}
	frames, _ := cause["backtrace"].([]any)
	if len(frames) != 1 {
		t.Fatalf("causes[0].backtrace has %d frames, want 1", len(frames))
	}
	if got, want := frames[0].(map[string]any)["file"], "https://app.example.com/invite/"+errtrack.Redacted; got != want {
		t.Errorf("causes[0].backtrace[0].file = %v, want %q", got, want)
	}

	forwarded, err := json.Marshal(hb.lastBody)
	if err != nil {
		t.Fatalf("re-encoding the forwarded notice: %v", err)
	}
	for _, leak := range []string{token, "customer-text", jwt, key} {
		if strings.Contains(string(forwarded), leak) {
			t.Errorf("forwarded notice still carries %q:\n%s", leak, forwarded)
		}
	}
}

// TestProxyScrubsBarePathHeaders — a header that holds a bare path, with no
// scheme, loses its token segment and its query, like a full URL does.
func TestProxyScrubsBarePathHeaders(t *testing.T) {
	const token = "live-reset-token-do-not-leak"
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	postNotice(t, p, "application/json", `{
		"error": {"class": "Error", "message": "boom"},
		"request": {
			"url": "https://app.example.com/projects",
			"cgi_data": {
				"HTTP_X_ORIGINAL_URI": "/reset-password/`+token+`",
				"HTTP_X_REWRITE_URL": "/api/tm/lookup?source_text=customer-text#frag"
			}
		}
	}`)

	req, _ := hb.lastBody["request"].(map[string]any)
	cgi, _ := req["cgi_data"].(map[string]any)
	if got, want := cgi["HTTP_X_ORIGINAL_URI"], "/reset-password/"+errtrack.Redacted; got != want {
		t.Errorf("HTTP_X_ORIGINAL_URI = %v, want %q", got, want)
	}
	if got, want := cgi["HTTP_X_REWRITE_URL"], "/api/tm/lookup"; got != want {
		t.Errorf("HTTP_X_REWRITE_URL = %v, want %q", got, want)
	}
}
