package publicapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/publicapi"
	"openloc/internal/testutil"
)

// TestIntegrationV1_VendorOrgInviteListed: the invite list tells a vendor-org
// invite from an admin invite into the organization, and names the vendor.
func TestIntegrationV1_VendorOrgInviteListed(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "VendorInvites")

	vendorInv, err := q.CreateInvite(context.Background(), db.CreateInviteParams{
		TenantID: f.tenant.ID, Email: "boss@vendor.example.com", Role: auth.RoleAdmin, InvitedBy: f.users["owner"].ID,
		Token: "tok-v1-vendor", ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		Kind: auth.InviteKindVendorOrg, VendorOrgName: ptr.To("Vendor Co"),
	})
	if err != nil {
		t.Fatalf("seed vendor-org invite: %v", err)
	}
	rec := f.client.do(http.MethodPost, "/api/v1/invites", map[string]any{"email": "colleague@example.com", "role": "admin"}, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusCreated)
	member := testutil.DecodeData[publicapi.Invite](t, rec)
	if member.Kind != "member" || member.VendorOrgName != nil {
		t.Fatalf("member invite = %+v", member)
	}

	rec = f.client.do(http.MethodGet, "/api/v1/invites", nil, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusOK)
	byID := map[int64]publicapi.Invite{}
	for _, i := range testutil.DecodeData[[]publicapi.Invite](t, rec) {
		byID[i.ID] = i
	}
	if got := byID[vendorInv.ID]; got.Kind != "vendor_org" || ptr.Deref(got.VendorOrgName) != "Vendor Co" || got.Role != "admin" {
		t.Errorf("vendor-org invite = %+v", got)
	}
	if got := byID[member.ID]; got.Kind != "member" || got.VendorOrgName != nil {
		t.Errorf("member invite = %+v", got)
	}
}
