package issues_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"openloc/internal/auth"
	"openloc/internal/issues"
	"openloc/internal/notification"
	"openloc/internal/testutil"
)

// Only a platform admin, acting as themselves, reaches the operator routes.
func TestIntegrationIssue_PlatformGate(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Gate check")

	if rec := f.get(t, "/api/platform/issues", f.colleague.ID, f.tenant.ID); rec.Code != http.StatusForbidden {
		t.Errorf("org owner: %d", rec.Code)
	}
	imp, err := auth.GenerateImpersonationToken(f.operator.ID, f.tenant.ID, "owner", testutil.TestJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodGet, "/api/platform/issues", nil, imp); rec.Code != http.StatusForbidden {
		t.Errorf("impersonating operator: %d", rec.Code)
	}

	rec := f.asOperator(t, http.MethodGet, "/api/platform/issues", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator: %d %s", rec.Code, rec.Body.String())
	}
	var list struct{ Data []issues.IssueView }
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 1 || list.Data[0].ID != issue.ID || list.Data[0].OrgName != "Acme Org" || list.Data[0].ReporterEmail != "rep@test.com" {
		t.Errorf("list = %+v", list.Data)
	}
	testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues?status=resolved", nil), &list)
	if len(list.Data) != 0 {
		t.Errorf("status filter: %+v", list.Data)
	}
	if rec := f.asOperator(t, http.MethodGet, "/api/platform/issues?status=bogus", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown status filter: %d", rec.Code)
	}
}

// The operator asks (needs info), the reporter answers (open again), the
// operator resolves, the reporter disagrees (reopened). Each side hears about
// the other's move.
func TestIntegrationIssue_Conversation(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Export does nothing")

	rec := f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"),
		map[string]string{"status": "needs_info", "note": "Which file format?"})
	if rec.Code != http.StatusOK {
		t.Fatalf("needs info: %d %s", rec.Code, rec.Body.String())
	}
	d := decodeDetail(t, f.get(t, issuePath(issue.ID, ""), f.reporter.ID, f.tenant.ID))
	if d.Issue.Status != issues.StatusNeedsInfo || d.Issue.LastActivityBy != "operator" || len(d.Comments) != 1 ||
		*d.Comments[0].Body != "Which file format?" || *d.Comments[0].Status != issues.StatusNeedsInfo ||
		d.Comments[0].AuthorSide != "operator" {
		t.Fatalf("after needs info: %+v", d)
	}
	if s := subjects(f.outbox, "rep@test.com"); len(s) != 1 || s[0] != fmt.Sprintf("Issue #%d: Export does nothing", issue.ID) {
		t.Errorf("reporter emails = %v", s)
	}
	assertIssueNotification(t, f, issue.ID, 1)

	d = decodeDetail(t, f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID,
		map[string]string{"body": "A DOCX file."}, png("answer.png")))
	last := d.Comments[len(d.Comments)-1]
	if d.Issue.Status != issues.StatusOpen || d.Issue.LastActivityBy != "reporter" || last.Status == nil ||
		*last.Status != issues.StatusOpen || len(last.Attachments) != 1 || last.Attachments[0].Filename != "answer.png" {
		t.Fatalf("after the answer: %+v", d)
	}
	if s := subjects(f.outbox, operatorEmail); len(s) != 2 || s[0] != fmt.Sprintf("Issue #%d: Export does nothing", issue.ID) {
		t.Errorf("operator emails (newest first) = %v", s)
	}

	d = decodeDetail(t, f.operatorMultipart(t, platformPath(issue.ID, "/comments"), map[string]string{"body": "Thanks, looking."}))
	if d.Issue.Status != issues.StatusOpen || d.Comments[len(d.Comments)-1].Status != nil {
		t.Errorf("an operator comment changed the status: %+v", d.Issue)
	}
	assertIssueNotification(t, f, issue.ID, 2)

	d = decodeDetail(t, f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"), map[string]string{"status": "resolved"}))
	if d.Issue.Status != issues.StatusResolved || d.Issue.ResolvedAt == nil {
		t.Fatalf("resolve: %+v", d.Issue)
	}
	d = decodeDetail(t, f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID,
		map[string]string{"body": "Still broken."}))
	if d.Issue.Status != issues.StatusOpen || d.Issue.ResolvedAt != nil {
		t.Errorf("reopen: %+v", d.Issue)
	}
}

// Setting the current status again needs a comment; an unknown status is refused.
func TestIntegrationIssue_StatusChecks(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Status checks")
	if rec := f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"), map[string]string{"status": "done"}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown status: %d", rec.Code)
	}
	if rec := f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"), map[string]string{"status": "open"}); rec.Code != http.StatusBadRequest {
		t.Errorf("same status, no comment: %d", rec.Code)
	}
	d := decodeDetail(t, f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"),
		map[string]string{"status": "open", "note": "Still open, checking."}))
	if d.Issue.Status != issues.StatusOpen || len(d.Comments) != 1 || d.Comments[0].Status != nil {
		t.Errorf("same status with a comment: %+v", d)
	}
	if rec := f.asOperator(t, http.MethodPost, platformPath(999999, "/status"), map[string]string{"status": "resolved"}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown issue: %d", rec.Code)
	}
}

// A reporter who muted issue emails still gets the bell.
func TestIntegrationIssue_MutedEmailKeepsTheBell(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Muted")
	off := false
	if rec := testutil.DoRequest(t, f.router, http.MethodPut, "/api/notification-preferences",
		notification.PreferencesPatch{EmailIssueUpdates: &off}, f.reporter.ID, f.tenant.ID); rec.Code != http.StatusOK {
		t.Fatalf("mute: %d %s", rec.Code, rec.Body.String())
	}
	f.operatorMultipart(t, platformPath(issue.ID, "/comments"), map[string]string{"body": "On it."})
	if s := subjects(f.outbox, "rep@test.com"); len(s) != 0 {
		t.Errorf("muted reporter got emails: %v", s)
	}
	assertIssueNotification(t, f, issue.ID, 1)
}

// assertIssueNotification checks the reporter's bell holds want issue notifications for the issue.
func assertIssueNotification(t *testing.T, f *fixture, issueID int64, want int) {
	t.Helper()
	var page struct {
		Data []struct {
			EventType     string `json:"event_type"`
			ReferenceType string `json:"reference_type"`
			ReferenceID   int64  `json:"reference_id"`
		}
	}
	testutil.DecodeJSON(t, f.get(t, "/api/notifications", f.reporter.ID, f.tenant.ID), &page)
	got := 0
	for _, n := range page.Data {
		if n.EventType == "issue_updated" && n.ReferenceType == "issue" && n.ReferenceID == issueID {
			got++
		}
	}
	if got != want {
		t.Errorf("issue notifications = %d, want %d (%+v)", got, want, page.Data)
	}
}

// Each filter narrows the operators' list; a severity matches bugs only, and an
// unknown value is refused.
func TestIntegrationIssue_PlatformFilters(t *testing.T) {
	f := newFixture(t)
	minor := f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, map[string]string{
		"kind": "bug", "severity": "minor", "area": "tm", "title": "Minor TM bug", "description": "x"})
	feature := f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, map[string]string{
		"kind": "feature_request", "area": "tm", "title": "TM feature", "description": "x"})
	blocker := f.report(t, "Editor blocker") // bug, major, editor
	ids := map[string]int64{}
	for name, rec := range map[string]*httptest.ResponseRecorder{"minor": minor, "feature": feature} {
		var env struct{ Data issues.IssueView }
		testutil.DecodeJSON(t, rec, &env)
		ids[name] = env.Data.ID
	}
	ids["major"] = blocker.ID

	for query, want := range map[string][]int64{
		"":                                  {ids["major"], ids["feature"], ids["minor"]},
		"?kind=bug":                         {ids["major"], ids["minor"]},
		"?kind=feature_request":             {ids["feature"]},
		"?area=tm":                          {ids["feature"], ids["minor"]},
		"?severity=minor,critical":          {ids["minor"]},
		"?area=tm&severity=major":           {},
		"?kind=bug,enhancement&area=editor": {ids["major"]},
	} {
		t.Run(query, func(t *testing.T) {
			var list struct{ Data []issues.IssueView }
			testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues"+query, nil), &list)
			got := []int64{}
			for _, i := range list.Data {
				got = append(got, i.ID)
			}
			if !slices.Equal(got, want) {
				t.Errorf("ids = %v, want %v", got, want)
			}
		})
	}
	for _, query := range []string{"?kind=question", "?area=billing", "?severity=urgent"} {
		if rec := f.asOperator(t, http.MethodGet, "/api/platform/issues"+query, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", query, rec.Code)
		}
	}
}

// Operators filter by organization and list the orgs that have issues; nobody
// else reaches that list.
func TestIntegrationIssue_PlatformOrgFilter(t *testing.T) {
	f := newFixture(t)
	acme := f.report(t, "Acme issue")
	rec := f.asMember(t, "/api/issues", f.outsider.ID, f.other.ID, bugFields("Other issue"))
	var other struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, rec, &other)

	var orgs struct{ Data []issues.IssueOrg }
	testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues/orgs", nil), &orgs)
	if len(orgs.Data) != 2 || orgs.Data[0].Name != "Acme Org" || orgs.Data[1].Name != "Other Org" {
		t.Errorf("orgs = %+v", orgs.Data)
	}
	for query, want := range map[string][]int64{
		"?org=" + itoa(f.other.ID):                           {other.Data.ID},
		"?org=" + itoa(f.tenant.ID):                          {acme.ID},
		"?org=" + itoa(f.tenant.ID) + "," + itoa(f.other.ID): {other.Data.ID, acme.ID},
		"?org=" + itoa(f.other.ID) + "&kind=enhancement":     {},
	} {
		var list struct{ Data []issues.IssueView }
		testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues"+query, nil), &list)
		got := []int64{}
		for _, i := range list.Data {
			got = append(got, i.ID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: ids = %v, want %v", query, got, want)
		}
	}
	if rec := f.asOperator(t, http.MethodGet, "/api/platform/issues?org=acme", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("non-numeric org: %d", rec.Code)
	}
	if rec := f.get(t, "/api/platform/issues/orgs", f.colleague.ID, f.tenant.ID); rec.Code != http.StatusForbidden {
		t.Errorf("org owner on the org list: %d", rec.Code)
	}
}

// Both lists are by last update, newest first by default, oldest first with
// sort=updated, and a page boundary keeps that order. A reply moves an issue
// up; an unknown sort is refused.
func TestIntegrationIssue_SortByUpdated(t *testing.T) {
	f := newFixture(t)
	first := f.report(t, "First")
	second := f.report(t, "Second")
	third := f.report(t, "Third")
	f.operatorMultipart(t, platformPath(first.ID, "/comments"), map[string]string{"body": "Looking."})

	// walk reads the whole list one issue per page, following the cursor.
	walk := func(path string, get func(string) *httptest.ResponseRecorder) []int64 {
		t.Helper()
		var ids []int64
		cursor := ""
		for range 10 {
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			var page struct {
				Data    []issues.IssueView
				Cursor  string `json:"cursor"`
				HasMore bool   `json:"has_more"`
			}
			testutil.DecodeJSON(t, get(path+sep+"limit=1&cursor="+cursor), &page)
			for _, i := range page.Data {
				ids = append(ids, i.ID)
			}
			if !page.HasMore {
				return ids
			}
			cursor = page.Cursor
		}
		t.Fatalf("%s: more than 10 pages", path)
		return nil
	}
	asOperator := func(p string) *httptest.ResponseRecorder { return f.asOperator(t, http.MethodGet, p, nil) }
	asReporter := func(p string) *httptest.ResponseRecorder { return f.get(t, p, f.reporter.ID, f.tenant.ID) }

	newest := []int64{first.ID, third.ID, second.ID}
	oldest := []int64{second.ID, third.ID, first.ID}
	for name, c := range map[string]struct {
		path string
		get  func(string) *httptest.ResponseRecorder
		want []int64
	}{
		"operator default":  {"/api/platform/issues", asOperator, newest},
		"operator -updated": {"/api/platform/issues?sort=-updated", asOperator, newest},
		"operator updated":  {"/api/platform/issues?sort=updated", asOperator, oldest},
		"reporter default":  {"/api/issues", asReporter, newest},
		"reporter updated":  {"/api/issues?sort=updated", asReporter, oldest},
	} {
		t.Run(name, func(t *testing.T) {
			if got := walk(c.path, c.get); !slices.Equal(got, c.want) {
				t.Errorf("ids = %v, want %v", got, c.want)
			}
		})
	}
	if rec := f.asOperator(t, http.MethodGet, "/api/platform/issues?sort=title", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown sort: %d", rec.Code)
	}
	if rec := f.get(t, "/api/issues?sort=created", f.reporter.ID, f.tenant.ID); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown sort (member): %d", rec.Code)
	}
}

// The next page starts after the previous page's last (updated, id), so an
// issue that changes between two pages neither repeats the list nor hides the
// rest of it. A malformed cursor is refused.
func TestIntegrationIssue_PagingSurvivesAnUpdate(t *testing.T) {
	type page struct {
		Data    []issues.IssueView
		Cursor  string `json:"cursor"`
		HasMore bool   `json:"has_more"`
	}
	for name, tc := range map[string]struct {
		query string
		// first and rest are indexes into the reported issues A, B, C: the
		// first page (limit 2), and the second page after the first page's
		// last issue changed.
		first, rest []int
	}{
		"newest first": {"", []int{2, 1}, []int{0}},
		// The reply makes B the most recently updated, so it rightly comes
		// again at the end; C is not skipped.
		"oldest first": {"&sort=updated", []int{0, 1}, []int{2, 1}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			reported := []int64{f.report(t, "A").ID, f.report(t, "B").ID, f.report(t, "C").ID}
			ids := func(idx []int) []int64 {
				out := []int64{}
				for _, i := range idx {
					out = append(out, reported[i])
				}
				return out
			}
			first, rest := ids(tc.first), ids(tc.rest)
			var p1 page
			testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues?status=&limit=2"+tc.query, nil), &p1)
			got := []int64{}
			for _, i := range p1.Data {
				got = append(got, i.ID)
			}
			if !slices.Equal(got, first) || !p1.HasMore || p1.Cursor == "" {
				t.Fatalf("first page = %v (has_more %v, cursor %q), want %v", got, p1.HasMore, p1.Cursor, first)
			}
			// The last issue of the page gets a reply before "Load more".
			f.operatorMultipart(t, platformPath(first[len(first)-1], "/comments"), map[string]string{"body": "Bump."})

			var p2 page
			testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues?status=&limit=2"+tc.query+"&cursor="+p1.Cursor, nil), &p2)
			got = []int64{}
			for _, i := range p2.Data {
				got = append(got, i.ID)
			}
			if !slices.Equal(got, rest) {
				t.Errorf("second page = %v, want %v", got, rest)
			}
		})
	}
	f := newFixture(t)
	for _, bad := range []string{"abc", "12", "12.x", "1.0", "1.-5"} {
		if rec := f.asOperator(t, http.MethodGet, "/api/platform/issues?cursor="+bad, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("cursor %q: %d", bad, rec.Code)
		}
		if rec := f.get(t, "/api/issues?cursor="+bad, f.reporter.ID, f.tenant.ID); rec.Code != http.StatusBadRequest {
			t.Errorf("member cursor %q: %d", bad, rec.Code)
		}
	}
}

// Asking for more information needs the question: the server refuses
// "Needs info" without a note, as the form does.
func TestIntegrationIssue_NeedsInfoNeedsAQuestion(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Unclear")
	if rec := f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"), map[string]string{"status": "needs_info"}); rec.Code != http.StatusBadRequest {
		t.Errorf("needs info without a question: %d", rec.Code)
	}
	if s := subjects(f.outbox, "rep@test.com"); len(s) != 0 {
		t.Errorf("the reporter was notified: %v", s)
	}
	d := decodeDetail(t, f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"),
		map[string]string{"status": "needs_info", "note": "Which file?"}))
	if d.Issue.Status != issues.StatusNeedsInfo {
		t.Errorf("with a question: %+v", d.Issue)
	}
}
