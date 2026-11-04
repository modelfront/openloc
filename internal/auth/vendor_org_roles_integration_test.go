package auth_test

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationVendorOrgRoleRules: a vendor org has admins, project
// managers and linguists, never an owner, and keeps at least one admin.
func TestIntegrationVendorOrgRoleRules(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Vendor Co")
	admin := testutil.SeedUserWithRole(t, q, vendor.ID, "admin@vendor.test", "pw", auth.RoleAdmin)
	pm := testutil.SeedUserWithRole(t, q, vendor.ID, "pm@vendor.test", "pw", auth.RoleProjectManager)
	linguist := testutil.SeedUserWithRole(t, q, vendor.ID, "ling@vendor.test", "pw", auth.RoleLinguist)

	setRole := func(callerID, userID int64, role string, want int) {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/members/%d/role", userID),
			map[string]string{"role": role}, callerID, vendor.ID, auth.RoleAdmin)
		testutil.AssertStatus(t, rec, want)
	}
	roleOf := func(userID int64) string {
		t.Helper()
		m, err := q.GetMembership(testutil.Ctx(), db.GetMembershipParams{UserID: userID, TenantID: vendor.ID})
		if err != nil {
			t.Fatalf("membership of user %d: %v", userID, err)
		}
		return m.Role
	}

	// The only admin can't be demoted or removed. The caller's token claims
	// admin, but the membership count is what the guard reads.
	setRole(pm.ID, admin.ID, auth.RoleLinguist, http.StatusConflict)
	rec := testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/members/%d", admin.ID), nil, pm.ID, vendor.ID, auth.RoleAdmin)
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if roleOf(admin.ID) != auth.RoleAdmin {
		t.Fatal("a refused change touched the last admin")
	}

	// Owner is refused in a vendor org, even for a caller whose token says owner.
	setRole(admin.ID, pm.ID, auth.RoleOwner, http.StatusBadRequest)
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/members/%d/role", pm.ID),
		map[string]string{"role": auth.RoleOwner}, admin.ID, vendor.ID, auth.RoleOwner)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if roleOf(pm.ID) != auth.RoleProjectManager {
		t.Fatalf("refused owner grant changed the role to %q", roleOf(pm.ID))
	}

	// Admin, project manager and linguist are all assignable.
	setRole(admin.ID, linguist.ID, auth.RoleProjectManager, http.StatusOK)
	setRole(admin.ID, pm.ID, auth.RoleAdmin, http.StatusOK)
	if roleOf(linguist.ID) != auth.RoleProjectManager || roleOf(pm.ID) != auth.RoleAdmin {
		t.Fatalf("roles = %q, %q", roleOf(linguist.ID), roleOf(pm.ID))
	}
	setRole(admin.ID, linguist.ID, auth.RoleLinguist, http.StatusOK)

	// With a second admin, the first may be demoted.
	setRole(pm.ID, admin.ID, auth.RoleProjectManager, http.StatusOK)
	if roleOf(admin.ID) != auth.RoleProjectManager {
		t.Fatalf("demote with a second admin left %q", roleOf(admin.ID))
	}

	// A customer org still assigns owner.
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pw")
	member := testutil.SeedUserWithRole(t, q, client.ID, "member@client.test", "pw", auth.RoleAdmin)
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/members/%d/role", member.ID),
		map[string]string{"role": auth.RoleOwner}, owner.ID, client.ID, auth.RoleOwner)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// TestIntegrationVendorOrgDevSetRole: the dev role switcher follows the same
// rule as member role changes: no owner in a vendor org.
func TestIntegrationVendorOrgDevSetRole(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Vendor Co")
	admin := testutil.SeedUserWithRole(t, q, vendor.ID, "admin@vendor.test", "pw", auth.RoleAdmin)

	switchTo := func(role string, want int) {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/auth/dev-role",
			map[string]string{"role": role}, admin.ID, vendor.ID, auth.RoleAdmin)
		testutil.AssertStatus(t, rec, want)
	}
	roleOf := func() string {
		t.Helper()
		m, err := q.GetMembership(testutil.Ctx(), db.GetMembershipParams{UserID: admin.ID, TenantID: vendor.ID})
		if err != nil {
			t.Fatalf("membership: %v", err)
		}
		return m.Role
	}

	switchTo(auth.RoleOwner, http.StatusBadRequest)
	if got := roleOf(); got != auth.RoleAdmin {
		t.Fatalf("refused switch to owner left the role %q", got)
	}
	switchTo(auth.RoleLinguist, http.StatusOK)
	switchTo(auth.RoleAdmin, http.StatusOK)
	if got := roleOf(); got != auth.RoleAdmin {
		t.Fatalf("role after switching back = %q", got)
	}

	// A customer org still switches to owner.
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pw")
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/auth/dev-role",
		map[string]string{"role": auth.RoleOwner}, owner.ID, client.ID, auth.RoleOwner)
	testutil.AssertStatus(t, rec, http.StatusOK)
}
