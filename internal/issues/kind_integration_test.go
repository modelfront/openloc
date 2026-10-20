package issues_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/issues"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// patchIssue sends an operator PATCH and returns the support team's view.
func (f *fixture) patchIssue(t *testing.T, id int64, body map[string]any) issues.PlatformIssueDetail {
	t.Helper()
	return decodePlatformDetail(t, f.asOperator(t, http.MethodPatch, platformPath(id, ""), body))
}

// kindEntries are the kind and severity changes in a history, in order, as
// "field:from>to" with "-" for no value.
func kindEntries(t *testing.T, comments []issues.CommentView) []string {
	t.Helper()
	val := func(s *string) string {
		if s == nil {
			return "-"
		}
		return *s
	}
	var out []string
	for _, c := range comments {
		if c.Change == nil || (c.Change.Field != "kind" && c.Change.Field != "severity") {
			continue
		}
		if !c.Internal {
			t.Errorf("%s entry is not internal: %+v", c.Change.Field, c)
		}
		out = append(out, c.Change.Field+":"+val(c.Change.From)+">"+val(c.Change.To))
	}
	return out
}

// The support team changes an issue's kind after it is filed. A bug gets a
// severity (Major unless the PATCH names one) and anything else loses it.
// Each change is an internal history entry: the org sees the new kind, but
// no entry, no new last update and no email.
func TestIntegrationIssue_SupportTeamChangesKind(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Export loses tables")

	d := f.patchIssue(t, issue.ID, map[string]any{"kind": "feature_request"})
	if d.Issue.Kind != "feature_request" || d.Issue.Severity != nil {
		t.Fatalf("to a feature request: kind %q, severity %v", d.Issue.Kind, d.Issue.Severity)
	}
	want := []string{"kind:bug>feature_request", "severity:major>-"}
	if got := kindEntries(t, d.Comments); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("history = %v, want %v", got, want)
	}

	// The org sees the new kind and nothing else.
	for _, viewer := range []struct {
		name   string
		userID int64
		role   string
	}{{"reporter", f.reporter.ID, "linguist"}, {"manager", f.colleague.ID, "owner"}} {
		rec := f.getAs(t, issuePath(issue.ID, ""), viewer.userID, f.tenant.ID, viewer.role)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s detail: %d", viewer.name, rec.Code)
		}
		org := decodeDetail(t, rec)
		if org.Issue.Kind != "feature_request" || org.Issue.Severity != nil {
			t.Errorf("%s sees kind %q, severity %v", viewer.name, org.Issue.Kind, org.Issue.Severity)
		}
		if len(org.Comments) != 0 {
			t.Errorf("%s sees internal entries: %+v", viewer.name, org.Comments)
		}
		if !org.Issue.UpdatedAt.Equal(issue.UpdatedAt) {
			t.Errorf("%s: a kind change moved updated_at to %v (filed %v)", viewer.name, org.Issue.UpdatedAt, issue.UpdatedAt)
		}
	}
	if s := subjects(f.outbox, "rep@test.com"); len(s) != 0 {
		t.Errorf("a kind change emailed the reporter: %v", s)
	}
	assertIssueNotification(t, f, issue.ID, 0)

	// The same kind again records nothing.
	if d = f.patchIssue(t, issue.ID, map[string]any{"kind": "feature_request"}); len(kindEntries(t, d.Comments)) != 2 {
		t.Errorf("an unchanged kind added an entry: %v", kindEntries(t, d.Comments))
	}

	// Back to a bug: Major, the report form's default.
	d = f.patchIssue(t, issue.ID, map[string]any{"kind": "bug"})
	if d.Issue.Kind != "bug" || d.Issue.Severity == nil || *d.Issue.Severity != "major" {
		t.Fatalf("to a bug: kind %q, severity %v", d.Issue.Kind, d.Issue.Severity)
	}

	// A severity sent with the kind wins over the default.
	f.patchIssue(t, issue.ID, map[string]any{"kind": "enhancement"})
	d = f.patchIssue(t, issue.ID, map[string]any{"kind": "bug", "severity": "blocker"})
	if d.Issue.Kind != "bug" || d.Issue.Severity == nil || *d.Issue.Severity != "blocker" {
		t.Fatalf("to a blocker bug: kind %q, severity %v", d.Issue.Kind, d.Issue.Severity)
	}
	want = []string{
		"kind:bug>feature_request", "severity:major>-",
		"kind:feature_request>bug", "severity:->major",
		"kind:bug>enhancement", "severity:major>-",
		"kind:enhancement>bug", "severity:->blocker",
	}
	if got := kindEntries(t, d.Comments); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("history = %v, want %v", got, want)
	}

	// Refused: an unknown kind, a severity on anything but a bug. Nothing changes.
	for _, body := range []map[string]any{
		{"kind": "question"},
		{"kind": ""},
		{"kind": nil},
		{"kind": "enhancement", "severity": "minor"},
	} {
		if rec := f.asOperator(t, http.MethodPatch, platformPath(issue.ID, ""), body); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %v: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	d = f.platformGet(t, issue.ID)
	if d.Issue.Kind != "bug" || d.Issue.Severity == nil || *d.Issue.Severity != "blocker" || len(kindEntries(t, d.Comments)) != len(want) {
		t.Errorf("a refused PATCH changed the issue: %+v", d.Issue)
	}
}

func (f *fixture) changeKind(t *testing.T, id, userID, tenantID int64, role string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, http.MethodPatch, issuePath(id, "/kind"), body, userID, tenantID, role)
}

// lastChange is the newest entry of a conversation that changed a field.
func lastChange(comments []issues.CommentView) *issues.CommentView {
	for i := len(comments) - 1; i >= 0; i-- {
		if comments[i].Change != nil {
			return &comments[i]
		}
	}
	return nil
}

// The reporter changes the kind both ways, through the support team's triage:
// to a bug (with the severity they pick, or Major), and back, which clears the
// severity. The kind change is an entry both sides see; the severity it sets
// or clears is internal. Nobody is notified.
func TestIntegrationIssue_ReporterChangesKind(t *testing.T) {
	f := newFixture(t)
	fields := bugFields("Bulk export")
	fields["kind"], fields["severity"] = "feature_request", ""
	issue := f.reportWith(t, fields)

	rec := f.changeKind(t, issue.ID, f.reporter.ID, f.tenant.ID, "linguist", map[string]string{"kind": "bug", "severity": "critical"})
	if rec.Code != http.StatusOK {
		t.Fatalf("to bug: %d %s", rec.Code, rec.Body.String())
	}
	d := decodeDetail(t, rec)
	if d.Issue.Kind != "bug" || ptr.Deref(d.Issue.Severity) != "critical" {
		t.Fatalf("after to bug: kind %s severity %v", d.Issue.Kind, d.Issue.Severity)
	}
	if len(d.Comments) != 1 {
		t.Fatalf("the org sees %d entries, want the kind change only: %+v", len(d.Comments), d.Comments)
	}
	c := d.Comments[0]
	if c.Change == nil || c.Change.Field != "kind" || ptr.Deref(c.Change.From) != "feature_request" ||
		ptr.Deref(c.Change.To) != "bug" || c.AuthorSide != "reporter" || c.Internal {
		t.Fatalf("kind entry = %+v", c)
	}
	want := []string{"kind:feature_request>bug", "severity:->critical"}
	if got := reporterKindEntries(f.platformGet(t, issue.ID).Comments); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("support team's history = %v, want %v", got, want)
	}

	d = decodeDetail(t, f.changeKind(t, issue.ID, f.reporter.ID, f.tenant.ID, "linguist", map[string]string{"kind": "enhancement"}))
	if d.Issue.Kind != "enhancement" || d.Issue.Severity != nil {
		t.Fatalf("after back from bug: kind %s severity %v", d.Issue.Kind, d.Issue.Severity)
	}
	d = decodeDetail(t, f.changeKind(t, issue.ID, f.colleague.ID, f.tenant.ID, "owner", map[string]string{"kind": "bug"}))
	if d.Issue.Kind != "bug" || ptr.Deref(d.Issue.Severity) != "major" {
		t.Fatalf("a manager's change to a bug without a severity: kind %s severity %v, want Major", d.Issue.Kind, d.Issue.Severity)
	}
	want = append(want, "kind:bug>enhancement", "severity:critical>-", "kind:enhancement>bug", "severity:->major")
	if got := reporterKindEntries(f.platformGet(t, issue.ID).Comments); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("support team's history = %v, want %v", got, want)
	}

	for _, to := range []string{"rep@test.com", "col@test.com"} {
		if s := subjects(f.outbox, to); len(s) != 0 {
			t.Errorf("a kind change emailed %s: %v", to, s)
		}
	}
	if s := subjects(f.outbox, operatorEmail); len(s) != 1 {
		t.Errorf("support team emails = %v, want the new-issue email only", s)
	}
	assertIssueNotification(t, f, issue.ID, 0)
}

// reporterKindEntries are the kind and severity changes in the support team's
// history, as "field:from>to", each checked for who may read it: a kind
// change from the reporter's side is visible, a severity change internal.
func reporterKindEntries(comments []issues.CommentView) []string {
	val := func(s *string) string {
		if s == nil {
			return "-"
		}
		return *s
	}
	var out []string
	for _, c := range comments {
		if c.Change == nil || (c.Change.Field != "kind" && c.Change.Field != "severity") {
			continue
		}
		if c.AuthorSide != "reporter" || c.Internal != (c.Change.Field == "severity") {
			out = append(out, "WRONG-VISIBILITY "+c.Change.Field)
			continue
		}
		out = append(out, c.Change.Field+":"+val(c.Change.From)+">"+val(c.Change.To))
	}
	return out
}

// Only a bug has a severity, and only a known one; the same kind or an
// unknown one is refused, and a refusal changes nothing.
func TestIntegrationIssue_KindChangeRefusals(t *testing.T) {
	f := newFixture(t)
	bug := f.report(t, "Export does nothing")
	fields := bugFields("Bulk export")
	fields["kind"], fields["severity"] = "feature_request", ""
	feature := f.reportWith(t, fields)
	for name, c := range map[string]struct {
		id   int64
		body map[string]string
	}{
		"to bug, unknown severity":   {feature.ID, map[string]string{"kind": "bug", "severity": "huge"}},
		"from bug, keeping severity": {bug.ID, map[string]string{"kind": "enhancement", "severity": "major"}},
		"the same kind":              {bug.ID, map[string]string{"kind": "bug", "severity": "minor"}},
		"an unknown kind":            {bug.ID, map[string]string{"kind": "question"}},
		"no kind":                    {bug.ID, map[string]string{}},
	} {
		if rec := f.changeKind(t, c.id, f.reporter.ID, f.tenant.ID, "linguist", c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, rec.Code)
		}
	}
	for _, issue := range []issues.IssueView{bug, feature} {
		d := f.platformGet(t, issue.ID)
		if d.Issue.Kind != issue.Kind || ptr.Deref(d.Issue.Severity) != ptr.Deref(issue.Severity) || len(d.Comments) != 0 {
			t.Errorf("#%d changed by a refusal: kind %s severity %v, %d entries", issue.ID, d.Issue.Kind, d.Issue.Severity, len(d.Comments))
		}
	}
}

// Whoever may comment on the issue may change its kind: the reporter and a
// manager of the org. A colleague who does not see it, and another org, get
// 404.
func TestIntegrationIssue_KindChangeAccess(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Export does nothing")
	for name, c := range map[string]struct {
		userID, tenantID int64
		role             string
	}{
		"a linguist colleague":             {f.peer.ID, f.tenant.ID, "linguist"},
		"another org's owner":              {f.outsider.ID, f.other.ID, "owner"},
		"the reporter's id in another org": {f.reporter.ID, f.other.ID, "owner"},
	} {
		if rec := f.changeKind(t, issue.ID, c.userID, c.tenantID, c.role, map[string]string{"kind": "enhancement"}); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, rec.Code)
		}
	}
	if got := f.platformGet(t, issue.ID).Issue.Kind; got != "bug" {
		t.Fatalf("kind changed by someone who may not: %s", got)
	}
	rec := f.changeKind(t, issue.ID, f.colleague.ID, f.tenant.ID, "owner", map[string]string{"kind": "enhancement"})
	if rec.Code != http.StatusOK || decodeDetail(t, rec).Issue.Kind != "enhancement" {
		t.Errorf("the manager: %d %s", rec.Code, rec.Body.String())
	}
}

// The newer areas are accepted on a report and in the filters.
func TestIntegrationIssue_NewAreas(t *testing.T) {
	f := newFixture(t)
	for _, area := range []string{"projects", "tasks", "chat", "mcp"} {
		fields := bugFields("About " + area)
		fields["area"] = area
		if got := f.reportWith(t, fields).Area; got != area {
			t.Errorf("area %s saved as %s", area, got)
		}
	}
	var page struct{ Data []issues.IssueView }
	testutil.DecodeJSON(t, f.get(t, "/api/issues?area=chat,mcp", f.reporter.ID, f.tenant.ID), &page)
	if len(page.Data) != 2 {
		t.Errorf("filtered by chat and mcp: %d issues, want 2", len(page.Data))
	}
}
