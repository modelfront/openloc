package translationmemory_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type searchPage struct {
	Data []struct {
		SourceText string `json:"source_text"`
	} `json:"data"`
	Total   *int64 `json:"total"`
	HasMore bool   `json:"has_more"`
	Cursor  int64  `json:"cursor"`
}

// searchSources runs GET /api/tms/{id}/entries with the given query string and
// returns the matching source texts, sorted.
func searchSources(t *testing.T, router http.Handler, tmID, userID, tenantID int64, params url.Values) []string {
	t.Helper()
	rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/tms/%d/entries?%s", tmID, params.Encode()), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var page searchPage
	testutil.DecodeJSON(t, rec, &page)
	out := make([]string, 0, len(page.Data))
	for _, e := range page.Data {
		out = append(out, e.SourceText)
	}
	if page.Total == nil || int64(len(out)) != *page.Total {
		t.Errorf("%s: total %v disagrees with %d rows returned", params.Encode(), page.Total, len(out))
	}
	slices.Sort(out)
	return out
}

func seedSearchEntries(t *testing.T, q db.Querier, tmID int64, pairs [][2]string) {
	t.Helper()
	for _, p := range pairs {
		if _, err := q.InsertTM(context.Background(), db.InsertTMParams{
			TmID: tmID, SourceLanguage: "en", TargetLanguage: "fr",
			SourceText: p[0], TargetText: p[1], CreatedBy: "test",
		}); err != nil {
			t.Fatalf("seed %q: %v", p[0], err)
		}
	}
}

func TestIntegrationTMEntrySearch_Options(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "SearchCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "search@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Search TM")

	seedSearchEntries(t, q, tm.ID, [][2]string{
		{"Hello world", "Bonjour le monde"},
		{"hello", "salut"},
		{"Say hello", "Dis bonjour"},
		{"Price: 10.5", "Prix : 10,5"},
		{"10x5 grid", "grille 10x5"},
		{"50% off", "50 % de réduction"},
		{"5000 items", "5000 articles"},
		{"The server is down", "Le serveur est en panne"},
		{"servers", "serveurs"},
	})

	cases := []struct {
		name   string
		params url.Values
		want   []string
	}{
		{"default: case-insensitive substring in both fields",
			url.Values{"q": {"hello"}}, []string{"Hello world", "Say hello", "hello"}},
		{"target-only scope ignores the source",
			url.Values{"q": {"bonjour"}, "scope": {"target"}}, []string{"Hello world", "Say hello"}},
		{"source-only scope ignores the target",
			url.Values{"q": {"bonjour"}, "scope": {"source"}}, []string{}},
		{"case sensitive",
			url.Values{"q": {"Hello"}, "case_sensitive": {"true"}}, []string{"Hello world"}},
		{"exact: the whole field equals the query, any case",
			url.Values{"q": {"HELLO"}, "exact": {"true"}}, []string{"hello"}},
		{"exact + case sensitive",
			url.Values{"q": {"HELLO"}, "exact": {"true"}, "case_sensitive": {"true"}}, []string{}},
		{"exact matches a target field too",
			url.Values{"q": {"salut"}, "exact": {"true"}}, []string{"hello"}},
		{"plain text: a dot is a dot",
			url.Values{"q": {"10.5"}}, []string{"Price: 10.5"}},
		{"plain text: a percent sign is not a wildcard",
			url.Values{"q": {"50%"}}, []string{"50% off"}},
		{"regex: a dot is any character",
			url.Values{"q": {"10.5"}, "regex": {"true"}}, []string{"10x5 grid", "Price: 10.5"}},
		{"regex: JavaScript word boundary",
			url.Values{"q": {`\bserver\b`}, "regex": {"true"}}, []string{"The server is down"}},
		{"regex + exact anchors the whole alternation",
			url.Values{"q": {"hello|servers"}, "regex": {"true"}, "exact": {"true"}}, []string{"hello", "servers"}},
		{"regex + case sensitive",
			url.Values{"q": {"^h"}, "regex": {"true"}, "case_sensitive": {"true"}}, []string{"hello"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := searchSources(t, router, tm.ID, user.ID, tenant.ID, c.params)
			slices.Sort(c.want)
			if !slices.Equal(got, c.want) {
				t.Errorf("%s: got %q, want %q", c.params.Encode(), got, c.want)
			}
		})
	}
}

func TestIntegrationTMEntrySearch_Pagination(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PageCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "page@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Page TM")

	pairs := make([][2]string, 0, 5)
	for i := range 5 {
		pairs = append(pairs, [2]string{fmt.Sprintf("Row %d", i), "ligne"})
	}
	pairs = append(pairs, [2]string{"Other", "autre"})
	seedSearchEntries(t, q, tm.ID, pairs)

	params := url.Values{"q": {`^Row \d$`}, "regex": {"true"}, "case_sensitive": {"true"}, "limit": {"3"}}
	var seen []string
	for i := range 3 {
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/tms/%d/entries?%s", tm.ID, params.Encode()), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var page searchPage
		testutil.DecodeJSON(t, rec, &page)
		// The first page counts every match; later pages skip the count.
		if i == 0 && (page.Total == nil || *page.Total != 5) {
			t.Fatalf("total must count every match, not the page: got %v", page.Total)
		}
		if i > 0 && page.Total != nil {
			t.Fatalf("a later page must leave the total out, got %d", *page.Total)
		}
		for _, e := range page.Data {
			seen = append(seen, e.SourceText)
		}
		if !page.HasMore {
			break
		}
		params.Set("cursor", fmt.Sprint(page.Cursor))
	}
	if len(seen) != 5 || slices.Contains(seen, "Other") {
		t.Errorf("paging through the matches must return each of the 5 rows once, got %q", seen)
	}
}

func TestIntegrationTMEntrySearch_BadInput(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "BadCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "bad@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Bad TM")
	seedSearchEntries(t, q, tm.ID, [][2]string{{"Hello", "Bonjour"}})

	for _, c := range []struct {
		name    string
		params  url.Values
		message string
	}{
		{"unbalanced regex", url.Values{"q": {"(hello"}, "regex": {"true"}}, "This regular expression is not valid."},
		{"JS-only regex syntax", url.Values{"q": {`\p{L}`}, "regex": {"true"}}, "This regular expression is not valid."},
		{"unknown scope", url.Values{"q": {"hello"}, "scope": {"everywhere"}}, "scope must be 'source', 'target' or 'both'"},
		{"search text over the length cap", url.Values{"q": {strings.Repeat("a", 1001)}}, "The search text is too long. Use at most 1000 characters."},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/tms/%d/entries?%s", tm.ID, c.params.Encode()), nil, user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
			var body struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			testutil.DecodeJSON(t, rec, &body)
			if body.Error.Message != c.message {
				t.Errorf("message: got %q, want %q", body.Error.Message, c.message)
			}
		})
	}

	// At the cap it is still a search.
	if got := searchSources(t, router, tm.ID, user.ID, tenant.ID, url.Values{"q": {strings.Repeat("a", 1000)}}); len(got) != 0 {
		t.Errorf("a 1000-character search must run and match nothing here, got %q", got)
	}

	// The same text as a plain search is fine: only regex mode parses it.
	got := searchSources(t, router, tm.ID, user.ID, tenant.ID, url.Values{"q": {"(hello"}})
	if len(got) != 0 {
		t.Errorf("plain '(hello' must match nothing, got %q", got)
	}
}

// helpRow matches one row of web/src/lib/regex-help-patterns.ts.
var helpRow = regexp.MustCompile(`pattern: '((?:[^'\\]|\\.)*)'.*sample: '((?:[^'\\]|\\.)*)'(.*)$`)

// unquoteJS undoes the escapes a single-quoted JS string literal uses here.
func unquoteJS(s string) string {
	return regexp.MustCompile(`\\(.)`).ReplaceAllString(s, "$1")
}

// The TM search's regex help shows the shared cheat sheet; every row it shows
// must work in Postgres, not just in the browser.
func TestIntegrationTMEntrySearch_RegexHelpPatternsWork(t *testing.T) {
	src, err := os.ReadFile("../../web/src/lib/regex-help-patterns.ts")
	if err != nil {
		t.Fatalf("read cheat sheet: %v", err)
	}
	type row struct{ pattern, sample string }
	var rows []row
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "{ pattern: ") {
			continue
		}
		// Every row must parse: a row the regex misses would silently drop
		// out of the Postgres check.
		m := helpRow.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("cannot parse cheat-sheet row (keep one row per line, single-quoted): %s", strings.TrimSpace(line))
		}
		if strings.Contains(m[3], "segmentsOnly: true") {
			continue
		}
		rows = append(rows, row{unquoteJS(m[1]), unquoteJS(m[2])})
	}
	if len(rows) == 0 {
		t.Fatal("found no cheat-sheet rows")
	}

	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "HelpCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "help@test.com", "pass")

	for _, r := range rows {
		t.Run(r.pattern, func(t *testing.T) {
			tm := testutil.SeedTM(t, q, tenant.ID, "Help TM "+r.pattern)
			seedSearchEntries(t, q, tm.ID, [][2]string{{r.sample, "x"}})
			got := searchSources(t, router, tm.ID, user.ID, tenant.ID, url.Values{"q": {r.pattern}, "regex": {"true"}, "scope": {"source"}})
			if len(got) != 1 {
				t.Errorf("pattern %q must find its sample %q in Postgres", r.pattern, r.sample)
			}
		})
	}
}
