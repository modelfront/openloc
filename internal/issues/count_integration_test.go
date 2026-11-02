package issues_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// countOf reads a count endpoint's answer.
func countOf(t *testing.T, res *http.Response) int64 {
	t.Helper()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("count: status %d", res.StatusCode)
	}
	var body struct {
		Count *int64 `json:"count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.Count == nil {
		t.Fatalf("count body: %v", err)
	}
	return *body.Count
}

// A list's count is the number of rows the list shows with the same filters,
// over every page, in the viewer's own scope.
func TestIntegrationIssueCount_MatchesTheList(t *testing.T) {
	f := newFixture(t)
	f.seedSortable(t)
	operator := func(path string) *http.Response { return f.asOperator(t, http.MethodGet, path, nil).Result() }
	manager := func(path string) *http.Response { return f.get(t, path, f.colleague.ID, f.tenant.ID).Result() }
	linguist := func(path string) *http.Response { return f.get(t, path, f.reporter.ID, f.tenant.ID).Result() }
	for name, c := range map[string]struct {
		get   func(string) *http.Response
		base  string
		query string
		want  int64
	}{
		"operator, every issue":    {operator, "/api/platform/issues", "", 8},
		"operator, open bugs":      {operator, "/api/platform/issues", "status=open&kind=bug", 3},
		"operator, one org":        {operator, "/api/platform/issues", "org=" + itoa(f.other.ID), 2},
		"operator, unassigned":     {operator, "/api/platform/issues", "assignee=none", 3},
		"operator, a search":       {operator, "/api/platform/issues", "q=Seeded", 8},
		"manager, the org's":       {manager, "/api/issues", "", 6},
		"manager, resolved":        {manager, "/api/issues", "status=resolved", 1},
		"linguist, their own":      {linguist, "/api/issues", "", 3},
		"linguist, their own bugs": {linguist, "/api/issues", "kind=bug", 2},
		"nothing matches":          {manager, "/api/issues", "status=duplicate", 0},
	} {
		t.Run(name, func(t *testing.T) {
			got := countOf(t, c.get(c.base+"/count?"+c.query))
			listed := listTitles(t, func(p string) *http.Response {
				if c.query != "" {
					p += "&" + c.query
				}
				return c.get(p)
			}, c.base, "", 2)
			if got != c.want || got != int64(len(listed)) {
				t.Errorf("count = %d, list shows %d, want %d", got, len(listed), c.want)
			}
		})
	}
}

// The support team's count is theirs alone, and a bad filter is refused as
// the list refuses it.
func TestIntegrationIssueCount_Refusals(t *testing.T) {
	f := newFixture(t)
	f.seedSortable(t)
	if rec := f.get(t, "/api/platform/issues/count", f.colleague.ID, f.tenant.ID); rec.Code != http.StatusForbidden {
		t.Errorf("a member counting every org's issues: %d", rec.Code)
	}
	for _, path := range []string{"/api/issues/count?status=nope", "/api/issues/count?sort=priority"} {
		if rec := f.get(t, path, f.colleague.ID, f.tenant.ID); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if rec := f.asOperator(t, http.MethodGet, "/api/platform/issues/count?org=x", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad org filter: %d", rec.Code)
	}
}
