package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationLastAdminGuard pins the last-admin guard on both member
// mutations: PUT /api/members/{id}/role (demote) and DELETE /api/members/{id}
// (remove). A token keeps the role it was issued with, so the caller's claim
// says admin while the memberships table — what the guard counts — holds a
// single active admin: the target. Both calls answer 409 and leave the row
// alone; a deactivated admin does not count. Once a second active admin
// exists, the same calls go through.
func TestIntegrationLastAdminGuard(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "LastAdminCorp")
	last := testutil.SeedUserWithRole(t, q, tenant.ID, "last@lastadmin.test", "pw", "admin")
	// Membership project_manager, token claims admin: the claim passes the
	// route gate, the membership keeps the caller out of the admin count.
	caller := testutil.SeedUserWithRole(t, q, tenant.ID, "caller@lastadmin.test", "pw", "project_manager")
	inactive := testutil.SeedUserWithRole(t, q, tenant.ID, "inactive@lastadmin.test", "pw", "admin")
	if _, err := pool.Exec(testutil.Ctx(), "UPDATE users SET is_active = false WHERE id = $1", inactive.ID); err != nil {
		t.Fatalf("deactivate admin: %v", err)
	}

	demote := func(userID int64) *httptest.ResponseRecorder {
		return testutil.DoRequestWithRole(t, router, "PUT",
			fmt.Sprintf("/api/members/%d/role", userID), map[string]string{"role": "linguist"},
			caller.ID, tenant.ID, "admin")
	}
	remove := func(userID int64) *httptest.ResponseRecorder {
		return testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/members/%d", userID), nil, caller.ID, tenant.ID, "admin")
	}
	roleOf := func(userID int64) string {
		t.Helper()
		m, err := q.GetMembership(testutil.Ctx(), db.GetMembershipParams{UserID: userID, TenantID: tenant.ID})
		if err != nil {
			t.Fatalf("membership of user %d: %v", userID, err)
		}
		return m.Role
	}

	// One active admin: both mutations are refused and the row is untouched.
	testutil.AssertStatus(t, demote(last.ID), http.StatusConflict)
	if got := roleOf(last.ID); got != "admin" {
		t.Fatalf("refused demote changed the role to %q", got)
	}
	testutil.AssertStatus(t, remove(last.ID), http.StatusConflict)
	if got := roleOf(last.ID); got != "admin" {
		t.Fatalf("refused remove changed the membership, role now %q", got)
	}

	// A second active admin lifts the guard off the demote.
	second := testutil.SeedUserWithRole(t, q, tenant.ID, "second@lastadmin.test", "pw", "admin")
	testutil.AssertStatus(t, demote(last.ID), http.StatusOK)
	if got := roleOf(last.ID); got != "linguist" {
		t.Fatalf("demote with a second admin present left the role %q, want linguist", got)
	}

	// The demotion moved the count: `second` is now the last active admin.
	testutil.AssertStatus(t, remove(second.ID), http.StatusConflict)
	if got := roleOf(second.ID); got != "admin" {
		t.Fatalf("refused remove changed the membership, role now %q", got)
	}

	// And a third admin lifts the guard off the remove.
	testutil.SeedUserWithRole(t, q, tenant.ID, "third@lastadmin.test", "pw", "admin")
	testutil.AssertStatus(t, remove(second.ID), http.StatusNoContent)
	if _, err := q.GetMembership(testutil.Ctx(), db.GetMembershipParams{UserID: second.ID, TenantID: tenant.ID}); err == nil {
		t.Fatal("remove with a third admin present left the membership in place")
	}
}
