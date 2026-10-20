package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/auth"
	"openloc/internal/testutil"
)

// Selecting an org mints a token for it, so the membership check is the only
// thing between an identity and every other org's data. A non-member gets the
// same 404 as a missing org, no token, and no change to its last org; the
// member's own org is the positive control on the same route.
func TestIntegrationSelectOrg_OnlyForMembers(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	home := testutil.SeedTenant(t, q, "Home Org")
	user := testutil.SeedUserWithRole(t, q, home.ID, "member@test.com", "pass", "project_manager")
	foreign := testutil.SeedTenant(t, q, "Foreign Org")
	testutil.SeedUser(t, q, foreign.ID, "owner@foreign.test", "pass")
	identity := testutil.IdentityJWT(user.ID)

	t.Run("foreign org is 404 with no token", func(t *testing.T) {
		rec := testutil.DoRequestWithRawToken(t, router, http.MethodPost,
			fmt.Sprintf("/api/auth/orgs/%d/select", foreign.ID), nil, identity)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		if c := rec.Header().Get("Set-Cookie"); c != "" {
			t.Errorf("a token cookie was set for a foreign org: %q", c)
		}
		var body map[string]any
		testutil.DecodeJSON(t, rec, &body)
		if _, ok := body["token"]; ok {
			t.Errorf("response carries a token for a foreign org: %v", body)
		}
		u, err := q.GetUser(ctx, user.ID)
		if err != nil {
			t.Fatalf("GetUser: %v", err)
		}
		if u.LastTenantID != nil && *u.LastTenantID == foreign.ID {
			t.Errorf("last_tenant_id moved to the foreign org")
		}
	})

	t.Run("own org mints a token for it", func(t *testing.T) {
		rec := testutil.DoRequestWithRawToken(t, router, http.MethodPost,
			fmt.Sprintf("/api/auth/orgs/%d/select", home.ID), nil, identity)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var body struct {
			Token string `json:"token"`
		}
		testutil.DecodeJSON(t, rec, &body)
		claims, err := auth.ValidateToken(body.Token, testutil.TestJWTSecret)
		if err != nil {
			t.Fatalf("minted token does not validate: %v", err)
		}
		if claims.TenantID != home.ID || claims.Role != "project_manager" {
			t.Errorf("token for tenant %d role %q, want tenant %d role project_manager", claims.TenantID, claims.Role, home.ID)
		}
	})
}
