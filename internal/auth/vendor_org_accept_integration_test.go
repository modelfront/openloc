package auth_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// seedVendorOrgInvite stores a pending vendor-org invite in client.
func seedVendorOrgInvite(t *testing.T, q db.Querier, clientID, invitedBy int64, email, vendorName string) db.Invite {
	t.Helper()
	token, err := testutil.RandomToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	inv, err := q.CreateInvite(context.Background(), db.CreateInviteParams{
		TenantID: clientID, Email: email, Role: auth.RoleAdmin, InvitedBy: invitedBy, Token: token,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(7 * 24 * time.Hour), Valid: true},
		Kind:      auth.InviteKindVendorOrg, VendorOrgName: ptr.To(vendorName),
	})
	if err != nil {
		t.Fatalf("seed vendor-org invite: %v", err)
	}
	return inv
}

// vendorOrgsOf returns the vendor orgs of client.
func vendorOrgsOf(t *testing.T, pool *pgxpool.Pool, clientID int64) []db.Tenant {
	t.Helper()
	rows, err := pool.Query(testutil.Ctx(),
		"SELECT id, name, kind, status, client_tenant_id FROM tenants WHERE client_tenant_id = $1 ORDER BY id", clientID)
	if err != nil {
		t.Fatalf("list vendor orgs: %v", err)
	}
	defer rows.Close()
	var out []db.Tenant
	for rows.Next() {
		var tn db.Tenant
		if err := rows.Scan(&tn.ID, &tn.Name, &tn.Kind, &tn.Status, &tn.ClientTenantID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, tn)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// assertVendorOrgAccepted checks the outcome of accepting inv as user: one
// approved vendor org of the client, named after the invite, with the user as
// its admin; no membership in the client; the invite marked accepted.
func assertVendorOrgAccepted(t *testing.T, pool *pgxpool.Pool, q db.Querier, inv db.Invite, userID int64) db.Tenant {
	t.Helper()
	ctx := testutil.Ctx()
	orgs := vendorOrgsOf(t, pool, inv.TenantID)
	if len(orgs) != 1 {
		t.Fatalf("vendor orgs of the client = %+v, want exactly one", orgs)
	}
	vendor := orgs[0]
	if vendor.Kind != auth.TenantKindVendor || vendor.Status != "approved" || vendor.Name != *inv.VendorOrgName ||
		vendor.ClientTenantID == nil || *vendor.ClientTenantID != inv.TenantID {
		t.Fatalf("vendor org = %+v", vendor)
	}
	m, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: userID, TenantID: vendor.ID})
	if err != nil || m.Role != auth.RoleAdmin {
		t.Fatalf("membership in the vendor org = %+v, %v; want admin", m, err)
	}
	// The key isolation rule: the invite lives in the client, but accepting it
	// never makes the invitee a member of the client.
	if m, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: userID, TenantID: inv.TenantID}); err == nil {
		t.Fatalf("vendor-org invite made the invitee a %s of the CLIENT org", m.Role)
	}
	got, err := q.GetInviteByToken(ctx, inv.Token)
	if err != nil || !got.AcceptedAt.Valid {
		t.Fatalf("invite not marked accepted: %+v, %v", got, err)
	}
	return vendor
}

// TestVendorOrgAccept_TokenLink: signing in through the emailed link creates
// the vendor org, and signing in again creates no second one.
func TestVendorOrgAccept_TokenLink(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	h := auth.NewHandler(q, pool, testutil.TestJWTSecret, "http://localhost:5173", true, nil, nil)
	client := testutil.SeedTenant(t, q, "Client Corp")
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pass")
	inv := seedVendorOrgInvite(t, q, client.ID, owner.ID, "boss@vendor.test", "Vendor Co")

	user := testutil.SeedIdentity(t, q, "boss@vendor.test")
	h.MaterializeMembershipsForTest(testutil.Ctx(), user.ID, "boss@vendor.test", inv.Token)
	assertVendorOrgAccepted(t, pool, q, inv, user.ID)

	h.MaterializeMembershipsForTest(testutil.Ctx(), user.ID, "boss@vendor.test", inv.Token)
	assertVendorOrgAccepted(t, pool, q, inv, user.ID)
}

// TestVendorOrgAccept_Concurrent: two sign-ins racing on one invite create one
// vendor org.
func TestVendorOrgAccept_Concurrent(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	h := auth.NewHandler(q, pool, testutil.TestJWTSecret, "http://localhost:5173", true, nil, nil)
	client := testutil.SeedTenant(t, q, "Client Corp")
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pass")
	inv := seedVendorOrgInvite(t, q, client.ID, owner.ID, "boss@vendor.test", "Vendor Co")
	user := testutil.SeedIdentity(t, q, "boss@vendor.test")

	const racers = 4
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h.MaterializeMembershipsForTest(context.Background(), user.ID, "boss@vendor.test", inv.Token)
		}()
	}
	close(start)
	wg.Wait()
	assertVendorOrgAccepted(t, pool, q, inv, user.ID)
}

// TestVendorOrgAccept_EmailAtSignIn: an existing account picks up a pending
// vendor-org invite for its email at password sign-in, without the link, and
// the new org is in its org list.
func TestVendorOrgAccept_EmailAtSignIn(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	client := testutil.SeedTenant(t, q, "Client Corp")
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pass")
	other := testutil.SeedTenant(t, q, "Other Corp")
	user := testutil.SeedUserWithRole(t, q, other.ID, "boss@vendor.test", "their-pw-123", "linguist")
	inv := seedVendorOrgInvite(t, q, client.ID, owner.ID, "boss@vendor.test", "Vendor Co")

	status, body := do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "boss@vendor.test", "password": "their-pw-123",
	})
	if status != http.StatusOK || body["needs_org_selection"] != true {
		t.Fatalf("login = %d %v; want the org picker (two orgs)", status, body)
	}
	vendor := assertVendorOrgAccepted(t, pool, q, inv, user.ID)

	rec := testutil.DoRequest(t, router, "GET", "/api/auth/orgs", nil, user.ID, other.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	orgs := testutil.DecodeData[[]db.ListMembershipsWithTenantRow](t, rec)
	found := false
	for _, o := range orgs {
		if o.TenantID == client.ID {
			t.Fatalf("org list carries the client org: %+v", orgs)
		}
		if o.TenantID == vendor.ID {
			found = o.Role == auth.RoleAdmin && o.TenantKind == auth.TenantKindVendor && ptr.Deref(o.ClientOrgName) == "Client Corp"
		}
	}
	if !found || len(orgs) != 2 {
		t.Fatalf("org list = %+v; want the other org and the vendor org as admin", orgs)
	}
}

// TestVendorOrgAccept_PasswordAccept: a new person accepts with a password and
// lands straight in the vendor org, their only org. A taken slug gets a
// suffix.
func TestVendorOrgAccept_PasswordAccept(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	testutil.SeedTenant(t, q, "Vendor Co") // takes the slug vendor-co
	client := testutil.SeedTenant(t, q, "Client Corp")
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pass")
	inv := seedVendorOrgInvite(t, q, client.ID, owner.ID, "fresh@vendor.test", "Vendor Co")

	status, body := do(t, router, "POST", "/api/auth/accept-invite", "", map[string]string{
		"token": inv.Token, "password": "brand-new-pw-1", "name": "Fresh Boss",
	})
	if status != http.StatusOK || body["needs_org_selection"] != false {
		t.Fatalf("accept = %d %v; want a direct landing", status, body)
	}
	user, err := q.GetUserByEmail(testutil.Ctx(), "fresh@vendor.test")
	if err != nil {
		t.Fatalf("user not created: %v", err)
	}
	vendor := assertVendorOrgAccepted(t, pool, q, inv, user.ID)
	if got, err := q.GetTenant(testutil.Ctx(), vendor.ID); err != nil || got.Slug != "vendor-co-2" {
		t.Errorf("vendor org slug = %q, %v; want vendor-co-2", got.Slug, err)
	}
	if u, ok := body["user"].(map[string]any); !ok || u["tenant_id"] != float64(vendor.ID) || u["role"] != auth.RoleAdmin {
		t.Fatalf("logged into %v; want admin of vendor org %d", body["user"], vendor.ID)
	}
}

// TestVendorOrgAccept_JoinableOrg: a pending vendor-org invite entitles an
// unknown email to an account, by email and by link.
func TestVendorOrgAccept_JoinableOrg(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	h := newProvisionHandler(q)
	client := testutil.SeedTenant(t, q, "Client Corp")
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pass")
	inv := seedVendorOrgInvite(t, q, client.ID, owner.ID, "boss@vendor.test", "Vendor Co")

	if !h.HasJoinableOrgForTest(testutil.Ctx(), "boss@vendor.test", "") {
		t.Error("a pending vendor-org invite does not count as a joinable org by email")
	}
	if !h.HasJoinableOrgForTest(testutil.Ctx(), "boss@vendor.test", inv.Token) {
		t.Error("a pending vendor-org invite does not count as a joinable org by link")
	}
}
