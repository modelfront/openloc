package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack/errtracktest"
	"openloc/internal/httputil"
)

// fakeHoneybadger records the last forwarded notice so tests can assert on
// what actually leaves the proxy — headers, path, and scrubbed payload.
type fakeHoneybadger struct {
	*httptest.Server
	lastPath   string
	lastAPIKey string
	lastBody   map[string]any
	calls      int
}

func newFakeHoneybadger(t *testing.T) *fakeHoneybadger {
	t.Helper()
	f := &fakeHoneybadger{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		f.lastPath = r.URL.Path
		f.lastAPIKey = r.Header.Get("X-API-Key")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("fake HB failed reading body: %v", err)
		}
		f.lastBody = nil
		if err := json.Unmarshal(body, &f.lastBody); err != nil {
			t.Errorf("forwarded body is not JSON: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(f.Server.Close)
	return f
}

// testProxyJWTSecret signs the session cookies the identity tests present.
const testProxyJWTSecret = "errtrack-proxy-test-secret"

// The session the identity tests sign in as. The two ids differ so a lookup
// given the wrong one finds nothing — see stubIdentityQuerier.
const (
	testOperatorUserID = int64(42)
	testOrgTenantID    = int64(7)
	testOperatorEmail  = "operator@example.com"
	testOrgOwnerEmail  = "owner@acme.example.com"

	// testOwnerlessTenantID is an org the stub has no owner row for, so
	// GetTenantOwner returns ErrNoRows. A real one: an org whose owner invite
	// was never accepted, or whose owner was removed.
	testOwnerlessTenantID = int64(8)
)

// testSessionCookieName duplicates the unexported cookieName in
// internal/auth/handler.go. Spelling it out keeps the test on the real
// transport — the browser SDK attaches this cookie and nothing else, so a test
// that used the Authorization header instead would pass while the production
// path was broken.
const testSessionCookieName = "openloc_token"

// stubIdentityQuerier answers only the two lookups applyServerIdentity makes.
// The embedded interface leaves every other method nil, so anything else this
// code starts calling panics loudly instead of quietly returning a zero value.
//
// Both lookups are keyed by id, because which id feeds which lookup is the
// feature: user_email comes from claims.UserID and the owner from
// claims.TenantID. A lookup handed the wrong id finds nothing.
type stubIdentityQuerier struct {
	db.Querier
	users   map[int64]db.User
	owners  map[int64]db.GetTenantOwnerRow
	userErr error
}

func (s stubIdentityQuerier) GetActiveUser(_ context.Context, id int64) (db.User, error) {
	if s.userErr != nil {
		return db.User{}, s.userErr
	}
	u, ok := s.users[id]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (s stubIdentityQuerier) GetTenantOwner(_ context.Context, tenantID int64) (db.GetTenantOwnerRow, error) {
	o, ok := s.owners[tenantID]
	if !ok {
		return db.GetTenantOwnerRow{}, pgx.ErrNoRows
	}
	return o, nil
}

func testProxy(hbURL string) *errtrackProxy {
	return newErrtrackProxy(Deps{
		HoneybadgerAPIKey:   "hb-test-key",
		HoneybadgerEndpoint: hbURL,
		Environment:         "test-env",
		Revision:            "test-rev",
		JWTSecret:           testProxyJWTSecret,
		Queries:             testIdentityQuerier(),
	})
}

func testIdentityQuerier() stubIdentityQuerier {
	return stubIdentityQuerier{
		users:  map[int64]db.User{testOperatorUserID: {Email: testOperatorEmail}},
		owners: map[int64]db.GetTenantOwnerRow{testOrgTenantID: {Email: testOrgOwnerEmail}},
	}
}

// postNoticeAs posts a notice carrying the HttpOnly session cookie that the
// SDK's same-origin fetch attaches automatically.
func postNoticeAs(t *testing.T, p *errtrackProxy, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", errtrackNoticePath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: testSessionCookieName, Value: token})
	res := httptest.NewRecorder()
	p.handleNotice(res, req)
	return res
}

// noticeContext returns the forwarded notice's request.context block.
func noticeContext(t *testing.T, hb *fakeHoneybadger) map[string]any {
	t.Helper()
	req, _ := hb.lastBody["request"].(map[string]any)
	if req == nil {
		t.Fatal("forwarded notice has no request block")
	}
	ctx, _ := req["context"].(map[string]any)
	if ctx == nil {
		t.Fatal("forwarded notice has no request.context block")
	}
	return ctx
}

// forgedIdentityKeys spells out every identity field forgedIdentityNotice sends.
// A literal, never the production `identityKeys` slice — the expectation has to
// stay independent of the code under test.
var forgedIdentityKeys = []string{
	"user_id",
	"user_email",
	"impersonating_user_email",
	"tenant_id",
	"role",
	"impersonating",
}

// forgedIdentityNotice claims to be someone else in every identity field the
// server owns.
const forgedIdentityNotice = `{
	"error": {"class": "Error", "message": "x"},
	"request": {"context": {
		"user_id": "999",
		"user_email": "attacker@evil.com",
		"impersonating_user_email": "attacker@evil.com",
		"tenant_id": "999",
		"role": "owner",
		"impersonating": true,
		"queryKey": "[\"projects\"]"
	}}
}`

func TestProxyIdentityComesFromSessionNotTheClient(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	token, err := auth.GenerateToken(42, 7, "member", testProxyJWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("signing session token: %v", err)
	}
	postNoticeAs(t, p, token, forgedIdentityNotice)

	ctx := noticeContext(t, hb)
	if ctx["user_id"] != "42" {
		t.Errorf("user_id = %v, want 42 from the session, not the client's 999", ctx["user_id"])
	}
	if ctx["user_email"] != testOperatorEmail {
		t.Errorf("user_email = %v, want the session user's address", ctx["user_email"])
	}
	if ctx["tenant_id"] != "7" {
		t.Errorf("tenant_id = %v, want 7 from the session", ctx["tenant_id"])
	}
	if ctx["role"] != "member" {
		t.Errorf("role = %v, want member from the session, not the client's owner", ctx["role"])
	}
	// Not an impersonated session, so neither impersonation field may appear
	// however loudly the client insists.
	if _, present := ctx["impersonating"]; present {
		t.Errorf("impersonating survived on a plain session: %v", ctx["impersonating"])
	}
	if _, present := ctx["impersonating_user_email"]; present {
		t.Errorf("impersonating_user_email survived on a plain session: %v", ctx["impersonating_user_email"])
	}
	// Non-identity context is the client's to send and must not be disturbed.
	if ctx["queryKey"] != `["projects"]` {
		t.Errorf("queryKey = %v, want the client's value untouched", ctx["queryKey"])
	}
}

func TestProxyStripsIdentityWithoutASession(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	// No cookie: a crash on the login page, or after the session expired. The
	// notice must still be forwarded — just with nothing it can't vouch for.
	postNotice(t, p, "application/json", forgedIdentityNotice)

	if hb.calls != 1 {
		t.Fatalf("forwarded %d notices, want 1 — an anonymous crash must still report", hb.calls)
	}
	ctx := noticeContext(t, hb)
	for _, k := range forgedIdentityKeys {
		if v, present := ctx[k]; present {
			t.Errorf("context[%q] = %v survived without a session, want stripped", k, v)
		}
	}
	if ctx["queryKey"] != `["projects"]` {
		t.Errorf("queryKey = %v, want non-identity context kept", ctx["queryKey"])
	}
}

// TestIdentityKeysCoverEveryForgedField guards the pair above: the strip list
// the proxy actually uses must cover every field the forgery tests send, so
// shrinking identityKeys fails here rather than silently narrowing the strip.
func TestIdentityKeysCoverEveryForgedField(t *testing.T) {
	covered := make(map[string]bool, len(identityKeys))
	for _, k := range identityKeys {
		covered[k] = true
	}
	for _, k := range forgedIdentityKeys {
		if !covered[k] {
			t.Errorf("identityKeys is missing %q — a forged value would reach Honeybadger", k)
		}
	}
}

func TestProxyImpersonatedSessionCarriesBothEmails(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	token, err := auth.GenerateImpersonationToken(42, 7, "owner", testProxyJWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("signing impersonation token: %v", err)
	}
	postNoticeAs(t, p, token, `{"error": {"class": "Error", "message": "x"}}`)

	ctx := noticeContext(t, hb)
	// Who was driving: resolved from claims.UserID, which stays the operator
	// even though /auth/me shows the org owner's address in the UI.
	if ctx["user_email"] != testOperatorEmail {
		t.Errorf("user_email = %v, want the operator who was driving", ctx["user_email"])
	}
	// Whose account they were in.
	if ctx["impersonating_user_email"] != testOrgOwnerEmail {
		t.Errorf("impersonating_user_email = %v, want the impersonated org's owner", ctx["impersonating_user_email"])
	}
	if ctx["impersonating"] != true {
		t.Errorf("impersonating = %v, want true", ctx["impersonating"])
	}
}

// TestProxyOwnerlessOrgKeepsTheOperator — the owner lookup is best-effort, so
// an org with no owner row costs the notice that one field and nothing else.
func TestProxyOwnerlessOrgKeepsTheOperator(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	token, err := auth.GenerateImpersonationToken(testOperatorUserID, testOwnerlessTenantID, "owner", testProxyJWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("signing impersonation token: %v", err)
	}
	postNoticeAs(t, p, token, `{"error": {"class": "Error", "message": "x"}}`)

	if hb.calls != 1 {
		t.Fatalf("forwarded %d notices, want 1 — a missing owner must not cost the notice", hb.calls)
	}
	ctx := noticeContext(t, hb)
	if _, present := ctx["impersonating_user_email"]; present {
		t.Errorf("impersonating_user_email = %v, want absent when the org has no owner", ctx["impersonating_user_email"])
	}
	// Everything that does not depend on that lookup must still be there.
	if ctx["user_email"] != testOperatorEmail {
		t.Errorf("user_email = %v, want the operator regardless of the owner lookup", ctx["user_email"])
	}
	if ctx["impersonating"] != true {
		t.Errorf("impersonating = %v, want true", ctx["impersonating"])
	}
}

func TestProxyIdentityLookupFailureKeepsTheNotice(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)
	p.queries = stubIdentityQuerier{userErr: errors.New("db down")}

	token, err := auth.GenerateToken(42, 7, "member", testProxyJWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("signing session token: %v", err)
	}
	postNoticeAs(t, p, token, `{"error": {"class": "Error", "message": "x"}}`)

	if hb.calls != 1 {
		t.Fatalf("forwarded %d notices, want 1 — a failed lookup costs a name, never the notice", hb.calls)
	}
	ctx := noticeContext(t, hb)
	if _, present := ctx["user_email"]; present {
		t.Errorf("user_email = %v, want absent when the lookup failed", ctx["user_email"])
	}
	// The claims-derived half needs no database and must survive regardless.
	if ctx["user_id"] != "42" {
		t.Errorf("user_id = %v, want 42 — claims need no lookup", ctx["user_id"])
	}
}

func postNotice(t *testing.T, p *errtrackProxy, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/errtrack/v1/notices/js", strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	res := httptest.NewRecorder()
	p.handleNotice(res, req)
	return res
}

func TestProxyForwardsWithServerSideKey(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	res := postNotice(t, p, "application/json", `{"error":{"class":"TypeError","message":"boom"}}`)

	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 passed through from HB", res.Code)
	}
	if hb.lastPath != "/v1/notices/js" {
		t.Errorf("forward path = %q, want /v1/notices/js", hb.lastPath)
	}
	if hb.lastAPIKey != "hb-test-key" {
		t.Errorf("X-API-Key = %q, want the server-side key", hb.lastAPIKey)
	}
}

func TestProxyOverridesEnvironmentAndRevision(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	// A client claiming to be production with a bogus revision must not win —
	// revision equality with uploaded sourcemaps depends on the override.
	postNotice(t, p, "application/json",
		`{"error":{"class":"Error"},"server":{"environment_name":"production","revision":"spoofed"}}`)

	server, _ := hb.lastBody["server"].(map[string]any)
	if server == nil {
		t.Fatal("forwarded notice has no server block")
	}
	if server["environment_name"] != "test-env" {
		t.Errorf("environment_name = %v, want test-env (server config)", server["environment_name"])
	}
	if server["revision"] != "test-rev" {
		t.Errorf("revision = %v, want test-rev (server config)", server["revision"])
	}
}

func TestProxyScrubsErrorMessageBacktraceAndReferer(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	postNotice(t, p, "application/json", `{
		"error": {
			"class": "Error",
			"message": "GET https://app.example.com/api/tm/lookup?source_text=CUSTOMER+SEGMENT failed",
			"backtrace": [
				{"file": "https://app.example.com/editor?source_text=LEAK", "method": "eval", "number": 1},
				{"file": "https://app.example.com/assets/index-abc.js", "method": "fn", "number": 2}
			]
		},
		"request": {
			"cgi_data": {
				"HTTP_REFERER": "https://app.example.com/projects?q=confidential-filename",
				"HTTP_USER_AGENT": "Mozilla/5.0"
			},
			"context": {"requests": [{"token": "leaky", "safe": "ok"}]}
		}
	}`)

	errObj, _ := hb.lastBody["error"].(map[string]any)
	if got := errObj["message"]; got != "GET https://app.example.com/api/tm/lookup failed" {
		t.Errorf("error.message = %v, want embedded URL query stripped", got)
	}
	frames, _ := errObj["backtrace"].([]any)
	if len(frames) != 2 {
		t.Fatalf("backtrace has %d frames, want 2", len(frames))
	}
	if got := frames[0].(map[string]any)["file"]; got != "https://app.example.com/editor" {
		t.Errorf("backtrace[0].file = %v, want query stripped", got)
	}
	if got := frames[1].(map[string]any)["file"]; got != "https://app.example.com/assets/index-abc.js" {
		t.Errorf("backtrace[1].file = %v, want untouched", got)
	}
	req, _ := hb.lastBody["request"].(map[string]any)
	cgi, _ := req["cgi_data"].(map[string]any)
	if got := cgi["HTTP_REFERER"]; got != "https://app.example.com/projects" {
		t.Errorf("HTTP_REFERER = %v, want query stripped (?q= carries free text)", got)
	}
	if got := cgi["HTTP_USER_AGENT"]; got != "Mozilla/5.0" {
		t.Errorf("HTTP_USER_AGENT = %v, want untouched", got)
	}
	// Sensitive keys inside ARRAYS of objects are redacted too.
	ctx, _ := req["context"].(map[string]any)
	arr, _ := ctx["requests"].([]any)
	first, _ := arr[0].(map[string]any)
	if first["token"] != "[REDACTED]" {
		t.Errorf("array-nested token = %v, want redacted", first["token"])
	}
	if first["safe"] != "ok" {
		t.Errorf("array-nested safe = %v, want kept", first["safe"])
	}
}

func TestProxyScrubsNotice(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	postNotice(t, p, "application/json", `{
		"error": {"class": "Error", "message": "x"},
		"breadcrumbs": {"enabled": true, "trail": [{"message": "GET /api/tm/lookup?source_text=SECRET"}]},
		"request": {
			"url": "https://app.example.com/editor?source_text=CUSTOMER+CONTENT#frag",
			"session": {"anything": "at all"},
			"context": {
				"authorization": "Bearer abc123",
				"nested": {"api_key": "xyz", "safe": "keep-me"}
			},
			"params": {"password": "hunter2"},
			"cgi_data": {"HTTP_COOKIE": "session=abc"}
		}
	}`)

	if _, present := hb.lastBody["breadcrumbs"]; present {
		t.Error("breadcrumbs survived — must be dropped wholesale")
	}
	req, _ := hb.lastBody["request"].(map[string]any)
	if req == nil {
		t.Fatal("forwarded notice has no request block")
	}
	if _, present := req["session"]; present {
		t.Error("request.session survived — must be dropped")
	}
	if got := req["url"]; got != "https://app.example.com/editor" {
		t.Errorf("url = %v, want query+fragment stripped", got)
	}
	ctx, _ := req["context"].(map[string]any)
	if ctx["authorization"] != "[REDACTED]" {
		t.Errorf("context.authorization = %v, want redacted", ctx["authorization"])
	}
	nested, _ := ctx["nested"].(map[string]any)
	if nested["api_key"] != "[REDACTED]" {
		t.Errorf("nested api_key = %v, want redacted", nested["api_key"])
	}
	if nested["safe"] != "keep-me" {
		t.Errorf("nested safe = %v, want kept", nested["safe"])
	}
	params, _ := req["params"].(map[string]any)
	if params["password"] != "[REDACTED]" {
		t.Errorf("params.password = %v, want redacted", params["password"])
	}
	cgi, _ := req["cgi_data"].(map[string]any)
	if cgi["HTTP_COOKIE"] != "[REDACTED]" {
		t.Errorf("cgi_data.HTTP_COOKIE = %v, want redacted", cgi["HTTP_COOKIE"])
	}
}

func TestProxyDisabledWithoutKey(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := newErrtrackProxy(Deps{HoneybadgerEndpoint: hb.URL}) // no key

	res := postNotice(t, p, "application/json", `{"error":{}}`)

	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when reporting is disabled", res.Code)
	}
	if hb.calls != 0 {
		t.Errorf("HB called %d times, want 0 — nothing may be forwarded without a key", hb.calls)
	}
}

func TestProxyRejectsBadInput(t *testing.T) {
	hb := newFakeHoneybadger(t)
	p := testProxy(hb.URL)

	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{"not json content type", "text/plain", `{"error":{}}`, http.StatusBadRequest},
		{"malformed json", "application/json", `{"error":`, http.StatusBadRequest},
		{"json but not an object", "application/json", `[1,2,3]`, http.StatusBadRequest},
		{"oversized body", "application/json", `{"pad":"` + strings.Repeat("x", noticeMaxBytes) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := hb.calls
			res := postNotice(t, p, tc.contentType, tc.body)
			if res.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", res.Code, tc.wantStatus)
			}
			if hb.calls != before {
				t.Error("rejected notice must not be forwarded")
			}
		})
	}
}

// TestProxyRateLimitNotBypassedByForgedXFF — X-Forwarded-For is client-
// controlled; randomizing it must NOT mint a fresh token bucket per request
// (the limiter is the sole defense of this unauthenticated endpoint).
func TestProxyRateLimitNotBypassedByForgedXFF(t *testing.T) {
	hb := newFakeHoneybadger(t)
	captureLogs(t)
	r := testRouter(t, func(r chi.Router) {
		p := testProxy(hb.URL)
		lim := httputil.NewRateLimiter(10.0/60.0, 5)
		// One trusted proxy, as in production.
		r.With(httputil.ClientIP(1), lim.Middleware).Post("/api/errtrack/v1/notices/js", p.handleNotice)
	})

	var last int
	for i := 0; i < 8; i++ {
		req := httptest.NewRequest("POST", "/api/errtrack/v1/notices/js", bytes.NewReader([]byte(`{"error":{}}`)))
		req.Header.Set("Content-Type", "application/json")
		// Attacker randomizes the leftmost (client-supplied) hops; the
		// rightmost hop — what our LB appends — stays the real address.
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.99.%d.%d, 203.0.113.7", i, i))
		req.RemoteAddr = "192.0.2.1:1234"
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		last = res.Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("8th forged-XFF request status = %d, want 429", last)
	}
	if hb.calls != 5 {
		t.Errorf("HB received %d notices, want the burst of 5 regardless of forged XFF", hb.calls)
	}
}

// TestProxyUpstreamFailureNotSelfReported — a 502 from the proxy means
// "Honeybadger is unreachable"; reporting that to Honeybadger goes nowhere,
// so requestObservability exempts this route from 5xx capture.
func TestProxyUpstreamFailureNotSelfReported(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	// An endpoint that refuses connections: bind, then close immediately.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	r := testRouter(t, func(r chi.Router) {
		p := testProxy(deadURL)
		r.Post("/api/errtrack/v1/notices/js", p.handleNotice)
	})

	req := httptest.NewRequest("POST", "/api/errtrack/v1/notices/js", strings.NewReader(`{"error":{}}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 when upstream is down", res.Code)
	}
	if rec.Len() != 0 {
		t.Errorf("captured %d notices, want 0 — a Honeybadger outage must not self-report", rec.Len())
	}
}

func TestConfigEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name          string
		key           string
		wantReporting bool
	}{
		{"reporting enabled", "some-key", true},
		{"reporting disabled", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := Deps{Revision: "abc123", Environment: "staging", HoneybadgerAPIKey: tc.key, BaseURL: "https://app.example.com"}
			captureLogs(t)
			r := testRouter(t, func(r chi.Router) {
				r.Get("/api/config", handleAppConfig(deps))
			})

			res := httptest.NewRecorder()
			r.ServeHTTP(res, httptest.NewRequest("GET", "/api/config", nil))

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 without auth", res.Code)
			}
			var body map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			if len(body) != 10 {
				t.Errorf("response has %d keys, want exactly version/environment/error_reporting/app_url/docs_host/docs_url/developer_host/developer_url/playground/issues", len(body))
			}
			if body["issues"] != false {
				t.Errorf("issues = %v, want false without platform admins", body["issues"])
			}
			if body["app_url"] != "https://app.example.com" {
				t.Errorf("app_url = %v, want https://app.example.com", body["app_url"])
			}
			if body["version"] != "abc123" || body["environment"] != "staging" {
				t.Errorf("version/environment = %v/%v, want abc123/staging", body["version"], body["environment"])
			}
			if body["error_reporting"] != tc.wantReporting {
				t.Errorf("error_reporting = %v, want %v", body["error_reporting"], tc.wantReporting)
			}
			if tc.key != "" && strings.Contains(res.Body.String(), tc.key) {
				t.Error("the API key leaked into the /api/config response")
			}
		})
	}
}

// docs_host says whether the request came in on DOCS_HOST, so the frontend
// never has to compare BASE_URL with the address it is served on.
func TestConfigEndpoint_DocsHost(t *testing.T) {
	deps := Deps{BaseURL: "http://localhost:5173", DocsHost: "Docs.Example.com"}
	captureLogs(t)
	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/config", handleAppConfig(deps))
	})
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"docs.example.com", true},
		{"docs.example.com:443", true},
		{"localhost:9000", false},
		{"app.example.com", false},
	} {
		req := httptest.NewRequest("GET", "/api/config", nil)
		req.Host = tc.host
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		var body map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: response is not JSON: %v", tc.host, err)
		}
		if body["docs_host"] != tc.want {
			t.Errorf("%s: docs_host = %v, want %v", tc.host, body["docs_host"], tc.want)
		}
		if body["docs_url"] != "http://docs.example.com:5173" {
			t.Errorf("%s: docs_url = %v, want http://docs.example.com:5173", tc.host, body["docs_url"])
		}
	}

	off := testRouter(t, func(r chi.Router) {
		r.Get("/api/config", handleAppConfig(Deps{BaseURL: "http://localhost:5173"}))
	})
	req := httptest.NewRequest("GET", "/api/config", nil)
	req.Host = "docs.example.com"
	res := httptest.NewRecorder()
	off.ServeHTTP(res, req)
	if !strings.Contains(res.Body.String(), `"docs_host":false`) {
		t.Errorf("without DOCS_HOST, docs_host must be false: %s", res.Body)
	}
	if !strings.Contains(res.Body.String(), `"docs_url":""`) {
		t.Errorf("without DOCS_HOST, docs_url must be empty: %s", res.Body)
	}
}

// developer_host / developer_url do for the API docs what docs_host /
// docs_url do for the help.
func TestConfigEndpoint_DeveloperHost(t *testing.T) {
	deps := Deps{BaseURL: "https://app.example.com", DocsHost: "docs.example.com", DeveloperHost: "developer.example.com"}
	captureLogs(t)
	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/config", handleAppConfig(deps))
	})
	for _, tc := range []struct {
		host            string
		docs, developer bool
	}{
		{"developer.example.com", false, true},
		{"docs.example.com", true, false},
		{"app.example.com", false, false},
	} {
		req := httptest.NewRequest("GET", "/api/config", nil)
		req.Host = tc.host
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		var body map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: response is not JSON: %v", tc.host, err)
		}
		if body["docs_host"] != tc.docs || body["developer_host"] != tc.developer {
			t.Errorf("%s: docs_host/developer_host = %v/%v, want %v/%v", tc.host, body["docs_host"], body["developer_host"], tc.docs, tc.developer)
		}
		if body["developer_url"] != "https://developer.example.com" || body["docs_url"] != "https://docs.example.com" {
			t.Errorf("%s: urls = %v / %v", tc.host, body["docs_url"], body["developer_url"])
		}
	}
}
