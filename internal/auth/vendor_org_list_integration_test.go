package auth_test

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"openloc/internal/auth"
	"openloc/internal/testutil"
)

type vendorOrgPair struct {
	SourceLang string `json:"source_lang"`
	TargetLang string `json:"target_lang"`
}

type vendorOrgAdmin struct {
	Email    string `json:"email"`
	Disabled bool   `json:"disabled"`
}

type vendorOrgView struct {
	ID            int64            `json:"id"`
	Name          string           `json:"name"`
	Admins        []vendorOrgAdmin `json:"admins"`
	LanguagePairs []vendorOrgPair  `json:"language_pairs"`
}

type vendorOrgPage struct {
	Data    []vendorOrgView `json:"data"`
	Cursor  int64           `json:"cursor"`
	HasMore bool            `json:"has_more"`
	Total   int64           `json:"total"`
}

// TestIntegrationListVendorOrgs: the Members tab's Vendors list shows the
// client's own vendor orgs with their admins and the languages their
// people cover; search on name or admin email, language filters, pages; never
// another client's; owners and admins only.
func TestIntegrationListVendorOrgs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	client := testutil.SeedTenant(t, q, "Client Corp")
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pass123")
	pm := testutil.SeedUserWithRole(t, q, client.ID, "pm@client.test", "pass123", auth.RoleProjectManager)
	other := testutil.SeedTenant(t, q, "Other Client")

	acme := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	vadmin := testutil.SeedUserWithRole(t, q, acme.ID, "admin@acme.test", "pass123", auth.RoleAdmin)
	ling := testutil.SeedUserWithRole(t, q, acme.ID, "ling@acme.test", "pass123", auth.RoleLinguist)
	testutil.SeedLanguagePair(t, q, acme.ID, ling.ID, "en", "de")
	testutil.SeedLanguagePair(t, q, acme.ID, ling.ID, "en", "fr")
	testutil.SeedLanguagePair(t, q, acme.ID, vadmin.ID, "en", "de") // the same pair twice is listed once

	bolt := testutil.SeedVendorTenant(t, q, client.ID, "Bolt Lingua")
	boltAdmin := testutil.SeedUserWithRole(t, q, bolt.ID, "boss@bolt.test", "pass123", auth.RoleAdmin)
	testutil.SeedLanguagePair(t, q, bolt.ID, boltAdmin.ID, "de", "en")

	testutil.SeedVendorTenant(t, q, client.ID, "Zed Words") // no people with pairs

	// Another client's vendor matches every filter below.
	foreign := testutil.SeedVendorTenant(t, q, other.ID, "Acme Elsewhere")
	fadmin := testutil.SeedUserWithRole(t, q, foreign.ID, "admin@elsewhere.test", "pass123", auth.RoleAdmin)
	testutil.SeedLanguagePair(t, q, foreign.ID, fadmin.ID, "en", "de")

	get := func(params url.Values) vendorOrgPage {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", "/api/vendor-orgs?"+params.Encode(), nil, owner.ID, client.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var p vendorOrgPage
		testutil.DecodeJSON(t, rec, &p)
		return p
	}
	names := func(p vendorOrgPage) []string {
		out := []string{}
		for _, v := range p.Data {
			out = append(out, v.Name)
		}
		return out
	}

	all := get(url.Values{})
	testutil.AssertStrings(t, "all", names(all), []string{"Acme Vendors", "Bolt Lingua", "Zed Words"})
	if all.Total != 3 || all.HasMore {
		t.Errorf("all total=%d has_more=%v, want 3 and false", all.Total, all.HasMore)
	}
	a := all.Data[0]
	if len(a.Admins) != 1 || a.Admins[0] != (vendorOrgAdmin{Email: "admin@acme.test"}) {
		t.Errorf("Acme row = %+v, want admin admin@acme.test", a)
	}
	if len(a.LanguagePairs) != 2 || a.LanguagePairs[0] != (vendorOrgPair{"en", "de"}) || a.LanguagePairs[1] != (vendorOrgPair{"en", "fr"}) {
		t.Errorf("Acme pairs = %+v, want en→de and en→fr once each", a.LanguagePairs)
	}
	if z := all.Data[2]; z.LanguagePairs == nil || len(z.LanguagePairs) != 0 {
		t.Errorf("Zed pairs = %#v, want an empty list", z.LanguagePairs)
	}

	// Pages.
	p1 := get(url.Values{"limit": {"2"}})
	testutil.AssertStrings(t, "page 1", names(p1), []string{"Acme Vendors", "Bolt Lingua"})
	if !p1.HasMore || p1.Total != 3 {
		t.Errorf("page 1 has_more=%v total=%d", p1.HasMore, p1.Total)
	}
	testutil.AssertStrings(t, "page 2", names(get(url.Values{"limit": {"2"}, "cursor": {strconv.FormatInt(p1.Cursor, 10)}})), []string{"Zed Words"})

	// Search: the vendor's name or an admin's email, any case.
	testutil.AssertStrings(t, "q=acme", names(get(url.Values{"q": {"ACME"}})), []string{"Acme Vendors"})
	testutil.AssertStrings(t, "q=boss@", names(get(url.Values{"q": {"boss@"}})), []string{"Bolt Lingua"})

	// Languages: someone in the vendor org has the pair; both together match one pair.
	testutil.AssertStrings(t, "target=de", names(get(url.Values{"target": {"de"}})), []string{"Acme Vendors"})
	testutil.AssertStrings(t, "source=de", names(get(url.Values{"source": {"DE"}})), []string{"Bolt Lingua"})
	testutil.AssertStrings(t, "en→fr", names(get(url.Values{"source": {"en"}, "target": {"fr"}})), []string{"Acme Vendors"})
	testutil.AssertStrings(t, "de→fr", names(get(url.Values{"source": {"de"}, "target": {"fr"}})), []string{})

	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/vendor-orgs", nil, pm.ID, client.ID, auth.RoleProjectManager)
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// A vendor org has no vendors of its own; the barrier refuses the route.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/vendor-orgs", nil, vadmin.ID, acme.ID, auth.RoleAdmin)
	testutil.AssertStatus(t, rec, http.StatusForbidden)
}
