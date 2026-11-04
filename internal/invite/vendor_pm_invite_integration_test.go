package invite_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/auth"
	"openloc/internal/testutil"
)

// TestIntegrationVendorPMInvitesNothing: in a vendor org only its admins
// handle invites. Its project managers can't create one, see one, resend or
// revoke one, not even a linguist's — in a client org a project manager can.
func TestIntegrationVendorPMInvitesNothing(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router, _ := testutil.NewTestRouterWithIssues(pool, nil, nil)

	client := testutil.SeedTenant(t, q, "Client Corp")
	clientPM := testutil.SeedUserWithRole(t, q, client.ID, "pm@client.test", "pw", auth.RoleProjectManager)
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	vadmin := testutil.SeedUserWithRole(t, q, vendor.ID, "boss@acme.test", "pw", auth.RoleAdmin)
	vpm := testutil.SeedUserWithRole(t, q, vendor.ID, "pat@acme.test", "pw", auth.RoleProjectManager)

	call := func(userID, tenantID int64, role, method, path string, body any) int {
		t.Helper()
		return testutil.DoRequestWithRole(t, router, method, path, body, userID, tenantID, role).Code
	}
	linguist := map[string]string{"email": "new-ling@acme.test", "role": auth.RoleLinguist}

	if code := call(vpm.ID, vendor.ID, auth.RoleProjectManager, http.MethodPost, "/api/invites", linguist); code != http.StatusForbidden {
		t.Errorf("vendor PM creates a linguist invite = %d, want 403", code)
	}
	// The vendor admin's invite exists; the vendor PM can't see or touch it.
	rec := testutil.DoRequestWithRole(t, router, http.MethodPost, "/api/invites", linguist, vadmin.ID, vendor.ID, auth.RoleAdmin)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &created)
	id := created.Data.ID

	rec = testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/invites", nil, vpm.ID, vendor.ID, auth.RoleProjectManager)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list struct {
		Data []struct{ ID int64 } `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 0 {
		t.Errorf("vendor PM lists %d invites, want none", len(list.Data))
	}
	if code := call(vpm.ID, vendor.ID, auth.RoleProjectManager, http.MethodPost, fmt.Sprintf("/api/invites/%d/resend", id), nil); code != http.StatusNotFound {
		t.Errorf("vendor PM resends = %d, want 404", code)
	}
	if code := call(vpm.ID, vendor.ID, auth.RoleProjectManager, http.MethodDelete, fmt.Sprintf("/api/invites/%d", id), nil); code != http.StatusNotFound {
		t.Errorf("vendor PM revokes = %d, want 404", code)
	}
	if code := call(vadmin.ID, vendor.ID, auth.RoleAdmin, http.MethodDelete, fmt.Sprintf("/api/invites/%d", id), nil); code != http.StatusNoContent && code != http.StatusOK {
		t.Errorf("vendor admin revokes = %d, want success", code)
	}

	// The accepted ones too: the vendor admin sees an accepted linguist
	// invite, the vendor PM does not.
	rec = testutil.DoRequestWithRole(t, router, http.MethodPost, "/api/invites", map[string]string{"email": "joined@acme.test", "role": auth.RoleLinguist}, vadmin.ID, vendor.ID, auth.RoleAdmin)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	testutil.DecodeJSON(t, rec, &created)
	if _, err := pool.Exec(context.Background(), `UPDATE invites SET accepted_at = now() WHERE id = $1`, created.Data.ID); err != nil {
		t.Fatal(err)
	}
	accepted := func(userID int64, role string) int {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/invites/accepted", nil, userID, vendor.ID, role)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var page struct {
			Data []struct{ ID int64 } `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &page)
		return len(page.Data)
	}
	if n := accepted(vadmin.ID, auth.RoleAdmin); n != 1 {
		t.Errorf("vendor admin sees %d accepted invites, want 1", n)
	}
	if n := accepted(vpm.ID, auth.RoleProjectManager); n != 0 {
		t.Errorf("vendor PM sees %d accepted invites, want none", n)
	}

	// A client org's project manager still invites linguists.
	clientLing := map[string]string{"email": "ling@client.test", "role": auth.RoleLinguist}
	if code := call(clientPM.ID, client.ID, auth.RoleProjectManager, http.MethodPost, "/api/invites", clientLing); code != http.StatusCreated {
		t.Errorf("client PM invites a linguist = %d, want 201", code)
	}
}
