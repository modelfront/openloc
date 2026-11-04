package invite_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/api"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type inviteResp struct {
	Data struct {
		ID            int64   `json:"id"`
		Email         string  `json:"email"`
		Role          string  `json:"role"`
		Kind          string  `json:"kind"`
		VendorOrgName *string `json:"vendor_org_name"`
	} `json:"data"`
}

// TestIntegrationVendorOrgInviteCreate pins who may invite a vendor org and
// what a valid one looks like: a client owner or admin, a 1–120 character
// name, stored as an admin invite of kind vendor_org.
func TestIntegrationVendorOrgInviteCreate(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	client := testutil.SeedTenant(t, q, "ClientCorp")
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pass")
	admin := testutil.SeedUserWithRole(t, q, client.ID, "admin@client.test", "pass", "admin")
	pm := testutil.SeedUserWithRole(t, q, client.ID, "pm@client.test", "pass", "project_manager")
	linguist := testutil.SeedUserWithRole(t, q, client.ID, "ling@client.test", "pass", "linguist")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Old Vendor")
	vendorAdmin := testutil.SeedUserWithRole(t, q, vendor.ID, "admin@vendor.test", "pass", "admin")

	create := func(userID, tenantID int64, role string, body map[string]any, want int) inviteResp {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/invites", body, userID, tenantID, role)
		testutil.AssertStatus(t, rec, want)
		var resp inviteResp
		if want == http.StatusCreated {
			testutil.DecodeJSON(t, rec, &resp)
		}
		return resp
	}

	// An owner invites a vendor: stored as an admin invite of kind vendor_org,
	// with the trimmed name.
	got := create(owner.ID, client.ID, "owner", map[string]any{
		"email": "Boss@Vendor-A.test", "vendor_org_name": "  Vendor A  ",
	}, http.StatusCreated)
	if got.Data.Kind != "vendor_org" || got.Data.Role != "admin" || got.Data.VendorOrgName == nil || *got.Data.VendorOrgName != "Vendor A" {
		t.Fatalf("vendor invite = %+v", got.Data)
	}
	stored := getInvite(t, q, client.ID, got.Data.ID)
	if stored.Kind != "vendor_org" || stored.Role != "admin" || stored.VendorOrgName == nil || *stored.VendorOrgName != "Vendor A" || stored.Email != "boss@vendor-a.test" {
		t.Fatalf("stored vendor invite = %+v", stored)
	}

	// An admin may too; role "admin" spelled out is accepted.
	create(admin.ID, client.ID, "admin", map[string]any{
		"email": "boss@vendor-b.test", "role": "admin", "vendor_org_name": "Vendor B",
	}, http.StatusCreated)

	// 120 characters, counted as characters, not bytes.
	create(owner.ID, client.ID, "owner", map[string]any{
		"email": "boss@vendor-c.test", "vendor_org_name": strings.Repeat("é", 120),
	}, http.StatusCreated)

	// A project manager or linguist may not: it creates an org.
	create(pm.ID, client.ID, "project_manager", map[string]any{
		"email": "pm-try@vendor.test", "vendor_org_name": "PM Vendor",
	}, http.StatusForbidden)
	create(linguist.ID, client.ID, "linguist", map[string]any{
		"email": "ling-try@vendor.test", "vendor_org_name": "Ling Vendor",
	}, http.StatusForbidden)

	// A vendor org can't invite a vendor of its own.
	create(vendorAdmin.ID, vendor.ID, "admin", map[string]any{
		"email": "nested@vendor.test", "vendor_org_name": "Nested Vendor",
	}, http.StatusForbidden)

	// A bad name, or a role other than admin, is refused.
	for name, body := range map[string]map[string]any{
		"empty name":   {"email": "x1@vendor.test", "vendor_org_name": ""},
		"blank name":   {"email": "x2@vendor.test", "vendor_org_name": "   "},
		"long name":    {"email": "x3@vendor.test", "vendor_org_name": strings.Repeat("a", 121)},
		"linguist":     {"email": "x4@vendor.test", "vendor_org_name": "V", "role": "linguist"},
		"owner":        {"email": "x5@vendor.test", "vendor_org_name": "V", "role": "owner"},
		"missing mail": {"vendor_org_name": "V"},
		"line break":   {"email": "x6@vendor.test", "vendor_org_name": "Vendor\nBcc: x@evil.test"},
	} {
		t.Run(name, func(t *testing.T) {
			create(owner.ID, client.ID, "owner", body, http.StatusBadRequest)
		})
	}

	// The duplicate-pending and existing-member rules still apply.
	create(owner.ID, client.ID, "owner", map[string]any{
		"email": "boss@vendor-a.test", "vendor_org_name": "Vendor A again",
	}, http.StatusConflict)
	create(owner.ID, client.ID, "owner", map[string]any{
		"email": "pm@client.test", "vendor_org_name": "In-house Vendor",
	}, http.StatusConflict)

	// None of the refused calls stored an invite.
	var n int
	if err := pool.QueryRow(testutil.Ctx(), "SELECT count(*) FROM invites WHERE kind = 'vendor_org'").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("vendor-org invites stored = %d, want 3", n)
	}
}

// TestIntegrationVendorOrgMemberInviteRoles: a vendor org invites project
// managers and linguists only; its admins come from a role change.
func TestIntegrationVendorOrgMemberInviteRoles(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	client := testutil.SeedTenant(t, q, "ClientCorp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Vendor Co")
	vendorAdmin := testutil.SeedUserWithRole(t, q, vendor.ID, "admin@vendor.test", "pass", "admin")

	for role, want := range map[string]int{
		"project_manager": http.StatusCreated,
		"linguist":        http.StatusCreated,
		"admin":           http.StatusBadRequest,
		"owner":           http.StatusBadRequest,
	} {
		t.Run(role, func(t *testing.T) {
			rec := testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]any{
				"email": role + "@vendor.test", "role": role,
			}, vendorAdmin.ID, vendor.ID, "admin")
			testutil.AssertStatus(t, rec, want)
			if want == http.StatusCreated {
				var resp inviteResp
				testutil.DecodeJSON(t, rec, &resp)
				if resp.Data.Kind != "member" || resp.Data.Role != role {
					t.Fatalf("invite = %+v", resp.Data)
				}
			}
		})
	}
}

// TestIntegrationVendorOrgInviteLifecycle: the vendor-org invite email, its
// resend, list, lookup and revoke.
func TestIntegrationVendorOrgInviteLifecycle(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	fake := &fakeSender{}
	router := api.NewRouter(api.Deps{
		Queries:     db.New(pool),
		DB:          pool,
		JWTSecret:   testutil.TestJWTSecret,
		BaseURL:     "http://localhost:5173",
		EmailSender: fake,
	})
	client := testutil.SeedTenant(t, q, "ClientCorp")
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pass")
	pm := testutil.SeedUserWithRole(t, q, client.ID, "pm@client.test", "pass", "project_manager")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]any{
		"email": "boss@vendor.test", "vendor_org_name": "Vendor & Co",
	}, owner.ID, client.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created inviteResp
	testutil.DecodeJSON(t, rec, &created)

	wantEmail := func(step string) {
		t.Helper()
		if fake.lastTo != "boss@vendor.test" {
			t.Fatalf("%s: email went to %q", step, fake.lastTo)
		}
		for _, want := range []string{"ClientCorp", "Vendor &amp; Co", "set up", "its admin"} {
			if !strings.Contains(fake.lastBody, want) {
				t.Errorf("%s: email body misses %q: %s", step, want, fake.lastBody)
			}
		}
		if !strings.Contains(fake.lastSubject, "Vendor & Co") {
			t.Errorf("%s: subject %q misses the vendor name", step, fake.lastSubject)
		}
	}
	wantEmail("create")

	rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/invites/%d/resend", created.Data.ID), nil, owner.ID, client.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resent inviteResp
	testutil.DecodeJSON(t, rec, &resent)
	if resent.Data.Kind != "vendor_org" || resent.Data.VendorOrgName == nil || *resent.Data.VendorOrgName != "Vendor & Co" {
		t.Fatalf("resend = %+v", resent.Data)
	}
	wantEmail("resend")
	inv := getInvite(t, q, client.ID, created.Data.ID)
	if !strings.Contains(fake.lastBody, "/invite/"+inv.Token) {
		t.Fatal("resend email misses the new link")
	}

	// The lookup tells the accept page it sets up a vendor org.
	rec = testutil.DoRequest(t, router, "GET", "/api/invites/lookup/"+inv.Token, nil, owner.ID, client.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var lookup struct {
		Data struct {
			OrgName       string `json:"org_name"`
			Role          string `json:"role"`
			Kind          string `json:"kind"`
			VendorOrgName string `json:"vendor_org_name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &lookup)
	if lookup.Data.OrgName != "ClientCorp" || lookup.Data.Kind != "vendor_org" || lookup.Data.VendorOrgName != "Vendor & Co" || lookup.Data.Role != "admin" {
		t.Fatalf("lookup = %+v", lookup.Data)
	}

	// The owner's list shows it as a vendor-org invite; a project manager's
	// list leaves it out, and the PM can't resend or revoke it.
	list := func(userID int64, role string) []inviteRow {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "GET", "/api/invites", nil, userID, client.ID, role)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []inviteRow `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data
	}
	rows := list(owner.ID, "owner")
	if len(rows) != 1 || rows[0].Kind != "vendor_org" || rows[0].VendorOrgName == nil || *rows[0].VendorOrgName != "Vendor & Co" {
		t.Fatalf("owner list = %+v", rows)
	}
	if rows := list(pm.ID, "project_manager"); len(rows) != 0 {
		t.Fatalf("pm list = %+v", rows)
	}
	rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/invites/%d/resend", created.Data.ID), nil, pm.ID, client.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/invites/%d", created.Data.ID), nil, pm.ID, client.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// The owner revokes it; the link dies.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/invites/%d", created.Data.ID), nil, owner.ID, client.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequest(t, router, "GET", "/api/invites/lookup/"+inv.Token, nil, owner.ID, client.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

type inviteRow struct {
	ID            int64   `json:"id"`
	Kind          string  `json:"kind"`
	VendorOrgName *string `json:"vendor_org_name"`
}
