package errtrack_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/honeybadger-io/honeybadger-go"
	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
)

// inviteToken stands in for a live invite token, which grants org membership
// when redeemed.
const inviteToken = "live-invite-token-do-not-leak"

func TestScrubURLsInText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{
			"GET https://x.com/api/tm/lookup?source_text=SECRET failed",
			"GET https://x.com/api/tm/lookup failed",
		},
		{
			"two: https://a.com/p?q=1 and https://b.com/r#frag end",
			"two: https://a.com/p and https://b.com/r end",
		},
		{
			"GET /api/invites/lookup/abc123 from https://x.com/invite/abc123?error=1 failed",
			"GET /api/invites/lookup/" + errtrack.Redacted + " from https://x.com/invite/" + errtrack.Redacted + " failed",
		},
		{"no urls here", "no urls here"},
		{"clean https://x.com/path stays", "clean https://x.com/path stays"},
	} {
		if got := errtrack.ScrubURLsInText(tc.in); got != tc.want {
			t.Errorf("ScrubURLsInText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPanicNoticeRedactsTokenPaths drives the SDK's Handler, which builds the
// notice from the request: URL is r.URL.String() — a relative path — and every
// header lands in CGIData. An API call made from the invite page carries the
// token in both, and a panic message can quote the path as well.
func TestPanicNoticeRedactsTokenPaths(t *testing.T) {
	rec := errtracktest.Install(t)

	h := honeybadger.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		panic("lookup failed for " + r.URL.Path)
	}))
	req := httptest.NewRequest("GET", "/api/invites/lookup/"+inviteToken+"?lang=en", nil)
	req.Header.Set("Referer", "https://app.example.com/invite/"+inviteToken)
	// A proxy header can hold the original path with no scheme, query included.
	req.Header.Set("X-Original-Uri", "/invite/"+inviteToken)
	req.Header.Set("X-Rewrite-Url", "/api/tm/lookup?source_text=customer-text")
	func() {
		defer func() { _ = recover() }() // Handler re-panics by design
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()

	n := rec.One(t)
	if want := "/api/invites/lookup/" + errtrack.Redacted; n.URL != want {
		t.Errorf("URL = %q, want %q", n.URL, want)
	}
	if got, want := n.CGIData["HTTP_REFERER"], "https://app.example.com/invite/"+errtrack.Redacted; got != want {
		t.Errorf("HTTP_REFERER = %v, want %q", got, want)
	}
	if got, want := n.CGIData["HTTP_X_ORIGINAL_URI"], "/invite/"+errtrack.Redacted; got != want {
		t.Errorf("HTTP_X_ORIGINAL_URI = %v, want %q", got, want)
	}
	if got, want := n.CGIData["HTTP_X_REWRITE_URL"], "/api/tm/lookup"; got != want {
		t.Errorf("HTTP_X_REWRITE_URL = %v, want %q", got, want)
	}
	if want := "lookup failed for /api/invites/lookup/" + errtrack.Redacted; n.ErrorMessage != want {
		t.Errorf("ErrorMessage = %q, want %q", n.ErrorMessage, want)
	}
	for k, v := range n.CGIData {
		if s, ok := v.(string); ok && strings.Contains(s, inviteToken) {
			t.Errorf("CGIData[%q] leaks the invite token: %q", k, s)
		}
	}
}
