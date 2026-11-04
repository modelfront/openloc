package auth_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"openloc/internal/auth"
	"openloc/internal/testutil"
)

type facets struct {
	Roles   []string `json:"roles"`
	Sources []string `json:"sources"`
	Targets []string `json:"targets"`
}

func joined(s []string) string { return strings.Join(s, ",") }

// TestIntegrationMemberFacets: the Members filters offer only values with at
// least one hit, given the other filters; a filter never narrows its own list.
func TestIntegrationMemberFacets(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	org := testutil.SeedTenant(t, q, "Facet Org")
	owner := testutil.SeedUserWithRole(t, q, org.ID, "owner@facet.test", "pw", auth.RoleOwner)
	pm := testutil.SeedUserWithRole(t, q, org.ID, "pm@facet.test", "pw", auth.RoleProjectManager)
	lin := testutil.SeedUserWithRole(t, q, org.ID, "carl@facet.test", "pw", auth.RoleLinguist)
	lin2 := testutil.SeedUserWithRole(t, q, org.ID, "dana@facet.test", "pw", auth.RoleLinguist)
	testutil.SeedLanguagePair(t, q, org.ID, lin.ID, "en", "de")
	testutil.SeedLanguagePair(t, q, org.ID, lin.ID, "en", "ja")
	testutil.SeedLanguagePair(t, q, org.ID, lin2.ID, "fr", "en")
	testutil.SeedLanguagePair(t, q, org.ID, pm.ID, "de", "en")

	// Another org's member with other languages and roles must not show up.
	other := testutil.SeedTenant(t, q, "Other Facet Org")
	stranger := testutil.SeedUserWithRole(t, q, other.ID, "stranger@facet.test", "pw", auth.RoleAdmin)
	testutil.SeedLanguagePair(t, q, other.ID, stranger.ID, "ko", "zh-cn")

	get := func(params url.Values) facets {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", "/api/members/facets?"+params.Encode(), nil, owner.ID, org.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		return testutil.DecodeData[facets](t, rec)
	}
	check := func(label string, got facets, roles, sources, targets string) {
		t.Helper()
		if joined(got.Roles) != roles || joined(got.Sources) != sources || joined(got.Targets) != targets {
			t.Errorf("%s = roles %v sources %v targets %v, want roles [%s] sources [%s] targets [%s]",
				label, got.Roles, got.Sources, got.Targets, roles, sources, targets)
		}
	}

	// No filters: every role and language in this org, most powerful role first.
	check("none", get(url.Values{}), "owner,project_manager,linguist", "de,en,fr", "de,en,ja")
	// Source en: targets narrow to en's; sources stay whole (a filter never
	// narrows its own list); roles narrow to people with an en pair.
	check("source=en", get(url.Values{"source": {"en"}}), "linguist", "de,en,fr", "de,ja")
	// Role linguist: the languages narrow to linguists' pairs.
	check("role=linguist", get(url.Values{"role": {"linguist"}}), "owner,project_manager,linguist", "en,fr", "de,en,ja")
	// Search narrows everything else.
	check("q=dana", get(url.Values{"q": {"dana"}}), "linguist", "fr", "en")

	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/members/facets", nil, pm.ID, org.ID, auth.RoleProjectManager)
	testutil.AssertStatus(t, rec, http.StatusForbidden)
}

// TestIntegrationVendorOrgFacets: the Vendors filters offer only languages
// some vendor's people cover, given the search and the other side.
func TestIntegrationVendorOrgFacets(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	client := testutil.SeedTenant(t, q, "Facet Client")
	owner := testutil.SeedUser(t, q, client.ID, "owner@facetclient.test", "pw")
	acme := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	testutil.SeedLanguagePair(t, q, acme.ID, testutil.SeedUserWithRole(t, q, acme.ID, "a@acme.test", "pw", auth.RoleAdmin).ID, "en", "de")
	bolt := testutil.SeedVendorTenant(t, q, client.ID, "Bolt Lingua")
	testutil.SeedLanguagePair(t, q, bolt.ID, testutil.SeedUserWithRole(t, q, bolt.ID, "b@bolt.test", "pw", auth.RoleAdmin).ID, "fr", "it")
	foreign := testutil.SeedVendorTenant(t, q, testutil.SeedTenant(t, q, "Other Client").ID, "Foreign")
	testutil.SeedLanguagePair(t, q, foreign.ID, testutil.SeedUserWithRole(t, q, foreign.ID, "f@foreign.test", "pw", auth.RoleAdmin).ID, "ko", "zh-cn")

	get := func(params url.Values) facets {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", "/api/vendor-orgs/facets?"+params.Encode(), nil, owner.ID, client.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		return testutil.DecodeData[facets](t, rec)
	}
	if f := get(url.Values{}); joined(f.Sources) != "en,fr" || joined(f.Targets) != "de,it" {
		t.Errorf("none = %+v, want sources en,fr and targets de,it", f)
	}
	if f := get(url.Values{"target": {"de"}}); joined(f.Sources) != "en" || joined(f.Targets) != "de,it" {
		t.Errorf("target=de = %+v, want sources en and targets de,it", f)
	}
	if f := get(url.Values{"q": {"bolt"}}); joined(f.Sources) != "fr" || joined(f.Targets) != "it" {
		t.Errorf("q=bolt = %+v, want sources fr and targets it", f)
	}
}
