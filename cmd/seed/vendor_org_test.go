package main

import (
	"context"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/seedid"
	"openloc/internal/testutil"
)

// The seed vendor org is a vendor of the seed organization with an admin, a
// project manager and a linguist and no owner; none of its people are members
// of the client, even after they sign in (the seed org's open registration is
// on its own domain only). A second run changes nothing.
func TestSeedVendorOrg(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	q := db.New(pool)

	healLanguages(ctx, pool)
	clientID, _ := seedCoreData(ctx, q)
	healTenantFields(ctx, pool, clientID)

	first := seedVendorOrg(ctx, q, clientID)
	if first == 0 {
		t.Fatal("seedVendorOrg returned 0")
	}
	if again := seedVendorOrg(ctx, q, clientID); again != first {
		t.Fatalf("second run returned org %d, want the same org %d", again, first)
	}

	vendor, err := q.GetTenant(ctx, first)
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if vendor.Kind != "vendor" || vendor.ClientTenantID == nil || *vendor.ClientTenantID != clientID {
		t.Errorf("vendor org kind=%q client=%v, want vendor of %d", vendor.Kind, vendor.ClientTenantID, clientID)
	}
	if vendor.Name != seedid.VendorOrgName || vendor.Domain != nil || vendor.Status != "approved" {
		t.Errorf("vendor org name=%q domain=%v status=%q", vendor.Name, vendor.Domain, vendor.Status)
	}

	members, err := q.ListMembersByTenant(ctx, db.ListMembersByTenantParams{TenantID: first})
	if err != nil {
		t.Fatalf("ListMembersByTenant: %v", err)
	}
	got := map[string]string{}
	for _, m := range members {
		got[m.Email] = m.Role
	}
	want := map[string]string{
		seedid.VendorAdminEmail:        "admin",
		seedid.VendorPMEmail:           "project_manager",
		seedid.VendorLinguistEmail:     "linguist",
		seedid.VendorIdleAdminEmail:    "admin",
		seedid.VendorIdleLinguistEmail: "linguist",
		seedid.VendorDueLinguistEmail:  "linguist",
	}
	if len(got) != len(want) {
		t.Errorf("vendor org members = %v, want %v", got, want)
	}
	// Domain auto-join is what dev login and Google run at sign-in.
	if _, err := q.GetTenantByDomainOpenReg(ctx, ptr.To(seedid.VendorOrgEmailDomain)); err == nil {
		t.Errorf("an org auto-joins %s, so the vendor org's people would join it at sign-in", seedid.VendorOrgEmailDomain)
	}
	for email, role := range want {
		if got[email] != role {
			t.Errorf("%s role = %q, want %q", email, got[email], role)
		}
		user, err := q.GetUserByEmail(ctx, email)
		if err != nil {
			t.Fatalf("GetUserByEmail(%s): %v", email, err)
		}
		// The idle people show the inactivity rules: two disabled, one due a
		// warning at the next sweep; everyone else is in use.
		m, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: user.ID, TenantID: first})
		if err != nil {
			t.Fatalf("GetMembership(%s): %v", email, err)
		}
		wantDisabled := email == seedid.VendorIdleAdminEmail || email == seedid.VendorIdleLinguistEmail
		unused := time.Since(m.LastActiveAt.Time)
		if m.DisabledAt.Valid != wantDisabled || m.InactivityWarnedAt.Valid != wantDisabled {
			t.Errorf("%s disabled=%v warned=%v, want %v", email, m.DisabledAt.Valid, m.InactivityWarnedAt.Valid, wantDisabled)
		}
		if email == seedid.VendorDueLinguistEmail && (unused < 84*24*time.Hour || unused > 86*24*time.Hour) {
			t.Errorf("%s unused for %v, want about 85 days", email, unused)
		}
		if _, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: user.ID, TenantID: clientID}); err == nil {
			t.Errorf("%s is a member of the client org", email)
		}
	}
}
