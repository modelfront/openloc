// OAuth 2.1 authorization server against a real DB.
// The suite is deliberately adversarial: every test that isn't the happy path
// is an attack that must fail — stolen-code replay, PKCE downgrade, redirect
// hijack, scope escalation via form tampering, refresh-token replay, audience
// escape. The server earns directory submission by surviving this file.
package oauthserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/oauthserver"
	"openloc/internal/testutil"
)

const (
	jwtSecret = "oauth-test-secret"
	// A valid PKCE verifier (43+ chars) and its fixed S256 challenge are
	// computed in the fixture; tests use mismatched pairs to attack.
	verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk-verifier"
)

type fixture struct {
	t      *testing.T
	q      db.Querier
	pool   *pgxpool.Pool
	router chi.Router

	tenantA  db.Tenant
	tenantB  db.Tenant
	owner    db.User // member of A only
	clientID string
	redirect string
	srv      *oauthserver.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)

	f := &fixture{t: t, q: q, pool: pool}
	f.tenantA = testutil.SeedTenant(t, q, "OAuthTenantA")
	f.tenantB = testutil.SeedTenant(t, q, "OAuthTenantB")
	f.owner = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "oauth-a@test.com", "pass", "owner")

	srv := oauthserver.NewServer(q, jwtSecret, "https://app.example.com")
	f.srv = srv

	r := chi.NewRouter()
	r.Get("/.well-known/oauth-authorization-server", srv.HandleAuthServerMetadata)
	r.Get("/.well-known/oauth-protected-resource", srv.HandleProtectedResourceMetadata)
	r.Get("/oauth/authorize", srv.HandleAuthorize)
	r.Post("/oauth/authorize", srv.HandleAuthorize)
	r.Post("/oauth/token", srv.HandleToken)
	r.Post("/oauth/register", srv.HandleRegister)

	// Mirror api.go's explicit /mcp chain: WWW-Authenticate wrapper OUTSIDE
	// the auth middleware, echo handler standing in for the MCP server.
	authMW := auth.Middleware(jwtSecret,
		func(ctx context.Context, id int64) (string, error) { return "oauth-a@test.com", nil },
		nil, // tenantActive: nil skips the approval check
		auth.NewPATResolver(q),
		oauthserver.NewResolver(q),
	)
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := auth.ClaimsFromContext(r.Context())
		info := auth.PATInfoFromContext(r.Context())
		httputil.JSON(w, 200, map[string]any{
			"tenant_id": claims.TenantID, "user_id": claims.UserID,
			"role": claims.Role, "capability": info.Capability, "client": info.ClientName,
		})
	})
	r.With(srv.WWWAuthenticate401, authMW).Handle("/mcp", echo)
	r.With(authMW).Handle("/api/echo", echo) // a non-/mcp route for audience tests

	// Connections API (authenticated group in prod).
	r.With(authMW).Get("/api/oauth/connections", srv.HandleListConnections)
	r.With(authMW).Delete("/api/oauth/connections/{familyID}", srv.HandleRevokeConnection)
	f.router = r

	// One DCR client for the default flows.
	f.redirect = "https://claude.ai/api/mcp/auth_callback"
	rec := f.do("POST", "/oauth/register", jsonBody(t, map[string]any{
		"client_name": "Claude", "redirect_uris": []string{f.redirect},
	}), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reg); err != nil || reg.ClientID == "" {
		t.Fatalf("register response: %s", rec.Body.String())
	}
	f.clientID = reg.ClientID
	return f
}

func jsonBody(t *testing.T, v any) *strings.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(b))
}

// do runs a request; headers optional.
func (f *fixture) do(method, path string, body *strings.Reader, headers map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, body)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if method == "POST" {
		if strings.HasPrefix(path, "/oauth/register") {
			req.Header.Set("Content-Type", "application/json")
		} else {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// session returns an Authorization header for the owner's browser session JWT.
func (f *fixture) session() map[string]string {
	return f.sessionFor(f.owner.ID, f.tenantA.ID, "owner")
}

// sessionFor mints a browser session JWT for any (user, tenant, role).
func (f *fixture) sessionFor(userID, tenantID int64, role string) map[string]string {
	f.t.Helper()
	tok, err := auth.GenerateToken(userID, tenantID, role, jwtSecret, time.Hour)
	if err != nil {
		f.t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + tok}
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// authorize walks GET consent → POST approve and returns the issued code.
func (f *fixture) authorize(scope string, tenantID int64) string {
	f.t.Helper()
	challenge := oauthserver.PKCEChallengeForTest(verifier)
	q := url.Values{
		"client_id": {f.clientID}, "redirect_uri": {f.redirect},
		"response_type": {"code"}, "state": {"xyz"}, "scope": {scope},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	rec := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, f.session())
	if rec.Code != http.StatusOK {
		f.t.Fatalf("consent GET: %d %s", rec.Code, rec.Body.String())
	}
	m := csrfRe.FindStringSubmatch(rec.Body.String())
	if m == nil {
		f.t.Fatalf("no csrf field in consent page")
	}

	form := url.Values{}
	for k, v := range q {
		form[k] = v
	}
	form.Set("csrf", m[1])
	form.Set("decision", "approve")
	form.Set("tenant_id", fmt.Sprint(tenantID))
	rec = f.do("POST", "/oauth/authorize", strings.NewReader(form.Encode()), f.session())
	if rec.Code != http.StatusFound {
		f.t.Fatalf("consent POST: %d %s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || loc.Query().Get("code") == "" {
		f.t.Fatalf("no code in redirect: %s", rec.Header().Get("Location"))
	}
	if loc.Query().Get("state") != "xyz" {
		f.t.Fatalf("state not echoed: %s", loc.String())
	}
	return loc.Query().Get("code")
}

type tokenPair struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Scope   string `json:"scope"`
}

func (f *fixture) exchange(code, verifier string) (*tokenPair, *httptest.ResponseRecorder) {
	f.t.Helper()
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {verifier}, "client_id": {f.clientID}, "redirect_uri": {f.redirect},
	}
	rec := f.do("POST", "/oauth/token", strings.NewReader(form.Encode()), nil)
	if rec.Code != http.StatusOK {
		return nil, rec
	}
	var tp tokenPair
	if err := json.Unmarshal(rec.Body.Bytes(), &tp); err != nil {
		f.t.Fatalf("token response: %s", rec.Body.String())
	}
	return &tp, rec
}

func (f *fixture) mcp(token string) *httptest.ResponseRecorder {
	return f.do("GET", "/mcp", nil, map[string]string{"Authorization": "Bearer " + token})
}

// --- The suite ---------------------------------------------------------------

func TestIntegrationOAuthDiscoveryShape(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/.well-known/oauth-authorization-server", nil, nil)
	var meta map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["issuer"] != "https://app.example.com" ||
		meta["token_endpoint"] != "https://app.example.com/oauth/token" ||
		fmt.Sprint(meta["code_challenge_methods_supported"]) != "[S256]" ||
		meta["client_id_metadata_document_supported"] != true {
		t.Fatalf("metadata: %v", meta)
	}
	rec = f.do("GET", "/.well-known/oauth-protected-resource", nil, nil)
	var prm map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &prm)
	if prm["resource"] != "https://app.example.com/mcp" {
		t.Fatalf("resource metadata: %v", prm)
	}

	// The 401 handshake: an uncredentialed /mcp hit must point at the metadata.
	rec = f.do("GET", "/mcp", nil, nil)
	if rec.Code != http.StatusUnauthorized ||
		!strings.Contains(rec.Header().Get("WWW-Authenticate"), "oauth-protected-resource") {
		t.Fatalf("401 handshake: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}

func TestIntegrationOAuthHappyPathReadAndWrite(t *testing.T) {
	f := newFixture(t)

	// Read-scope grant: token works on /mcp with read capability.
	code := f.authorize("read", f.tenantA.ID)
	tp, _ := f.exchange(code, verifier)
	rec := f.mcp(tp.Access)
	var echo map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &echo)
	if rec.Code != 200 || echo["capability"] != "read" ||
		int64(echo["tenant_id"].(float64)) != f.tenantA.ID || echo["role"] != "owner" {
		t.Fatalf("read grant echo: %d %v", rec.Code, echo)
	}
	if echo["client"] != "Claude" {
		t.Fatalf("client name missing from info: %v", echo)
	}

	// Write-scope grant carries write capability.
	code = f.authorize("write", f.tenantA.ID)
	tpw, _ := f.exchange(code, verifier)
	_ = json.Unmarshal(f.mcp(tpw.Access).Body.Bytes(), &echo)
	if echo["capability"] != "write" || tpw.Scope != "write" {
		t.Fatalf("write grant: %v scope=%s", echo, tpw.Scope)
	}
}

// The token is AUDIENCE-BOUND: valid on /mcp, dead everywhere else.
func TestIntegrationOAuthTokenIsMCPOnly(t *testing.T) {
	f := newFixture(t)
	tp, _ := f.exchange(f.authorize("write", f.tenantA.ID), verifier)
	rec := f.do("GET", "/api/echo", nil, map[string]string{"Authorization": "Bearer " + tp.Access})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("OAuth token accepted outside /mcp: %d", rec.Code)
	}
}

func TestIntegrationOAuthCodeAttacks(t *testing.T) {
	f := newFixture(t)

	// Wrong PKCE verifier: rejected, and the code is BURNED by the attempt.
	code := f.authorize("read", f.tenantA.ID)
	if _, rec := f.exchange(code, "wrong-verifier-wrong-verifier-wrong-verifier"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad verifier: %d", rec.Code)
	}
	if _, rec := f.exchange(code, verifier); rec.Code != http.StatusBadRequest {
		t.Fatalf("burned code retried successfully: %d", rec.Code)
	}

	// Replay: a redeemed code must never redeem twice.
	code = f.authorize("read", f.tenantA.ID)
	if tp, _ := f.exchange(code, verifier); tp == nil {
		t.Fatal("first exchange failed")
	}
	if _, rec := f.exchange(code, verifier); rec.Code != http.StatusBadRequest {
		t.Fatalf("code replay: %d", rec.Code)
	}

	// Wrong client_id at exchange.
	code = f.authorize("read", f.tenantA.ID)
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {verifier}, "client_id": {"dcr_someoneelse"}, "redirect_uri": {f.redirect},
	}
	if rec := f.do("POST", "/oauth/token", strings.NewReader(form.Encode()), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong client exchange: %d", rec.Code)
	}
}

func TestIntegrationOAuthAuthorizeRejections(t *testing.T) {
	f := newFixture(t)
	challenge := oauthserver.PKCEChallengeForTest(verifier)
	base := url.Values{
		"client_id": {f.clientID}, "redirect_uri": {f.redirect},
		"response_type": {"code"}, "scope": {"read"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}

	// Unregistered redirect_uri: 400 ERROR PAGE, never a redirect.
	q := cloneValues(base)
	q.Set("redirect_uri", "https://evil.example/steal")
	rec := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, f.session())
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
		t.Fatalf("hostile redirect_uri: %d loc=%q", rec.Code, rec.Header().Get("Location"))
	}

	// Missing PKCE: protocol error redirected to the (valid) client.
	q = cloneValues(base)
	q.Del("code_challenge")
	rec = f.do("GET", "/oauth/authorize?"+q.Encode(), nil, f.session())
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "error=invalid_request") {
		t.Fatalf("missing PKCE: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	// No session: bounce to login with next.
	rec = f.do("GET", "/oauth/authorize?"+base.Encode(), nil, nil)
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/login?next=") {
		t.Fatalf("anonymous authorize: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	// Unknown scope value → invalid_scope.
	q = cloneValues(base)
	q.Set("scope", "admin")
	rec = f.do("GET", "/oauth/authorize?"+q.Encode(), nil, f.session())
	if !strings.Contains(rec.Header().Get("Location"), "error=invalid_scope") {
		t.Fatalf("scope=admin: %s", rec.Header().Get("Location"))
	}

	// Machine credentials must NOT drive a consent approval (SessionClaims
	// rejects them): a PAT and an OAuth access token both bounce to /login
	// instead of rendering the consent page. This is the privilege-escalation
	// boundary — a stolen MCP token must not mint fresh grants.
	pat := testutil.SeedAPIToken(t, f.q, f.tenantA.ID, f.owner.ID, auth.CapabilityWrite, time.Hour)
	oauthTok, _ := f.exchange(f.authorize("read", f.tenantA.ID), verifier)
	for name, bearer := range map[string]string{"PAT": pat, "OAuth access token": oauthTok.Access} {
		rec = f.do("GET", "/oauth/authorize?"+base.Encode(), nil, map[string]string{"Authorization": "Bearer " + bearer})
		if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/login?next=") {
			t.Fatalf("%s drove consent instead of bouncing to login: %d %s", name, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// Expiry paths: an authorization code past its expires_at, and a consent CSRF
// nonce from a prior time bucket, must both be rejected. Without these the
// expiry checks in token.go / authorize.go could be silently removed.
func TestIntegrationOAuthExpiry(t *testing.T) {
	f := newFixture(t)

	// Auth-code expiry: issue a code, backdate its expires_at, exchange fails.
	code := f.authorize("read", f.tenantA.ID)
	if _, err := f.pool.Exec(context.Background(),
		"UPDATE oauth_auth_codes SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatalf("backdate code: %v", err)
	}
	if _, rec := f.exchange(code, verifier); rec.Code != http.StatusBadRequest {
		t.Fatalf("expired code exchanged: %d", rec.Code)
	}

	// Consent CSRF expiry: a nonce minted one bucket ago must 403. Drive the
	// POST directly with a stale nonce (bypassing the GET that mints a current
	// one), everything else valid.
	challenge := oauthserver.PKCEChallengeForTest(verifier)
	form := url.Values{
		"client_id": {f.clientID}, "redirect_uri": {f.redirect},
		"response_type": {"code"}, "scope": {"read"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"decision": {"approve"}, "tenant_id": {fmt.Sprint(f.tenantA.ID)},
		"csrf": {f.srv.ConsentNonceAtBucketForTest(f.owner.ID, f.clientID, 1)},
	}
	rec := f.do("POST", "/oauth/authorize", strings.NewReader(form.Encode()), f.session())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stale CSRF nonce accepted: %d", rec.Code)
	}
}

// The consent screen must show the redirect destination host so a user can
// spot a client whose (self-chosen) DCR name doesn't match where the grant
// actually goes — the name-spoofing defense.
func TestIntegrationOAuthConsentShowsRedirectHost(t *testing.T) {
	f := newFixture(t)
	challenge := oauthserver.PKCEChallengeForTest(verifier)
	q := url.Values{
		"client_id": {f.clientID}, "redirect_uri": {f.redirect},
		"response_type": {"code"}, "scope": {"read"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	rec := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, f.session())
	if rec.Code != http.StatusOK {
		t.Fatalf("consent GET: %d", rec.Code)
	}
	// Assert the VISIBLE destination marker, not a bare substring — the hidden
	// redirect_uri form field also contains "claude.ai", so matching that
	// would pass even if the visible host display were deleted.
	if !strings.Contains(rec.Body.String(), "to <strong>claude.ai</strong>") {
		t.Fatalf("consent screen does not show the redirect host: %s", rec.Body.String())
	}
}

// The consent screen says what each scope allows: a write grant is
// everything the user's role allows, not a list of a few tools.
func TestIntegrationOAuthConsentDescribesScope(t *testing.T) {
	f := newFixture(t)
	for scope, want := range map[string]string{
		"read":  "Read-only access</strong>\n    View projects, jobs, translation memories, terminology, QA reports and metrics. Nothing can be modified.",
		"write": "Read and write access</strong>\n    Do what you can do in OpenLoc yourself, with your role.",
	} {
		q := url.Values{
			"client_id": {f.clientID}, "redirect_uri": {f.redirect},
			"response_type": {"code"}, "scope": {scope},
			"code_challenge": {oauthserver.PKCEChallengeForTest(verifier)}, "code_challenge_method": {"S256"},
		}
		rec := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, f.session())
		if rec.Code != http.StatusOK {
			t.Fatalf("%s consent GET: %d", scope, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s consent screen does not say %q: %s", scope, want, rec.Body.String())
		}
	}
}

// Both pages a user meets mid-connection — the consent screen and the error
// page — carry the OpenLoc favicon and wordmark, so the user can tell they are
// on OpenLoc and not on the tool that sent them.
func TestIntegrationOAuthPagesAreBranded(t *testing.T) {
	f := newFixture(t)
	challenge := oauthserver.PKCEChallengeForTest(verifier)
	q := url.Values{
		"client_id": {f.clientID}, "redirect_uri": {f.redirect},
		"response_type": {"code"}, "scope": {"read"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	consent := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, f.session())
	if consent.Code != http.StatusOK {
		t.Fatalf("consent GET: %d", consent.Code)
	}
	q.Set("redirect_uri", "https://evil.example/steal")
	errPage := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, f.session())
	if errPage.Code != http.StatusBadRequest {
		t.Fatalf("error page: %d", errPage.Code)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{"consent": consent, "error": errPage} {
		body := rec.Body.String()
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s page Content-Type = %q", name, ct)
		}
		for _, want := range []string{`rel="icon"`, `href="/favicon.svg"`, `<span class="wordmark">OpenLoc</span>`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s page lacks %s", name, want)
			}
		}
	}
	if !strings.Contains(errPage.Body.String(), "The redirect address does not match the client&#39;s registration.") {
		t.Errorf("error page lost its message: %s", errPage.Body.String())
	}
}

// A consent POST whose body cannot be parsed is a 400 on the same branded
// error page, never a blank 200.
func TestIntegrationOAuthMalformedConsentForm(t *testing.T) {
	f := newFixture(t)
	rec := f.do("POST", "/oauth/authorize", strings.NewReader("client_id=%zz"), f.session())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed form: %d, want 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"Malformed form submission.", `<span class="wordmark">OpenLoc</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("malformed-form page lacks %s", want)
		}
	}
}

// Form tampering: approving for a tenant the user is NOT a member of.
func TestIntegrationOAuthConsentTenantTampering(t *testing.T) {
	f := newFixture(t)
	challenge := oauthserver.PKCEChallengeForTest(verifier)
	q := url.Values{
		"client_id": {f.clientID}, "redirect_uri": {f.redirect},
		"response_type": {"code"}, "scope": {"read"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	rec := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, f.session())
	m := csrfRe.FindStringSubmatch(rec.Body.String())

	form := cloneValues(q)
	form.Set("csrf", m[1])
	form.Set("decision", "approve")
	form.Set("tenant_id", fmt.Sprint(f.tenantB.ID)) // not a member!
	rec = f.do("POST", "/oauth/authorize", strings.NewReader(form.Encode()), f.session())
	if !strings.Contains(rec.Header().Get("Location"), "error=access_denied") {
		t.Fatalf("cross-tenant consent accepted: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	// And a garbage CSRF is a hard 403, no redirect.
	form.Set("csrf", "forged")
	form.Set("tenant_id", fmt.Sprint(f.tenantA.ID))
	rec = f.do("POST", "/oauth/authorize", strings.NewReader(form.Encode()), f.session())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged csrf: %d", rec.Code)
	}
}

func TestIntegrationOAuthRefreshRotationAndReplay(t *testing.T) {
	f := newFixture(t)
	tp, _ := f.exchange(f.authorize("read", f.tenantA.ID), verifier)

	refreshForm := func(rt string) *strings.Reader {
		return strings.NewReader(url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {f.clientID},
		}.Encode())
	}

	// Rotate: new pair works, old access token is revoked with its row.
	rec := f.do("POST", "/oauth/token", refreshForm(tp.Refresh), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body.String())
	}
	var tp2 tokenPair
	_ = json.Unmarshal(rec.Body.Bytes(), &tp2)
	if f.mcp(tp2.Access).Code != 200 {
		t.Fatal("rotated access token dead")
	}
	if f.mcp(tp.Access).Code != http.StatusUnauthorized {
		t.Fatal("pre-rotation access token survived rotation")
	}

	// Replay the OLD refresh token: the entire family dies, including tp2.
	rec = f.do("POST", "/oauth/token", refreshForm(tp.Refresh), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("replayed refresh: %d", rec.Code)
	}
	if f.mcp(tp2.Access).Code != http.StatusUnauthorized {
		t.Fatal("family not revoked after refresh replay — token theft response missing")
	}
}

func TestIntegrationOAuthConnectionsListAndRevoke(t *testing.T) {
	f := newFixture(t)
	tp, _ := f.exchange(f.authorize("write", f.tenantA.ID), verifier)

	rec := f.do("GET", "/api/oauth/connections", nil, f.session())
	var env struct {
		Data []struct {
			FamilyID   string `json:"family_id"`
			ClientName string `json:"client_name"`
			Scope      string `json:"scope"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || len(env.Data) != 1 {
		t.Fatalf("connections: %s", rec.Body.String())
	}
	if env.Data[0].ClientName != "Claude" || env.Data[0].Scope != "write" {
		t.Fatalf("connection row: %+v", env.Data[0])
	}

	// Cross-principal isolation (every security-boundary query gets a test, per
	// CONTRIBUTING.md): another user in the SAME tenant, and a user in a DIFFERENT tenant,
	// must neither see nor revoke the owner's grant. If the queries dropped their
	// user_id/tenant_id scoping, these would fail.
	otherSame := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "oauth-other@test.com", "pass", "owner")
	otherTenant := testutil.SeedUserWithRole(t, f.q, f.tenantB.ID, "oauth-b@test.com", "pass", "owner")
	for _, sess := range []map[string]string{
		f.sessionFor(otherSame.ID, f.tenantA.ID, "owner"),
		f.sessionFor(otherTenant.ID, f.tenantB.ID, "owner"),
	} {
		var otherEnv struct {
			Data []json.RawMessage `json:"data"`
		}
		listRec := f.do("GET", "/api/oauth/connections", nil, sess)
		if err := json.Unmarshal(listRec.Body.Bytes(), &otherEnv); err != nil || len(otherEnv.Data) != 0 {
			t.Fatalf("other principal saw the owner's connection: %s", listRec.Body.String())
		}
		if delRec := f.do("DELETE", "/api/oauth/connections/"+env.Data[0].FamilyID, nil, sess); delRec.Code != http.StatusNotFound {
			t.Fatalf("other principal revoked the owner's grant: %d", delRec.Code)
		}
	}
	// The cross-principal DELETEs did NOT revoke it — owner's token still works.
	if f.mcp(tp.Access).Code != 200 {
		t.Fatal("owner's access token died from another principal's revoke attempt")
	}

	// The owner's own disconnect kills the live access token immediately.
	rec = f.do("DELETE", "/api/oauth/connections/"+env.Data[0].FamilyID, nil, f.session())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if f.mcp(tp.Access).Code != http.StatusUnauthorized {
		t.Fatal("access token survived disconnect")
	}
}

// CIMD: the client_id is a URL we fetch; the doc must claim that exact id.
func TestIntegrationOAuthCIMD(t *testing.T) {
	f := newFixture(t)

	var cimdURL string
	cimd := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":     cimdURL,
			"client_name":   "ChatGPT",
			"redirect_uris": []string{"https://chatgpt.com/connector_platform_oauth_redirect"},
		})
	}))
	defer cimd.Close()
	cimdURL = cimd.URL + "/client-metadata.json"

	// The fixture's server must trust the test TLS cert.
	srv := oauthserver.NewServer(f.q, jwtSecret, "https://app.example.com")
	srv.SetHTTPClientForTest(cimd.Client())
	r := chi.NewRouter()
	r.Get("/oauth/authorize", srv.HandleAuthorize)
	f.router.Mount("/cimd", r) // isolated mount to use the CIMD-aware server

	q := url.Values{
		"client_id": {cimdURL}, "redirect_uri": {"https://chatgpt.com/connector_platform_oauth_redirect"},
		"response_type": {"code"}, "scope": {"read"},
		"code_challenge": {oauthserver.PKCEChallengeForTest(verifier)}, "code_challenge_method": {"S256"},
	}
	rec := f.do("GET", "/cimd/oauth/authorize?"+q.Encode(), nil, f.session())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ChatGPT") {
		t.Fatalf("CIMD consent: %d %s", rec.Code, rec.Body.String()[:min(300, rec.Body.Len())])
	}

	// A doc claiming a different client_id is impersonation: rejected.
	evil := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":     "https://legit.example/metadata.json",
			"redirect_uris": []string{"https://evil.example/cb"},
		})
	}))
	defer evil.Close()
	srv.SetHTTPClientForTest(evil.Client())
	q.Set("client_id", evil.URL+"/m.json")
	q.Set("redirect_uri", "https://evil.example/cb")
	rec = f.do("GET", "/cimd/oauth/authorize?"+q.Encode(), nil, f.session())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("impersonating CIMD accepted: %d", rec.Code)
	}
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vals := range v {
		out[k] = append([]string{}, vals...)
	}
	return out
}
