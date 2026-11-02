package issues_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"openloc/internal/issues"
)

// sortSeed is one issue with every field a list sorts by.
type sortSeed struct {
	title, kind, severity, area, status, priority string
	tenant, reporter                              int64
	assignee                                      int64 // 0: unassigned
	updated, created                              time.Time
}

// seedSortable files issues directly, so each sorted field has a chosen value,
// and names the users and orgs that the "Reported by" and "Assignee" columns
// show.
func (f *fixture) seedSortable(t *testing.T) []sortSeed {
	t.Helper()
	ctx := context.Background()
	for id, name := range map[int64]string{f.reporter.ID: "Rita", f.colleague.ID: "Carl", f.peer.ID: "Paula", f.outsider.ID: "Otto", f.operator.ID: "Oscar"} {
		if _, err := f.pool.Exec(ctx, `UPDATE users SET name = $2 WHERE id = $1`, id, name); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }
	acme, other := f.tenant.ID, f.other.ID
	// Created times run in neither the id's nor the last update's order; E
	// and F share theirs, and the id decides.
	seeds := []sortSeed{
		{"A", "bug", "blocker", "editor", "open", "high", acme, f.reporter.ID, f.operator.ID, at(0), at(-5)},
		{"B", "bug", "minor", "tm", "resolved", "", acme, f.colleague.ID, 0, at(1), at(-3)},
		{"C", "enhancement", "", "qa", "open", "medium", acme, f.peer.ID, f.colleague.ID, at(2), at(-9)},
		{"D", "feature_request", "", "api", "in_progress", "low", acme, f.reporter.ID, f.operator.ID, at(3), at(-7)},
		{"E", "bug", "critical", "other", "needs_info", "high", other, f.outsider.ID, 0, at(4), at(-8)},
		{"F", "bug", "major", "editor", "open", "", acme, f.colleague.ID, f.colleague.ID, at(4), at(-8)},
		{"G", "enhancement", "", "tm", "wont_fix", "low", other, f.outsider.ID, f.operator.ID, at(5), at(-10)},
		{"H", "bug", "major", "qa", "open", "medium", acme, f.reporter.ID, 0, at(6), at(-4)},
	}
	null := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	for _, s := range seeds {
		var assignee any
		if s.assignee != 0 {
			assignee = s.assignee
		}
		if _, err := f.pool.Exec(ctx, `INSERT INTO issues (tenant_id, reporter_id, kind, severity, area, title, description, status, priority, assignee_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'Seeded for sorting.', $7, $8, $9, $10, $11)`,
			s.tenant, s.reporter, s.kind, null(s.severity), s.area, s.title, s.status, null(s.priority), assignee, s.created, s.updated); err != nil {
			t.Fatalf("seed %s: %v", s.title, err)
		}
	}
	return seeds
}

// listTitles reads a whole list in pages of limit, following each cursor.
func listTitles(t *testing.T, get func(path string) *http.Response, base, sortParam string, limit int) []string {
	t.Helper()
	var titles []string
	cursor := ""
	for page := 0; page < 50; page++ {
		q := url.Values{"limit": {itoa(int64(limit))}}
		if sortParam != "" {
			q.Set("sort", sortParam)
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		res := get(base + "?" + q.Encode())
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s sort=%s: status %d", base, sortParam, res.StatusCode)
		}
		var body struct {
			Data []struct {
				Title string `json:"title"`
			} `json:"data"`
			Cursor  string `json:"cursor"`
			HasMore bool   `json:"has_more"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, d := range body.Data {
			titles = append(titles, d.Title)
		}
		if !body.HasMore {
			return titles
		}
		cursor = body.Cursor
	}
	t.Fatalf("%s sort=%s: more than 50 pages", base, sortParam)
	return nil
}

// expectedOrder sorts seeds as the list must, from these ranks alone.
func expectedOrder(t *testing.T, seeds []sortSeed, sortParam string, names map[int64]string, emails map[int64]string, orgs map[int64]string, platform bool) []string {
	t.Helper()
	rank := func(order ...string) func(string) int {
		return func(v string) int {
			for i, o := range order {
				if o == v {
					return i
				}
			}
			return len(order)
		}
	}
	status := rank("needs_info", "open", "in_progress", "resolved", "wont_fix", "duplicate")
	priority := rank("high", "medium", "low")
	kind := rank("bug", "enhancement", "feature_request")
	severity := rank("blocker", "critical", "major", "minor")
	area := rank("api", "editor", "other", "qa", "tm") // by label: API, Editor, Other, Quality assurance, Translation memory
	// cmp is -1, 0 or 1 for one key, ascending.
	cmp := func(key string, a, b sortSeed) int {
		ints := func(x, y int) int {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		}
		strs := func(x, y string) int { return strings.Compare(strings.ToLower(x), strings.ToLower(y)) }
		switch key {
		case "status":
			return ints(status(a.status), status(b.status))
		case "priority":
			return ints(priority(a.priority), priority(b.priority))
		case "kind":
			if c := ints(kind(a.kind), kind(b.kind)); c != 0 {
				return c
			}
			return ints(severity(a.severity), severity(b.severity))
		case "area":
			return ints(area(a.area), area(b.area))
		case "assignee":
			if (a.assignee == 0) != (b.assignee == 0) {
				return map[bool]int{true: 1, false: -1}[a.assignee == 0]
			}
			return strs(names[a.assignee], names[b.assignee])
		case "reporter":
			first := names
			ka, kb := a.reporter, b.reporter
			if platform {
				first, ka, kb = orgs, a.tenant, b.tenant
			}
			if c := strs(first[ka], first[kb]); c != 0 {
				return c
			}
			return strs(emails[a.reporter], emails[b.reporter])
		case "updated":
			return a.updated.Compare(b.updated)
		case "created":
			return a.created.Compare(b.created)
		}
		t.Fatalf("no expectation for key %q", key)
		return 0
	}
	var levels []string
	if sortParam != "" {
		levels = strings.Split(sortParam, ",")
	}
	ids := map[string]int{}
	for i, s := range seeds {
		ids[s.title] = i // seeded in order, so ids grow with the index
	}
	sorted := append([]sortSeed(nil), seeds...)
	sort.SliceStable(sorted, func(i, j int) bool {
		for _, l := range append(levels, "-created") {
			c := cmp(strings.TrimPrefix(l, "-"), sorted[i], sorted[j])
			if strings.HasPrefix(l, "-") {
				c = -c
			}
			if c != 0 {
				return c < 0
			}
		}
		return ids[sorted[i].title] > ids[sorted[j].title]
	})
	out := make([]string, len(sorted))
	for i, s := range sorted {
		out[i] = s.title
	}
	return out
}

func (f *fixture) people() (names, emails, orgs map[int64]string) {
	names = map[int64]string{f.reporter.ID: "Rita", f.colleague.ID: "Carl", f.peer.ID: "Paula", f.outsider.ID: "Otto", f.operator.ID: "Oscar"}
	emails = map[int64]string{f.reporter.ID: f.reporter.Email, f.colleague.ID: f.colleague.Email, f.peer.ID: f.peer.Email, f.outsider.ID: f.outsider.Email}
	orgs = map[int64]string{f.tenant.ID: f.tenant.Name, f.other.ID: f.other.Name}
	return
}

// The support team's list sorts by every key, in both directions and on up
// to three levels, the same whether read at once or a row at a time.
func TestIntegrationIssueSort_Platform(t *testing.T) {
	f := newFixture(t)
	seeds := f.seedSortable(t)
	names, emails, orgs := f.people()
	get := func(path string) *http.Response { return f.asOperator(t, http.MethodGet, path, nil).Result() }
	for _, s := range []string{
		"", "updated", "-updated", "status", "-status", "priority", "-priority", "kind", "-kind", "area", "-area",
		"assignee", "-assignee", "reporter", "-reporter",
		"priority,-status", "assignee,kind,updated", "-area,priority", "status,-updated", "-kind,assignee,-reporter",
	} {
		want := expectedOrder(t, seeds, s, names, emails, orgs, true)
		for _, limit := range []int{100, 1, 3} {
			if got := listTitles(t, get, "/api/platform/issues", s, limit); !reflect.DeepEqual(got, want) {
				t.Errorf("sort=%q limit=%d: %v, want %v", s, limit, got, want)
			}
		}
	}
}

// An org's manager sorts every member's issues by the keys of their list;
// no sort shows another org's issue, and a linguist sees only their own.
func TestIntegrationIssueSort_Member(t *testing.T) {
	f := newFixture(t)
	seeds := f.seedSortable(t)
	names, emails, orgs := f.people()
	var own, acme []sortSeed
	for _, s := range seeds {
		if s.tenant == f.tenant.ID {
			acme = append(acme, s)
			if s.reporter == f.reporter.ID {
				own = append(own, s)
			}
		}
	}
	manager := func(path string) *http.Response { return f.get(t, path, f.colleague.ID, f.tenant.ID).Result() }
	linguist := func(path string) *http.Response { return f.get(t, path, f.reporter.ID, f.tenant.ID).Result() }
	for _, s := range []string{"", "updated", "status", "-kind", "area", "reporter", "-reporter", "status,-reporter", "kind,area,updated"} {
		for _, limit := range []int{100, 1} {
			if got, want := listTitles(t, manager, "/api/issues", s, limit), expectedOrder(t, acme, s, names, emails, orgs, false); !reflect.DeepEqual(got, want) {
				t.Errorf("manager sort=%q limit=%d: %v, want %v", s, limit, got, want)
			}
			if got, want := listTitles(t, linguist, "/api/issues", s, limit), expectedOrder(t, own, s, names, emails, orgs, false); !reflect.DeepEqual(got, want) {
				t.Errorf("linguist sort=%q limit=%d: %v, want %v", s, limit, got, want)
			}
		}
	}
}

// Priority and assignee are the support team's columns; a cursor belongs to
// the order it was made for.
func TestIntegrationIssueSort_Refusals(t *testing.T) {
	f := newFixture(t)
	f.seedSortable(t)
	for _, s := range []string{"priority", "assignee", "title", "status,status", "kind,area,status,updated"} {
		if rec := f.get(t, "/api/issues?sort="+url.QueryEscape(s), f.colleague.ID, f.tenant.ID); rec.Code != http.StatusBadRequest {
			t.Errorf("member sort=%s: %d", s, rec.Code)
		}
	}
	rec := f.asOperator(t, http.MethodGet, "/api/platform/issues?sort=status&limit=2", nil)
	var page struct {
		Cursor string `json:"cursor"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil || page.Cursor == "" {
		t.Fatalf("first page: %v %q", err, page.Cursor)
	}
	for _, s := range []string{"-status", "priority", ""} {
		if rec := f.asOperator(t, http.MethodGet, "/api/platform/issues?sort="+url.QueryEscape(s)+"&cursor="+page.Cursor, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("a status cursor with sort=%q: %d", s, rec.Code)
		}
	}
	// The members' list refuses the support team's cursor.
	if rec := f.get(t, "/api/issues?sort=status&cursor="+page.Cursor, f.colleague.ID, f.tenant.ID); rec.Code != http.StatusBadRequest {
		t.Errorf("the support team's cursor on the members' list: %d", rec.Code)
	}
}

// A list row carries the same issue as the issue's own page: the row is read
// by column position.
func TestIntegrationIssueSort_RowsMatchGet(t *testing.T) {
	f := newFixture(t)
	f.seedSortable(t)
	rec := f.asOperator(t, http.MethodGet, "/api/platform/issues?sort=assignee,-kind", nil)
	var page struct {
		Data []issues.PlatformIssueView `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil || len(page.Data) == 0 {
		t.Fatalf("list: %v %d", err, len(page.Data))
	}
	for _, row := range page.Data {
		got := f.platformGet(t, row.ID).Issue
		strip := func(v issues.PlatformIssueView) issues.PlatformIssueView {
			v.Duplicates, v.Attachments, v.Context = nil, nil, nil
			return v
		}
		if a, b := strip(row), strip(got); !reflect.DeepEqual(a, b) {
			t.Errorf("issue %d: list %+v\nget %+v", row.ID, a, b)
		}
	}
}

// Names outside ASCII page the same as they read at once, however the
// database collates them.
func TestIntegrationIssueSort_NonASCIINames(t *testing.T) {
	f := newFixture(t)
	f.seedSortable(t)
	for id, name := range map[int64]string{f.peer.ID: "Émile", f.colleague.ID: "Zoë", f.operator.ID: "Ørjan"} {
		if _, err := f.pool.Exec(context.Background(), `UPDATE users SET name = $2 WHERE id = $1`, id, name); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) *http.Response { return f.asOperator(t, http.MethodGet, path, nil).Result() }
	for _, s := range []string{"assignee", "-assignee", "assignee,-reporter"} {
		if all, paged := listTitles(t, get, "/api/platform/issues", s, 100), listTitles(t, get, "/api/platform/issues", s, 1); !reflect.DeepEqual(all, paged) {
			t.Errorf("sort=%s: paged %v, at once %v", s, paged, all)
		}
	}
	manager := func(path string) *http.Response { return f.get(t, path, f.colleague.ID, f.tenant.ID).Result() }
	if all, paged := listTitles(t, manager, "/api/issues", "reporter", 100), listTitles(t, manager, "/api/issues", "reporter", 1); !reflect.DeepEqual(all, paged) {
		t.Errorf("members' reporter sort: paged %v, at once %v", paged, all)
	}
}
