package issues_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"openloc/internal/issues"
)

// The support team's reporter filter matches an issue of a picked org or by a
// picked reporter; the reporters endpoint lists everyone who has issues, once,
// with their orgs.
func TestIntegrationIssueReporterFilter(t *testing.T) {
	f := newFixture(t)
	f.seedSortable(t)
	operator := func(path string) *http.Response { return f.asOperator(t, http.MethodGet, path, nil).Result() }

	rec := f.asOperator(t, http.MethodGet, "/api/platform/issues/reporters", nil)
	var people struct {
		Data []issues.IssueReporter `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&people); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("reporters: %d %v", rec.Code, err)
	}
	orgsOf := map[int64]string{}
	for _, p := range people.Data {
		if _, twice := orgsOf[p.ID]; twice {
			t.Errorf("%s listed twice", p.Email)
		}
		orgsOf[p.ID] = p.Orgs
	}
	for id, org := range map[int64]string{f.reporter.ID: f.tenant.Name, f.colleague.ID: f.tenant.Name, f.peer.ID: f.tenant.Name, f.outsider.ID: f.other.Name} {
		if orgsOf[id] != org {
			t.Errorf("reporter %d: orgs %q, want %q", id, orgsOf[id], org)
		}
	}
	if _, ok := orgsOf[f.operator.ID]; ok || len(orgsOf) != 4 {
		t.Errorf("reporters %v: want the four who filed issues", orgsOf)
	}
	if rec := f.get(t, "/api/platform/issues/reporters", f.colleague.ID, f.tenant.ID); rec.Code != http.StatusForbidden {
		t.Errorf("a member listing every org's reporters: %d", rec.Code)
	}

	for name, c := range map[string]struct {
		query string
		want  []string
	}{
		"one reporter":                 {"reporter=" + itoa(f.colleague.ID), []string{"B", "F"}},
		"two reporters":                {"reporter=" + itoa(f.colleague.ID) + "," + itoa(f.peer.ID), []string{"B", "C", "F"}},
		"one org":                      {"org=" + itoa(f.other.ID), []string{"E", "G"}},
		"an org or a reporter":         {"org=" + itoa(f.other.ID) + "&reporter=" + itoa(f.peer.ID), []string{"C", "E", "G"}},
		"a reporter of the picked org": {"org=" + itoa(f.other.ID) + "&reporter=" + itoa(f.outsider.ID), []string{"E", "G"}},
		"with another filter":          {"reporter=" + itoa(f.colleague.ID) + "&status=open", []string{"F"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := listTitles(t, func(p string) *http.Response { return operator(p + "&" + c.query) }, "/api/platform/issues", "", 1)
			slices.Sort(got)
			if !slices.Equal(got, c.want) {
				t.Errorf("titles %v, want %v", got, c.want)
			}
			if n := countOf(t, operator("/api/platform/issues/count?"+c.query)); n != int64(len(c.want)) {
				t.Errorf("count %d, want %d", n, len(c.want))
			}
		})
	}
	for _, q := range []string{"reporter=none", "reporter=x"} {
		if rec := f.asOperator(t, http.MethodGet, "/api/platform/issues?"+q, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}

	// Someone whose only issue is deleted is gone; someone who reports in two
	// orgs is listed once, with both.
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE issues SET deleted_at = now() WHERE reporter_id = $1`, f.peer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO issues (tenant_id, reporter_id, kind, area, title, description)
		VALUES ($1, $2, 'enhancement', 'other', 'In two orgs', 'Seeded for the reporter list.')`, f.other.ID, f.colleague.ID); err != nil {
		t.Fatal(err)
	}
	people.Data = nil
	if rec := f.asOperator(t, http.MethodGet, "/api/platform/issues/reporters", nil); json.NewDecoder(rec.Body).Decode(&people) != nil {
		t.Fatal("reporters after the changes")
	}
	both := []string{f.tenant.Name, f.other.Name}
	slices.Sort(both)
	byID := map[int64][]string{}
	for _, p := range people.Data {
		byID[p.ID] = append(byID[p.ID], p.Orgs)
	}
	if _, ok := byID[f.peer.ID]; ok {
		t.Error("a reporter whose only issue is deleted is still listed")
	}
	if got := byID[f.colleague.ID]; !slices.Equal(got, []string{strings.Join(both, ", ")}) {
		t.Errorf("a reporter in two orgs: %q, want once with %q", got, strings.Join(both, ", "))
	}
}
