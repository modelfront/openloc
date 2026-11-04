package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
)

// TestIntegrationMCPVendorOrgInvites: the invite tool can't create a vendor
// org, which only the app's form does; the list and revoke tools show a
// vendor-org invite for what it is.
func TestIntegrationMCPVendorOrgInvites(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	// vendor_org_name is not a parameter: the call is refused and stores nothing.
	res, err := s.CallTool(ctx, &sdk.CallToolParams{Name: "openloc_invite_member", Arguments: map[string]any{
		"email": "boss@vendor.test", "role": "admin", "vendor_org_name": "Vendor Co",
	}})
	if err == nil && !res.IsError {
		t.Fatalf("invite with vendor_org_name was accepted: %+v", res)
	}
	if _, err := f.q.GetPendingInviteByEmailAndTenant(ctx, db.GetPendingInviteByEmailAndTenantParams{
		Email: "boss@vendor.test", TenantID: f.tenantA.ID,
	}); err == nil {
		t.Fatal("a refused vendor invite was stored")
	}

	// An admin invite through the tool is a member invite.
	var inv struct {
		ID   int64  `json:"id"`
		Role string `json:"role"`
		Kind string `json:"kind"`
	}
	if isErr, text := confirmedCall(t, s, "openloc_invite_member", map[string]any{"email": "colleague@test.com", "role": "admin"}, &inv); isErr {
		t.Fatalf("invite admin: %s", text)
	}
	if inv.Role != "admin" || inv.Kind != "member" {
		t.Fatalf("tool invite = %+v", inv)
	}

	vendorInv, err := f.q.CreateInvite(ctx, db.CreateInviteParams{
		TenantID: f.tenantA.ID, Email: "boss@vendor.test", Role: auth.RoleAdmin, InvitedBy: f.ownerA.ID, Token: "tok-vendor-mcp",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		Kind:      auth.InviteKindVendorOrg, VendorOrgName: ptr.To("Vendor Co"),
	})
	if err != nil {
		t.Fatalf("seed vendor-org invite: %v", err)
	}

	var list struct {
		Invites []struct {
			ID            int64   `json:"id"`
			Kind          string  `json:"kind"`
			VendorOrgName *string `json:"vendor_org_name"`
		} `json:"invites"`
	}
	if isErr, text := callTool(t, s, "openloc_list_invites", map[string]any{}, &list); isErr {
		t.Fatalf("list_invites: %s", text)
	}
	found := false
	for _, i := range list.Invites {
		if i.ID == vendorInv.ID {
			found = i.Kind == "vendor_org" && ptr.Deref(i.VendorOrgName) == "Vendor Co"
		}
	}
	if !found {
		t.Fatalf("list_invites misses the vendor-org fields: %+v", list.Invites)
	}

	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_revoke_invite", map[string]any{"invite_id": vendorInv.ID}, &g); isErr {
		t.Fatalf("revoke preview: %s", text)
	}
	if g.Preview == nil || !strings.Contains(g.Preview.Summary, "set up the vendor Vendor Co") {
		t.Errorf("revoke preview doesn't name the vendor: %+v", g.Preview)
	}
}
