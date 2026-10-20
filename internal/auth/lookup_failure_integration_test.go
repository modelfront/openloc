package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	"openloc/internal/testutil"
)

// When the database cannot answer, a signed-in request gets 503, never the 401
// that makes the app sign the user out; a user or key that really is gone
// still gets 401.
func TestIntegrationAuthDatabaseFailureIsNotUnauthorized(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenant := testutil.SeedTenant(t, q, "AuthOutage")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "outage@test.com", "pass", "owner")
	key := testutil.SeedAPIToken(t, q, tenant.ID, user.ID, "write", time.Hour)

	get := func(router http.Handler, path, bearer string) int {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	session := testutil.TestJWT(user.ID, tenant.ID)

	live := testutil.NewTestRouter(pool)
	if code := get(live, "/api/projects", session); code != http.StatusOK {
		t.Fatalf("session on a live database: %d", code)
	}
	if code := get(live, "/api/v1/projects", key); code != http.StatusOK {
		t.Fatalf("access key on a live database: %d", code)
	}
	if code := get(live, "/api/projects", testutil.TestJWT(user.ID+1000, tenant.ID)); code != http.StatusUnauthorized {
		t.Errorf("session of a missing user: %d, want 401", code)
	}

	down, err := pgxpool.NewWithConfig(context.Background(), pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	broken := testutil.NewTestRouter(down)
	down.Close()
	if code := get(broken, "/api/projects", session); code != http.StatusServiceUnavailable {
		t.Errorf("session while the database is down: %d, want 503", code)
	}
	if code := get(broken, "/api/auth/me", session); code != http.StatusServiceUnavailable {
		t.Errorf("identity route while the database is down: %d, want 503", code)
	}
	if code := get(broken, "/api/v1/projects", key); code != http.StatusServiceUnavailable {
		t.Errorf("access key while the database is down: %d, want 503", code)
	}

	// The assistant connectors' OAuth token on /mcp answers the same way.
	oauth := testutil.SeedOAuthToken(t, q, tenant.ID, user.ID, auth.CapabilityRead)
	if code := get(live, "/mcp", auth.OAuthAccessPrefix+"unknown"); code != http.StatusUnauthorized {
		t.Errorf("unknown OAuth token on a live database: %d, want 401", code)
	}
	if code := get(broken, "/mcp", oauth); code != http.StatusServiceUnavailable {
		t.Errorf("OAuth token while the database is down: %d, want 503", code)
	}
}
