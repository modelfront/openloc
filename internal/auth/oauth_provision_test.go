package auth_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// These tests exercise the membership-materialization seam directly
// (materializeMemberships / hasJoinableOrg, via the export_test.go shims)
// rather than the full Google callback, which would need a live Google token
// exchange. They cover the org-join rules:
//
//   - Self-service domain sign-ups get a project_manager membership.
//   - Someone on the Vendors list (a pending linguist invite) gets a linguist
//     membership — even after the invite would have "expired". Only revocation
//     (deleting the invite) stops them.

func newProvisionHandler(q db.Querier) *auth.Handler {
	return auth.NewHandler(q, nil, testutil.TestJWTSecret, "http://localhost:5173", false, nil, nil)
}

func pastTimestamp() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true}
}

func futureTimestamp() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true}
}

func TestDomainAutoJoin_BecomesProjectManager(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "Acme")
	if _, err := pool.Exec(testutil.Ctx(),
		"UPDATE tenants SET domain = $1, open_registration = true WHERE id = $2",
		"acme.test", tenant.ID); err != nil {
		t.Fatalf("enable open registration: %v", err)
	}

	user := testutil.SeedIdentity(t, q, "newbie@acme.test")
	h := newProvisionHandler(q)
	h.MaterializeMembershipsForTest(testutil.Ctx(), user.ID, "newbie@acme.test", "")

	m, err := q.GetMembership(testutil.Ctx(), db.GetMembershipParams{UserID: user.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("expected domain auto-join membership, got: %v", err)
	}
	if m.Role != auth.RoleProjectManager {
		t.Fatalf("expected role %q, got %q", auth.RoleProjectManager, m.Role)
	}
}

func TestDomainAutoJoin_UnknownDomainRejected(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	h := newProvisionHandler(q)
	if h.HasJoinableOrgForTest(testutil.Ctx(), "x@nowhere.test", "") {
		t.Fatal("expected no joinable org for a domain with no open-registration tenant")
	}
}

// TestInviteMaterialize_MatchesExpiredVendorInvite is the regression guard for
// the "vendor added to the list can't log in via SSO" bug: an invite whose
// expires_at is in the past must still be accepted into a linguist membership.
func TestInviteMaterialize_MatchesExpiredVendorInvite(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "Acme")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@acme.test", "pw", "owner")

	if _, err := q.CreateInvite(testutil.Ctx(), db.CreateInviteParams{
		TenantID:  tenant.ID,
		Email:     "vendor@gmail.com",
		Role:      auth.RoleLinguist,
		InvitedBy: owner.ID,
		Token:     "tok-expired",
		ExpiresAt: pastTimestamp(),
	}); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	user := testutil.SeedIdentity(t, q, "vendor@gmail.com")
	h := newProvisionHandler(q)
	h.MaterializeMembershipsForTest(testutil.Ctx(), user.ID, "vendor@gmail.com", "")

	m, err := q.GetMembership(testutil.Ctx(), db.GetMembershipParams{UserID: user.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("expected expired vendor invite to still materialize a membership, got: %v", err)
	}
	if m.Role != auth.RoleLinguist {
		t.Fatalf("expected role %q, got %q", auth.RoleLinguist, m.Role)
	}
}

// TestInviteMaterialize_ExistingAccountJoinsSecondOrg is the core multi-org
// guarantee: a person who already has an account + membership in org
// A, invited to org B, joins B via a NEW membership on the same identity — no
// duplicate user row, no email-unique 409.
func TestInviteMaterialize_ExistingAccountJoinsSecondOrg(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	orgA := testutil.SeedTenant(t, q, "OrgA")
	user := testutil.SeedUserWithRole(t, q, orgA.ID, "person@corp.test", "pw", "owner")

	orgB := testutil.SeedTenant(t, q, "OrgB")
	ownerB := testutil.SeedUserWithRole(t, q, orgB.ID, "ownerb@corp.test", "pw", "owner")
	if _, err := q.CreateInvite(testutil.Ctx(), db.CreateInviteParams{
		TenantID:  orgB.ID,
		Email:     "person@corp.test",
		Role:      auth.RoleLinguist,
		InvitedBy: ownerB.ID,
		Token:     "tok-b",
		ExpiresAt: futureTimestamp(),
	}); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	h := newProvisionHandler(q)
	h.MaterializeMembershipsForTest(testutil.Ctx(), user.ID, "person@corp.test", "")

	// New membership in B with the invite's role; membership in A untouched.
	mb, err := q.GetMembership(testutil.Ctx(), db.GetMembershipParams{UserID: user.ID, TenantID: orgB.ID})
	if err != nil {
		t.Fatalf("expected membership in org B: %v", err)
	}
	if mb.Role != auth.RoleLinguist {
		t.Fatalf("expected linguist in B, got %q", mb.Role)
	}
	if _, err := q.GetMembership(testutil.Ctx(), db.GetMembershipParams{UserID: user.ID, TenantID: orgA.ID}); err != nil {
		t.Fatalf("membership in A must remain: %v", err)
	}
	// Still exactly one identity for this email — no duplicate row / 409.
	if u, err := q.GetUserByEmail(testutil.Ctx(), "person@corp.test"); err != nil || u.ID != user.ID {
		t.Fatalf("expected the same single identity, got id=%d err=%v", u.ID, err)
	}
}

func TestInviteMaterialize_RevokedInviteRejected(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "Acme")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@acme.test", "pw", "owner")

	inv, err := q.CreateInvite(testutil.Ctx(), db.CreateInviteParams{
		TenantID:  tenant.ID,
		Email:     "vendor@gmail.com",
		Role:      auth.RoleLinguist,
		InvitedBy: owner.ID,
		Token:     "tok-live",
		ExpiresAt: futureTimestamp(),
	})
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	// Revoke it.
	if err := q.DeleteInvite(testutil.Ctx(), db.DeleteInviteParams{ID: inv.ID, TenantID: tenant.ID}); err != nil {
		t.Fatalf("DeleteInvite: %v", err)
	}

	h := newProvisionHandler(q)
	if h.HasJoinableOrgForTest(testutil.Ctx(), "vendor@gmail.com", "") {
		t.Fatal("expected no joinable org after the invite was revoked")
	}

	// And materializing grants no membership.
	user := testutil.SeedIdentity(t, q, "vendor@gmail.com")
	h.MaterializeMembershipsForTest(testutil.Ctx(), user.ID, "vendor@gmail.com", "")
	if _, err := q.GetMembership(testutil.Ctx(), db.GetMembershipParams{UserID: user.ID, TenantID: tenant.ID}); err == nil {
		t.Fatal("expected no membership after revoked invite")
	}
}
