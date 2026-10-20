package issues_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"openloc/internal/issues"
	"openloc/internal/testutil"
)

// rawJSON decodes a response into generic maps, to assert on the keys the
// server sends rather than on what a Go struct would keep.
func rawJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

// supportOnlyKeys are the issue fields the reporter's org never receives.
var supportOnlyKeys = []string{"priority", "tags", "assignee_id", "import_key"}

// assertNoSupportFields fails when an issue object of the org's API carries a
// support-team field, or an assignee before one is set.
func assertNoSupportFields(t *testing.T, where string, issue map[string]any, assigned bool) {
	t.Helper()
	for _, k := range supportOnlyKeys {
		if _, ok := issue[k]; ok {
			t.Errorf("%s: the org's API returned %q: %v", where, k, issue[k])
		}
	}
	if _, ok := issue["assignee"]; ok != assigned {
		t.Errorf("%s: assignee present = %v, want %v (%v)", where, ok, assigned, issue["assignee"])
	}
}

// The org's API never returns priority, tags, internal notes (nor their files)
// or internal history entries, and no assignee before one is set. Internal
// changes leave the org's view untouched, its last update included.
func TestIntegrationIssue_InternalFieldsNeverReachTheOrg(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Export loses tables")

	if rec := f.asOperator(t, http.MethodPatch, platformPath(issue.ID, ""), map[string]any{
		"priority": "high", "tags": []string{"MVP", "blocks testing"}, "severity": "critical", "area": "qa",
	}); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	rec := f.operatorMultipart(t, platformPath(issue.ID, "/notes"),
		map[string]string{"body": "Root cause sits in the table writer"}, png("internal-trace.png"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("note: %d %s", rec.Code, rec.Body.String())
	}
	op := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(issue.ID, ""), nil))
	if p := op.Issue.Priority; p == nil || *p != "high" || !slices.Equal(op.Issue.Tags, []string{"blocks-testing", "mvp"}) {
		t.Fatalf("operator view = %+v", op.Issue)
	}
	var noteFile int64
	internal := 0
	for _, c := range op.Comments {
		if !c.Internal {
			t.Errorf("operator entry not internal: %+v", c)
		}
		internal++
		if len(c.Attachments) == 1 {
			noteFile = c.Attachments[0].ID
		}
	}
	// priority, tags, severity, area, and the note.
	if internal != 5 || noteFile == 0 {
		t.Fatalf("operator thread: %d internal entries, note file %d: %+v", internal, noteFile, op.Comments)
	}

	for _, viewer := range []struct {
		name           string
		userID         int64
		listPath, role string
	}{
		{"reporter", f.reporter.ID, "/api/issues", "linguist"},
		{"manager", f.colleague.ID, "/api/issues", "owner"},
	} {
		detail := f.getAs(t, issuePath(issue.ID, ""), viewer.userID, f.tenant.ID, viewer.role)
		if detail.Code != http.StatusOK {
			t.Fatalf("%s detail: %d", viewer.name, detail.Code)
		}
		for _, secret := range []string{"Root cause", "internal-trace", "blocks-testing", "mvp", `"internal"`} {
			if strings.Contains(detail.Body.String(), secret) {
				t.Errorf("%s detail leaks %q: %s", viewer.name, secret, detail.Body.String())
			}
		}
		body := rawJSON(t, detail)["data"].(map[string]any)
		assertNoSupportFields(t, viewer.name+" detail", body["issue"].(map[string]any), false)
		if comments := body["comments"].([]any); len(comments) != 0 {
			t.Errorf("%s sees internal entries: %v", viewer.name, comments)
		}
		if got := decodeDetail(t, detail).Issue.UpdatedAt; !got.Equal(issue.UpdatedAt) {
			t.Errorf("%s: internal changes moved updated_at to %v (filed %v)", viewer.name, got, issue.UpdatedAt)
		}
		list := f.getAs(t, viewer.listPath, viewer.userID, f.tenant.ID, viewer.role)
		items := rawJSON(t, list)["data"].([]any)
		if len(items) != 1 {
			t.Fatalf("%s list: %v", viewer.name, items)
		}
		assertNoSupportFields(t, viewer.name+" list", items[0].(map[string]any), false)
		if strings.Contains(list.Body.String(), "blocks-testing") {
			t.Errorf("%s list leaks tags: %s", viewer.name, list.Body.String())
		}
		if rec := f.getAs(t, issuePath(issue.ID, "/attachments/"+itoa(noteFile)), viewer.userID, f.tenant.ID, viewer.role); rec.Code != http.StatusNotFound {
			t.Errorf("%s downloads the internal note's file: %d", viewer.name, rec.Code)
		}
	}
	if rec := f.asOperator(t, http.MethodGet, platformPath(issue.ID, "/attachments/"+itoa(noteFile)), nil); rec.Code != http.StatusOK {
		t.Errorf("operator note file: %d", rec.Code)
	}
	if s := subjects(f.outbox, "rep@test.com"); len(s) != 0 {
		t.Errorf("internal changes emailed the reporter: %v", s)
	}
	assertIssueNotification(t, f, issue.ID, 0)

	// Once assigned, the org sees who works on it, and the assignment entry.
	// The fields the PATCH leaves out stay as they were.
	op = decodePlatformDetail(t, f.asOperator(t, http.MethodPatch, platformPath(issue.ID, ""), map[string]any{"assignee_id": f.operator.ID}))
	if p := op.Issue.Priority; p == nil || *p != "high" || !slices.Equal(op.Issue.Tags, []string{"blocks-testing", "mvp"}) ||
		op.Issue.Severity == nil || *op.Issue.Severity != "critical" || op.Issue.Area != "qa" {
		t.Fatalf("an assignee-only PATCH changed other fields: %+v", op.Issue)
	}
	detail := f.get(t, issuePath(issue.ID, ""), f.reporter.ID, f.tenant.ID)
	body := rawJSON(t, detail)["data"].(map[string]any)
	assertNoSupportFields(t, "assigned detail", body["issue"].(map[string]any), true)
	d := decodeDetail(t, detail)
	if d.Issue.Assignee == nil || d.Issue.Assignee.Name != f.operator.Name || len(d.Comments) != 1 {
		t.Fatalf("assigned: %+v", d)
	}
	if c := d.Comments[0].Change; c == nil || c.Field != "assignee" || c.From != nil || c.To == nil || *c.To != f.operator.Name {
		t.Errorf("assignment entry = %+v", d.Comments[0].Change)
	}
}

func decodePlatformDetail(t *testing.T, rec *httptest.ResponseRecorder) issues.PlatformIssueDetail {
	t.Helper()
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("platform detail: %d %s", rec.Code, rec.Body.String())
	}
	var env struct{ Data issues.PlatformIssueDetail }
	testutil.DecodeJSON(t, rec, &env)
	return env.Data
}

// Only the support team assigns, prioritizes, tags and writes internal notes,
// and only to a support team member; nothing assigns an issue by itself.
func TestIntegrationIssue_OnlySupportTeamAssigns(t *testing.T) {
	f := newFixture(t)
	const secondOperator = "ops2@test.com"
	f.router, f.outbox = testutil.NewTestRouterWithIssues(f.pool, f.store, []string{operatorEmail, secondOperator})
	second := testutil.SeedUserWithRole(t, f.q, f.opsTenant.ID, secondOperator, "pw", "admin")
	issue := f.report(t, "Assign me")

	for name, req := range map[string]struct {
		userID int64
		role   string
	}{"reporter": {f.reporter.ID, "linguist"}, "org owner": {f.colleague.ID, "owner"}} {
		for _, body := range []map[string]any{{"priority": "high"}, {"assignee_id": f.operator.ID}, {"tags": []string{"mvp"}}, {"kind": "enhancement"}} {
			rec := testutil.DoRequestWithRole(t, f.router, http.MethodPatch, platformPath(issue.ID, ""), body, req.userID, f.tenant.ID, req.role)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s PATCH %v: %d", name, body, rec.Code)
			}
		}
		if rec := f.asMemberWithRole(t, platformPath(issue.ID, "/notes"), req.userID, f.tenant.ID, req.role, map[string]string{"body": "x"}); rec.Code != http.StatusForbidden {
			t.Errorf("%s note: %d", name, rec.Code)
		}
		for _, path := range []string{"/api/platform/issues/assignees", "/api/platform/issues/tags"} {
			if rec := f.getAs(t, path, req.userID, f.tenant.ID, req.role); rec.Code != http.StatusForbidden {
				t.Errorf("%s GET %s: %d", name, path, rec.Code)
			}
		}
	}

	// Moving, answering and triaging an issue leaves it unassigned.
	if rec := f.setStatus(t, issue.ID, "in_progress"); rec.Code != http.StatusOK {
		t.Fatalf("in progress: %d", rec.Code)
	}
	if rec := f.operatorMultipart(t, platformPath(issue.ID, "/comments"), map[string]string{"body": "Looking"}); rec.Code != http.StatusCreated {
		t.Fatalf("comment: %d", rec.Code)
	}
	if rec := f.asOperator(t, http.MethodPatch, platformPath(issue.ID, ""), map[string]any{"priority": "low"}); rec.Code != http.StatusOK {
		t.Fatalf("priority: %d", rec.Code)
	}
	d := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(issue.ID, ""), nil))
	if d.Issue.Status != issues.StatusInProgress || d.Issue.Priority == nil || *d.Issue.Priority != "low" {
		t.Fatalf("the setup did not land: %+v", d.Issue)
	}
	if d.Issue.AssigneeID != nil {
		t.Fatalf("auto-assigned to %d", *d.Issue.AssigneeID)
	}

	if rec := f.asOperator(t, http.MethodPatch, platformPath(issue.ID, ""), map[string]any{"assignee_id": f.colleague.ID}); rec.Code != http.StatusBadRequest {
		t.Errorf("assign to an org owner: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.asOperator(t, http.MethodPatch, platformPath(issue.ID, ""), map[string]any{"priority": "urgent"}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown priority: %d", rec.Code)
	}
	if d := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(issue.ID, ""), nil)); d.Issue.AssigneeID != nil {
		t.Fatalf("a refused assignment stuck: %d", *d.Issue.AssigneeID)
	}

	// An operator assigns a colleague of the support team, then unassigns.
	d = decodePlatformDetail(t, f.asOperator(t, http.MethodPatch, platformPath(issue.ID, ""), map[string]any{"assignee_id": second.ID}))
	if d.Issue.AssigneeID == nil || *d.Issue.AssigneeID != second.ID {
		t.Fatalf("assign a colleague: %+v", d.Issue)
	}
	d = decodePlatformDetail(t, f.asOperator(t, http.MethodPatch, platformPath(issue.ID, ""), map[string]any{"assignee_id": nil}))
	if d.Issue.AssigneeID != nil {
		t.Fatalf("unassign: %+v", d.Issue)
	}
	last := d.Comments[len(d.Comments)-1].Change
	if last == nil || last.Field != "assignee" || last.From == nil || *last.From != second.Name || last.To != nil {
		t.Errorf("unassign entry = %+v", last)
	}

	var team struct{ Data []issues.Assignable }
	testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues/assignees", nil), &team)
	var emails []string
	for _, a := range team.Data {
		emails = append(emails, a.Email)
	}
	want := []string{operatorEmail, secondOperator}
	slices.Sort(emails)
	slices.Sort(want)
	if !slices.Equal(emails, want) {
		t.Errorf("support team = %v", emails)
	}
}

// The operators' list filters by priority, tag and assignee (with the
// unassigned ones as "none"), and both lists search the title and the
// description, wildcards taken literally, within what the caller may see.
func TestIntegrationIssue_SearchAndSupportFilters(t *testing.T) {
	f := newFixture(t)
	export := f.report(t, "Export loses tables")
	percent := f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, map[string]string{
		"kind": "enhancement", "title": "Show 100% matches first", "description": "Sort by score_value."})
	var pct struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, percent, &pct)
	other := f.asMember(t, "/api/issues", f.outsider.ID, f.other.ID, bugFields("Export elsewhere"))
	var out struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, other, &out)

	f.asOperator(t, http.MethodPatch, platformPath(export.ID, ""), map[string]any{"priority": "high", "tags": []string{"mvp"}, "assignee_id": f.operator.ID})
	f.asOperator(t, http.MethodPatch, platformPath(out.Data.ID, ""), map[string]any{"priority": "low", "tags": []string{"buyer", "mvp"}})

	ids := func(rec *httptest.ResponseRecorder) []int64 {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		var page struct{ Data []issues.IssueView }
		testutil.DecodeJSON(t, rec, &page)
		var got []int64
		for _, i := range page.Data {
			got = append(got, i.ID)
		}
		slices.Sort(got)
		return got
	}
	both := []int64{export.ID, out.Data.ID}
	slices.Sort(both)
	member := func(path string) *httptest.ResponseRecorder { return f.get(t, path, f.reporter.ID, f.tenant.ID) }
	operator := func(path string) *httptest.ResponseRecorder { return f.asOperator(t, http.MethodGet, path, nil) }
	cases := map[string]struct {
		as   func(string) *httptest.ResponseRecorder
		path string
		want []int64
	}{
		"member search, own org only":  {member, "/api/issues?q=EXPORT", []int64{export.ID}},
		"member search in description": {member, "/api/issues?q=score_v", []int64{pct.Data.ID}},
		"percent is literal":           {member, "/api/issues?q=0%25%20m", []int64{pct.Data.ID}},
		"underscore is literal":        {member, "/api/issues?q=e_v", []int64{pct.Data.ID}},
		"percent is no wildcard":       {member, "/api/issues?q=w%25m", nil},
		"underscore is no wildcard":    {member, "/api/issues?q=y_s", nil},
		"platform search, every org":   {operator, "/api/platform/issues?q=export", both},
		"platform search is literal":   {operator, "/api/platform/issues?q=w%25m", nil},
		"priority":                     {operator, "/api/platform/issues?priority=high", []int64{export.ID}},
		"tag, any of":                  {operator, "/api/platform/issues?tag=buyer,nothing", []int64{out.Data.ID}},
		"shared tag":                   {operator, "/api/platform/issues?tag=mvp", both},
		"assignee":                     {operator, "/api/platform/issues?assignee=" + itoa(f.operator.ID), []int64{export.ID}},
		"unassigned":                   {operator, "/api/platform/issues?assignee=none&q=export", []int64{out.Data.ID}},
		"assignee or unassigned":       {operator, "/api/platform/issues?assignee=none," + itoa(f.operator.ID) + "&q=export", both},
	}
	// Each order has its own query: both must filter alike.
	for _, order := range []string{"", "&sort=updated"} {
		for name, c := range cases {
			if got := ids(c.as(c.path + order)); !slices.Equal(got, c.want) {
				t.Errorf("%s%s: got %v, want %v", name, order, got, c.want)
			}
		}
	}
	// The member list takes none of the operators' filters.
	if got := ids(f.get(t, "/api/issues?tag=buyer&priority=low", f.reporter.ID, f.tenant.ID)); len(got) != 2 {
		t.Errorf("member list applied support filters: %v", got)
	}
	for _, path := range []string{"/api/platform/issues?priority=urgent", "/api/platform/issues?assignee=x", "/api/platform/issues?q=" + strings.Repeat("a", 201)} {
		if rec := f.asOperator(t, http.MethodGet, path, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	var tags struct{ Data []string }
	testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues/tags", nil), &tags)
	if !slices.Equal(tags.Data, []string{"buyer", "mvp"}) {
		t.Errorf("tags in use = %v", tags.Data)
	}
}
