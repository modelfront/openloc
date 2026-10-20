package auth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/api"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

const (
	relayDomain = "app.openloc.com"
	prodBase    = "https://app.openloc.com"
	branchBase  = "https://product-docs.app.openloc.com"
	branchHost  = "product-docs.app.openloc.com"
)

// relayRouter builds a router as one deployment sees the world: selfBase is its
// own BASE_URL, redirectBase the origin registered with the provider, domain the
// parent domain it may forward callbacks to ("" disables relaying).
func relayRouter(pool *pgxpool.Pool, selfBase, redirectBase, domain string) chi.Router {
	return api.NewRouter(api.Deps{
		Queries:               db.New(pool),
		DB:                    pool,
		JWTSecret:             testutil.TestJWTSecret,
		GoogleClientID:        "test-client-id",
		GoogleClientSecret:    "test-client-secret",
		MicrosoftClientID:     "test-client-id",
		MicrosoftClientSecret: "test-client-secret",
		BaseURL:               selfBase,
		OAuthRedirectBaseURL:  redirectBase,
		OAuthRelayDomain:      domain,
	})
}

// callback issues a callback request carrying `state`, with NO state cookie —
// the relay hop must not need one, because the cookie belongs to the branch.
func callback(t *testing.T, router chi.Router, path, state string, extra url.Values) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"code": {"auth-code-abc"}, "state": {state}}
	for k, vs := range extra {
		q[k] = vs
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", path+"?"+q.Encode(), nil))
	return rec
}

func TestOAuthCallback_RelaysToBranchHost(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	// The production deployment: it owns the registered redirect URI and is
	// willing to forward to its own subdomains.
	router := relayRouter(pool, prodBase, prodBase, relayDomain)

	for _, provider := range []string{"google", "microsoft"} {
		t.Run(provider, func(t *testing.T) {
			path := "/api/auth/" + provider + "/callback"
			state := "h=" + branchHost + "|nonce123"
			rec := callback(t, router, path, state, nil)

			if rec.Code != http.StatusTemporaryRedirect {
				t.Fatalf("expected 307, got %d: %s", rec.Code, rec.Body.String())
			}
			loc, err := url.Parse(rec.Header().Get("Location"))
			if err != nil {
				t.Fatalf("parse Location: %v", err)
			}
			if loc.Scheme != "https" || loc.Host != branchHost {
				t.Errorf("expected forward to https://%s, got %s", branchHost, loc)
			}
			if loc.Path != path {
				t.Errorf("expected path %s, got %s", path, loc.Path)
			}
			if got := loc.Query().Get("code"); got != "auth-code-abc" {
				t.Errorf("code = %q, want auth-code-abc", got)
			}
			if got := loc.Query().Get("state"); got != state {
				t.Errorf("state = %q, want %q", got, state)
			}
		})
	}
}

// The relay rebuilds the forwarded query from a fixed set of OAuth parameters.
// Passing the provider's raw query string through would let anyone append
// arbitrary parameters to a URL on one of our own hosts.
func TestOAuthCallback_RelayDropsUnknownParams(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := relayRouter(pool, prodBase, prodBase, relayDomain)
	rec := callback(t, router, "/api/auth/google/callback", "h="+branchHost+"|nonce123",
		url.Values{"invite": {"smuggled"}, "redirect": {"https://evil.com"}})

	loc := rec.Header().Get("Location")
	for _, unwanted := range []string{"invite", "smuggled", "redirect", "evil.com"} {
		if strings.Contains(loc, unwanted) {
			t.Errorf("forwarded URL leaked %q: %s", unwanted, loc)
		}
	}
}

func TestOAuthCallback_RelayForwardsProviderError(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := relayRouter(pool, prodBase, prodBase, relayDomain)
	rec := callback(t, router, "/api/auth/google/callback", "h="+branchHost+"|nonce123",
		url.Values{"error": {"access_denied"}})

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Host != branchHost {
		t.Fatalf("expected forward to %s, got %s", branchHost, loc)
	}
	if got := loc.Query().Get("error"); got != "access_denied" {
		t.Errorf("error = %q, want access_denied — the branch shows the message", got)
	}
}

func TestOAuthCallback_RelayRefusals(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tests := []struct {
		name   string
		domain string
		state  string
	}{
		{"relaying disabled", "", "h=" + branchHost + "|nonce123"},
		{"host outside the allowed domain", relayDomain, "h=evil.com|nonce123"},
		{"nested label", relayDomain, "h=a.b." + relayDomain + "|nonce123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := relayRouter(pool, prodBase, prodBase, tt.domain)
			rec := callback(t, router, "/api/auth/google/callback", tt.state, nil)

			loc := rec.Header().Get("Location")
			if !strings.HasPrefix(loc, prodBase+"/login?error=") {
				t.Fatalf("expected a local login error, got %s", loc)
			}
			if strings.Contains(loc, "auth-code-abc") {
				t.Errorf("refused relay still leaked the auth code: %s", loc)
			}
		})
	}
}

func TestOAuthLogin_StampsOwnHost(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	// A branch deployment: its own host, but production's redirect URI.
	router := relayRouter(pool, branchBase, prodBase, relayDomain)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/google", nil))
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d", rec.Code)
	}

	var stateCookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "__oauth_state" {
			stateCookie = c.Value
		}
	}
	want := "h=" + branchHost + "|"
	if !strings.HasPrefix(stateCookie, want) {
		t.Errorf("state cookie = %q, want prefix %q", stateCookie, want)
	}

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	// The provider echoes `state` verbatim, and the callback compares it to the
	// cookie — so a mismatch here would reject every branch sign-in.
	if got := loc.Query().Get("state"); got != stateCookie {
		t.Errorf("state sent to provider = %q, cookie = %q", got, stateCookie)
	}
	if got := loc.Query().Get("redirect_uri"); got != prodBase+"/api/auth/google/callback" {
		t.Errorf("redirect_uri = %q, want the registered production URI", got)
	}
}

func TestOAuthLogin_NoStampWhenSelfOwnsRedirectURI(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := relayRouter(pool, prodBase, prodBase, relayDomain)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/google", nil))

	for _, c := range rec.Result().Cookies() {
		if c.Name == "__oauth_state" && strings.Contains(c.Value, "h=") {
			t.Errorf("production stamped a host into the state: %q", c.Value)
		}
	}
}
