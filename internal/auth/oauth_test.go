package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/api"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/seedid"
	"openloc/internal/testutil"
)

func TestHandleDevLogin_Enabled(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DevLoginCo")
	testutil.SeedUser(t, q, tenant.ID, seedid.OwnerEmail, "pass")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/dev-login", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Token string         `json:"token"`
		User  map[string]any `json:"user"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" {
		t.Error("expected non-empty token")
	}
	if resp.User["email"] != seedid.OwnerEmail {
		t.Errorf("expected %s, got %v", seedid.OwnerEmail, resp.User["email"])
	}

	// Verify cookie was set
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "openloc_token" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected openloc_token cookie to be set")
	}
}

func TestHandleDevLogin_Disabled(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := api.NewRouter(api.Deps{
		Queries: db.New(pool),
		// DB needed since Pass 1 wired the userpair handler in api.go,
		// which does deps.DB.(*pgxpool.Pool); without this the router
		// constructor panics regardless of which route the test hits.
		DB:           pool,
		JWTSecret:    testutil.TestJWTSecret,
		DevAutoLogin: false,
		BaseURL:      "http://localhost:5173",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/dev-login", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when dev-login disabled, got %d", rec.Code)
	}
}

func TestHandleAuthConfig(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/auth/config", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		GoogleEnabled    bool `json:"google_enabled"`
		MicrosoftEnabled bool `json:"microsoft_enabled"`
		DevLoginEnabled  bool `json:"dev_login_enabled"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.GoogleEnabled {
		t.Error("expected google_enabled=false (no client ID set)")
	}
	if resp.MicrosoftEnabled {
		t.Error("expected microsoft_enabled=false (no client ID set)")
	}
	if !resp.DevLoginEnabled {
		t.Error("expected dev_login_enabled=true (test router has it enabled)")
	}
}

func TestHandleAuthConfig_WithGoogle(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := api.NewRouter(api.Deps{
		Queries: db.New(pool),
		// DB required since Pass 1 wired userpair.NewHandler in api.go.
		DB:                 pool,
		JWTSecret:          testutil.TestJWTSecret,
		GoogleClientID:     "test-client-id",
		GoogleClientSecret: "test-client-secret",
		BaseURL:            "http://localhost:5173",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/auth/config", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		GoogleEnabled   bool `json:"google_enabled"`
		DevLoginEnabled bool `json:"dev_login_enabled"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !resp.GoogleEnabled {
		t.Error("expected google_enabled=true when client ID is set")
	}
	if resp.DevLoginEnabled {
		t.Error("expected dev_login_enabled=false by default")
	}
}

func TestGoogleOAuthConfig(t *testing.T) {
	// No client ID → nil
	cfg := auth.GoogleOAuthConfig("", "secret", "http://localhost")
	if cfg != nil {
		t.Error("expected nil config when client ID is empty")
	}

	// With client ID → non-nil
	cfg = auth.GoogleOAuthConfig("client-id", "secret", "http://localhost:5173")
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.ClientID != "client-id" {
		t.Errorf("expected client-id, got %s", cfg.ClientID)
	}
	if cfg.RedirectURL != "http://localhost:5173/api/auth/google/callback" {
		t.Errorf("unexpected redirect URL: %s", cfg.RedirectURL)
	}
}

func TestHandleGoogleLogin_NotConfigured(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	// No Google credentials → GoogleOAuthConfig returns nil
	router := testutil.NewTestRouter(pool)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/auth/google", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when Google OAuth not configured, got %d", rec.Code)
	}
}

func TestHandleGoogleLogin_Redirect(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := api.NewRouter(api.Deps{
		Queries: db.New(pool),
		// DB required since Pass 1 wired userpair.NewHandler in api.go.
		DB:                 pool,
		JWTSecret:          testutil.TestJWTSecret,
		GoogleClientID:     "test-client-id",
		GoogleClientSecret: "test-client-secret",
		BaseURL:            "http://localhost:5173",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/auth/google", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 redirect, got %d", rec.Code)
	}

	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("expected Location header")
	}
	// Should redirect to Google
	if !strings.Contains(loc, "accounts.google.com") {
		t.Errorf("expected redirect to Google, got: %s", loc)
	}

	// Should set state cookie
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "__oauth_state" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected __oauth_state cookie to be set")
	}
}

func TestHandleGoogleLogin_WithInviteToken(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := api.NewRouter(api.Deps{
		Queries: db.New(pool),
		// DB required since Pass 1 wired userpair.NewHandler in api.go.
		DB:                 pool,
		JWTSecret:          testutil.TestJWTSecret,
		GoogleClientID:     "test-client-id",
		GoogleClientSecret: "test-client-secret",
		BaseURL:            "http://localhost:5173",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/auth/google?invite=abc123", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d", rec.Code)
	}

	// State cookie should contain the invite token suffix
	cookies := rec.Result().Cookies()
	for _, c := range cookies {
		if c.Name == "__oauth_state" {
			if !strings.Contains(c.Value, ":abc123") {
				t.Errorf("expected state to contain :abc123, got: %s", c.Value)
			}
		}
	}
}
