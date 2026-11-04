package auth_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type memberPagePair struct {
	SourceLang string `json:"source_lang"`
	TargetLang string `json:"target_lang"`
	RowGroup   int32  `json:"row_group"`
}

type memberPageItem struct {
	ID            int64            `json:"id"`
	Email         string           `json:"email"`
	Name          string           `json:"name"`
	Role          string           `json:"role"`
	LanguagePairs []memberPagePair `json:"language_pairs"`
}

type memberPage struct {
	Data    []memberPageItem `json:"data"`
	Cursor  int64            `json:"cursor"`
	HasMore bool             `json:"has_more"`
	Total   int64            `json:"total"`
}

// TestIntegrationMembersPage: the Members tab's paged list. Most powerful role
// first, then name; search on name or email; role and language filters; each
// member's language pairs inline; this org's members only; owner/admin.
func TestIntegrationMembersPage(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	org := testutil.SeedTenant(t, q, "Paged Org")
	owner := testutil.SeedUserWithRole(t, q, org.ID, "zoe.owner@paged.test", "pw", auth.RoleOwner)
	setName := func(u db.User, name string) {
		if _, err := pool.Exec(ctx, `UPDATE users SET name = $1 WHERE id = $2`, name, u.ID); err != nil {
			t.Fatalf("name %s: %v", name, err)
		}
	}
	setName(owner, "Zoe Owner")
	add := func(email, name, role string) db.User {
		u := testutil.SeedUserWithRole(t, q, org.ID, email, "pw", role)
		setName(u, name)
		return u
	}
	pm := add("bob.pm@paged.test", "Bob Manager", auth.RoleProjectManager)
	add("amy.admin@paged.test", "Amy Admin", auth.RoleAdmin)
	lin1 := add("carl@paged.test", "Carl Linguist", auth.RoleLinguist)
	add("anna@paged.test", "anna linguist", auth.RoleLinguist)
	lin3 := add("percent@paged.test", "100% Dana", auth.RoleLinguist)
	pair := func(u db.User, src, tgt string, group int32) {
		if err := q.InsertUserLanguagePair(ctx, db.InsertUserLanguagePairParams{UserID: u.ID, SourceLang: src, TargetLang: tgt, RowGroup: group, TenantID: org.ID}); err != nil {
			t.Fatalf("pair: %v", err)
		}
	}
	pair(lin1, "en", "de", 0)
	pair(lin1, "fr", "en", 1)
	pair(lin3, "en", "fr", 0)

	// Someone in another org who matches every filter below.
	other := testutil.SeedTenant(t, q, "Other Org")
	stranger := testutil.SeedUserWithRole(t, q, other.ID, "carl.stranger@paged.test", "pw", auth.RoleLinguist)
	setName(stranger, "Carl Stranger")
	if err := q.InsertUserLanguagePair(ctx, db.InsertUserLanguagePairParams{UserID: stranger.ID, SourceLang: "en", TargetLang: "de", TenantID: other.ID}); err != nil {
		t.Fatalf("stranger pair: %v", err)
	}

	get := func(userID int64, role string, params url.Values) memberPage {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "GET", "/api/members/page?"+params.Encode(), nil, userID, org.ID, role)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var p memberPage
		testutil.DecodeJSON(t, rec, &p)
		return p
	}
	names := func(p memberPage) []string {
		out := make([]string, len(p.Data))
		for i, m := range p.Data {
			out[i] = m.Name
		}
		return out
	}

	// Order: role rank, then name (case-insensitive). Two pages of 4 then 2.
	first := get(owner.ID, auth.RoleOwner, url.Values{"limit": {"4"}})
	testutil.AssertStrings(t, "page 1", names(first), []string{"Zoe Owner", "Amy Admin", "Bob Manager", "100% Dana"})
	if !first.HasMore || first.Total != 6 {
		t.Errorf("page 1 has_more=%v total=%d, want true and 6", first.HasMore, first.Total)
	}
	second := get(owner.ID, auth.RoleOwner, url.Values{"limit": {"4"}, "cursor": {strconv.FormatInt(first.Cursor, 10)}})
	testutil.AssertStrings(t, "page 2", names(second), []string{"anna linguist", "Carl Linguist"})
	if second.HasMore {
		t.Error("page 2 should be the last")
	}

	// The pairs come with the member, in editor row order.
	for _, m := range second.Data {
		if m.ID == lin1.ID && (len(m.LanguagePairs) != 2 || m.LanguagePairs[0].TargetLang != "de" || m.LanguagePairs[1].RowGroup != 1) {
			t.Errorf("Carl's pairs = %+v", m.LanguagePairs)
		}
	}

	// Search: name or email, any case; % is a plain character, not a wildcard.
	testutil.AssertStrings(t, "q=CARL", names(get(owner.ID, auth.RoleOwner, url.Values{"q": {"CARL"}})), []string{"Carl Linguist"})
	testutil.AssertStrings(t, "q=bob.pm", names(get(owner.ID, auth.RoleOwner, url.Values{"q": {"bob.pm"}})), []string{"Bob Manager"})
	testutil.AssertStrings(t, "q=%", names(get(owner.ID, auth.RoleOwner, url.Values{"q": {"%"}})), []string{"100% Dana"})

	// Filters.
	testutil.AssertStrings(t, "role=linguist", names(get(owner.ID, auth.RoleOwner, url.Values{"role": {"linguist"}})), []string{"100% Dana", "anna linguist", "Carl Linguist"})
	testutil.AssertStrings(t, "target=de", names(get(owner.ID, auth.RoleOwner, url.Values{"target": {"DE"}})), []string{"Carl Linguist"})
	testutil.AssertStrings(t, "source=en", names(get(owner.ID, auth.RoleOwner, url.Values{"source": {"en"}})), []string{"100% Dana", "Carl Linguist"})
	// Source and target together match one pair, not one of each.
	testutil.AssertStrings(t, "fr→de", names(get(owner.ID, auth.RoleOwner, url.Values{"source": {"fr"}, "target": {"de"}})), []string{})
	if p := get(owner.ID, auth.RoleOwner, url.Values{"q": {"nobody"}}); len(p.Data) != 0 || p.Total != 0 || p.HasMore {
		t.Errorf("no match = %+v", p)
	}

	rec := testutil.DoRequest(t, router, "GET", "/api/members/page?role=boss", nil, owner.ID, org.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/members/page", nil, pm.ID, org.ID, auth.RoleProjectManager)
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// A vendor org's admin manages their own members with the same list.
	vendor := testutil.SeedVendorTenant(t, q, org.ID, "Paged Vendors")
	vadmin := testutil.SeedUserWithRole(t, q, vendor.ID, "vadmin@paged.test", "pw", auth.RoleAdmin)
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/members/page", nil, vadmin.ID, vendor.ID, auth.RoleAdmin)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var vp memberPage
	testutil.DecodeJSON(t, rec, &vp)
	if len(vp.Data) != 1 || vp.Data[0].ID != vadmin.ID {
		t.Errorf("vendor org page = %+v, want only its admin", vp.Data)
	}
}
