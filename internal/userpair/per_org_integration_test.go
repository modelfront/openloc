package userpair_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationUserPairsPerOrg: a person in several orgs has separate pairs
// in each. A vendor admin sets the pairs of someone who is also a member of
// the client, and the client's pairs, assignee picker and member filter do
// not change; removing the person from an org removes only that org's pairs.
func TestIntegrationUserPairsPerOrg(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	client := testutil.SeedTenant(t, q, "Client Corp")
	clientAdmin := testutil.SeedUserWithRole(t, q, client.ID, "admin@client.test", "pw", auth.RoleAdmin)
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	vadmin := testutil.SeedUserWithRole(t, q, vendor.ID, "boss@acme.test", "pw", auth.RoleAdmin)
	both := testutil.SeedUserWithRole(t, q, client.ID, "sam@client.test", "pw", auth.RoleLinguist)
	if err := q.CreateMembership(ctx, db.CreateMembershipParams{UserID: both.ID, TenantID: vendor.ID, Role: auth.RoleLinguist}); err != nil {
		t.Fatalf("vendor membership: %v", err)
	}

	put := func(actor db.User, tenantID int64, target string) {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, http.MethodPut, fmt.Sprintf("/api/users/%d/language-pairs", both.ID),
			map[string]any{"pairs": []pairDTO{{SourceLang: "en", TargetLang: target}}}, actor.ID, tenantID, auth.RoleAdmin)
		testutil.AssertStatus(t, rec, http.StatusOK)
	}
	pairsIn := func(actor db.User, tenantID int64) []pairDTO {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, http.MethodGet, fmt.Sprintf("/api/users/%d/language-pairs", both.ID),
			nil, actor.ID, tenantID, auth.RoleAdmin)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var out pairListResp
		testutil.DecodeJSON(t, rec, &out)
		return out.Data
	}
	qualified := func(target string) []string {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/users/by-language-pair?source=en&target="+target,
			nil, clientAdmin.ID, client.ID, auth.RoleAdmin)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var out userListResp
		testutil.DecodeJSON(t, rec, &out)
		var emails []string
		for _, u := range out.Data {
			emails = append(emails, u.Email)
		}
		return emails
	}

	put(clientAdmin, client.ID, "de")
	put(vadmin, vendor.ID, "fr")

	if got := pairsIn(clientAdmin, client.ID); len(got) != 1 || got[0] != (pairDTO{"en", "de"}) {
		t.Errorf("client's pairs = %+v, want only en→de", got)
	}
	if got := pairsIn(vadmin, vendor.ID); len(got) != 1 || got[0] != (pairDTO{"en", "fr"}) {
		t.Errorf("vendor's pairs = %+v, want only en→fr", got)
	}
	testutil.AssertStrings(t, "client picker for en→de", qualified("de"), []string{both.Email})
	testutil.AssertStrings(t, "client picker for en→fr", qualified("fr"), nil)

	getJSON := func(path string, actor db.User, tenantID int64, out any) {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, http.MethodGet, path, nil, actor.ID, tenantID, auth.RoleAdmin)
		testutil.AssertStatus(t, rec, http.StatusOK)
		testutil.DecodeJSON(t, rec, out)
	}
	type listRow struct {
		Email         string    `json:"email"`
		Name          string    `json:"name"`
		LanguagePairs []pairDTO `json:"language_pairs"`
	}
	type list struct {
		Data []listRow `json:"data"`
	}
	type facets struct {
		Data struct {
			Targets []string `json:"targets"`
		} `json:"data"`
	}
	pairsOf := func(rows []listRow, key string) []string {
		t.Helper()
		for _, r := range rows {
			if r.Email == key || r.Name == key {
				var out []string
				for _, p := range r.LanguagePairs {
					out = append(out, p.SourceLang+"→"+p.TargetLang)
				}
				return out
			}
		}
		t.Fatalf("%s missing from %+v", key, rows)
		return nil
	}

	// The Members page: each org's row shows its own pairs, and its pair
	// filter and Target facet know only those.
	var members list
	getJSON("/api/members/page", clientAdmin, client.ID, &members)
	testutil.AssertStrings(t, "client's Members row", pairsOf(members.Data, both.Email), []string{"en→de"})
	getJSON("/api/members/page", vadmin, vendor.ID, &members)
	testutil.AssertStrings(t, "vendor's Members row", pairsOf(members.Data, both.Email), []string{"en→fr"})
	getJSON("/api/members/page?source=en&target=fr", clientAdmin, client.ID, &members)
	if len(members.Data) != 0 {
		t.Errorf("client's members filtered by the vendor's pair en→fr = %+v, want none", members.Data)
	}
	var f facets
	getJSON("/api/members/facets", clientAdmin, client.ID, &f)
	testutil.AssertStrings(t, "client's Target facet", f.Data.Targets, []string{"de"})

	// The client's Vendors list: the vendor's languages are the pairs the
	// vendor set, never the client's.
	var vendors list
	getJSON("/api/vendor-orgs", clientAdmin, client.ID, &vendors)
	testutil.AssertStrings(t, "Vendors row", pairsOf(vendors.Data, "Acme Vendors"), []string{"en→fr"})
	getJSON("/api/vendor-orgs?source=en&target=de", clientAdmin, client.ID, &vendors)
	if len(vendors.Data) != 0 {
		t.Errorf("Vendors filtered by the client's pair en→de = %+v, want none", vendors.Data)
	}
	getJSON("/api/vendor-orgs/facets", clientAdmin, client.ID, &f)
	testutil.AssertStrings(t, "Vendors Target facet", f.Data.Targets, []string{"fr"})

	if err := q.DeleteMembership(ctx, db.DeleteMembershipParams{UserID: both.ID, TenantID: vendor.ID}); err != nil {
		t.Fatalf("remove from vendor: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_language_pairs WHERE user_id = $1 AND tenant_id = $2`, both.ID, vendor.ID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("vendor's pairs after removal = %d rows, want 0", left)
	}
	if got := pairsIn(clientAdmin, client.ID); len(got) != 1 || got[0] != (pairDTO{"en", "de"}) {
		t.Errorf("client's pairs after the vendor removed the person = %+v, want en→de", got)
	}
}
