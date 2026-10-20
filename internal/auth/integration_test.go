package auth_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationGetMe(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "bob@test.com", "pass123")

	// Authenticated /me request
	rec := testutil.DoRequest(t, router, "GET", "/api/auth/me", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var meResp struct {
		Data struct {
			ID       int64  `json:"id"`
			Email    string `json:"email"`
			Name     string `json:"name"`
			TenantID int64  `json:"tenant_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &meResp)
	if meResp.Data.Email != "bob@test.com" {
		t.Errorf("expected bob@test.com, got %q", meResp.Data.Email)
	}

	// Unauthenticated request
	rec = testutil.DoRequest(t, router, "GET", "/api/auth/me", nil, 0, 0)
	testutil.AssertStatus(t, rec, http.StatusUnauthorized)
}

// TestIntegrationDeletedUserJWTReturns401 verifies that a valid JWT for a
// deleted user returns 401, not 200 with empty data.
func TestIntegrationDeletedUserJWTReturns401(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "GhostCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "ghost@test.com", "pass123")

	// Verify user can access /me
	rec := testutil.DoRequest(t, router, "GET", "/api/auth/me", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Delete the user directly from the DB
	_, err := pool.Exec(testutil.Ctx(), "DELETE FROM users WHERE id = $1", user.ID)
	if err != nil {
		t.Fatalf("failed to delete user: %v", err)
	}

	// Same JWT should now return 401, NOT 200 with empty data
	rec = testutil.DoRequest(t, router, "GET", "/api/auth/me", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusUnauthorized)
}

// TestIntegrationRemoveMember covers per-org removal: DELETE
// /api/members/{id} deletes the membership (not the account), and an owner
// cannot remove themselves.
func TestIntegrationRemoveMember(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "RemoveCorp")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@remove.test", "pw", "owner")
	target := testutil.SeedUserWithRole(t, q, tenant.ID, "target@remove.test", "pw", "project_manager")

	// Owner removes the PM → 204, membership gone, but the identity survives.
	rec := testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/members/%d", target.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	if _, err := q.GetMembership(testutil.Ctx(), db.GetMembershipParams{UserID: target.ID, TenantID: tenant.ID}); err == nil {
		t.Fatal("expected membership to be deleted")
	}
	if _, err := q.GetUser(testutil.Ctx(), target.ID); err != nil {
		t.Fatalf("identity must survive per-org removal: %v", err)
	}

	// Owner removing self → 400 "cannot remove yourself". Self-removal is
	// checked before the last-admin guard.
	rec = testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/members/%d", owner.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

// TestIntegrationAdminCannotPromoteToOwner pins that an admin cannot grant or
// change owner via `PUT /api/members/{id}/role` — the same role hierarchy as
// HandleRemoveMember. Without it an admin could launder through a colluder to
// reach owner-only endpoints such as `DELETE /api/tenants/current`.
//
// The test asserts:
//  1. Admin → promote member to "owner" → 403 (FORBIDDEN).
//  2. Admin → demote existing owner → 403.
//  3. Owner → promote member to "owner" → 200 (allowed; only owners can grant).
//  4. Admin → promote member to "admin" → 200 (no escalation across the hierarchy).
func TestIntegrationAdminCannotPromoteToOwner(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "EscCorp")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@esc.test", "pw", "owner")
	admin := testutil.SeedUserWithRole(t, q, tenant.ID, "admin@esc.test", "pw", "admin")
	member := testutil.SeedUserWithRole(t, q, tenant.ID, "member@esc.test", "pw", "project_manager")
	owner2 := testutil.SeedUserWithRole(t, q, tenant.ID, "owner2@esc.test", "pw", "owner")

	// (1) Admin promotes a member → "owner" must be rejected.
	rec := testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/members/%d/role", member.ID),
		map[string]string{"role": "owner"},
		admin.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// (2) Admin demotes an existing owner → must be rejected (only owners can
	// touch owner rows). owner2 exists alongside owner so the last-admin
	// guard wouldn't kick in even if this WERE allowed — proves the failure
	// path is hierarchy, not last-admin.
	rec = testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/members/%d/role", owner2.ID),
		map[string]string{"role": "admin"},
		admin.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// (3) Owner promotes a member → "owner" must succeed.
	rec = testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/members/%d/role", member.ID),
		map[string]string{"role": "owner"},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Reset member to "project_manager" for the next case.
	rec = testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/members/%d/role", member.ID),
		map[string]string{"role": "project_manager"},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// (4) Admin → promote member to "admin" — within hierarchy, must succeed.
	rec = testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/members/%d/role", member.ID),
		map[string]string{"role": "admin"},
		admin.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// TestIntegrationDevSetRole covers the dev-only role switcher: switching mints a
// token carrying the new role, and an unknown role is rejected. (The test
// router enables dev auto-login, so the endpoint is live; in prod it 404s.)
func TestIntegrationDevSetRole(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DevRoleCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "u@test.com", "pass")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/auth/dev-role",
		map[string]string{"role": "linguist"}, user.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Token string `json:"token"`
		Role  string `json:"role"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Role != "linguist" {
		t.Fatalf("expected role linguist, got %q", resp.Role)
	}
	if resp.Token == "" {
		t.Fatal("expected a fresh token")
	}

	// Unknown role is rejected.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/auth/dev-role",
		map[string]string{"role": "wizard"}, user.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}
