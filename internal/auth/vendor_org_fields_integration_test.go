package auth_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// TestIntegrationVendorOrgFields: the profile, the org picker and the invite
// list and lookup say which kind an org or invite is, and a vendor org names
// its client.
func TestIntegrationVendorOrgFields(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	client := testutil.SeedTenant(t, q, "Client Corp")
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pass123")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Vendor Co")
	testutil.SeedMembership(t, q, owner.ID, vendor.ID, auth.RoleAdmin)

	me := func(tenantID int64) map[string]any {
		rec := testutil.DoRequest(t, router, "GET", "/api/auth/me", nil, owner.ID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		return testutil.DecodeData[map[string]any](t, rec)
	}
	if got := me(vendor.ID); got["tenant_kind"] != "vendor" || got["client_org_name"] != "Client Corp" {
		t.Errorf("vendor /me tenant_kind=%v client_org_name=%v", got["tenant_kind"], got["client_org_name"])
	}
	if got := me(client.ID); got["tenant_kind"] != "customer" || got["client_org_name"] != nil {
		t.Errorf("client /me tenant_kind=%v client_org_name=%v", got["tenant_kind"], got["client_org_name"])
	}

	rec := testutil.DoRequest(t, router, "GET", "/api/auth/orgs", nil, owner.ID, client.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	orgs := testutil.DecodeData[[]db.ListMembershipsWithTenantRow](t, rec)
	byID := map[int64]db.ListMembershipsWithTenantRow{}
	for _, o := range orgs {
		byID[o.TenantID] = o
	}
	if o := byID[vendor.ID]; o.TenantKind != "vendor" || ptr.Deref(o.ClientOrgName) != "Client Corp" {
		t.Errorf("vendor org row = %+v", o)
	}
	if o := byID[client.ID]; o.TenantKind != "customer" || o.ClientOrgName != nil {
		t.Errorf("client org row = %+v", o)
	}

	token, err := testutil.RandomToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := q.CreateInvite(ctx, db.CreateInviteParams{
		TenantID: client.ID, Email: "boss@newvendor.test", Role: auth.RoleAdmin, InvitedBy: owner.ID, Token: token,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		Kind:      auth.InviteKindVendorOrg, VendorOrgName: ptr.To("New Vendor"),
	}); err != nil {
		t.Fatalf("seed vendor-org invite: %v", err)
	}
	rec = testutil.DoRequest(t, router, "GET", "/api/invites", nil, owner.ID, client.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	invites := testutil.DecodeData[[]map[string]any](t, rec)
	if len(invites) != 1 || invites[0]["kind"] != "vendor_org" || invites[0]["vendor_org_name"] != "New Vendor" {
		t.Errorf("invite list = %v", invites)
	}

	rec = testutil.DoRequest(t, router, "GET", "/api/invites/lookup/"+token, nil, 0, 0)
	testutil.AssertStatus(t, rec, http.StatusOK)
	lookup := testutil.DecodeData[map[string]any](t, rec)
	if lookup["kind"] != "vendor_org" || lookup["vendor_org_name"] != "New Vendor" || lookup["org_name"] != "Client Corp" {
		t.Errorf("invite lookup = %v", lookup)
	}
}
