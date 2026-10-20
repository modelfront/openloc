package issues_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/issues"
	"openloc/internal/notification"
	"openloc/internal/testutil"
)

// importService is the service the import command builds: the fixture's
// database and storage, an outbox to prove nothing is sent.
func (f *fixture) importService() (*issues.Service, *notification.Outbox) {
	outbox := notification.NewOutbox(10)
	sender := &notification.RecordingSender{Next: &notification.ConsoleSender{}, Outbox: outbox}
	notifier := notification.NewService(db.New(f.pool), sender)
	return issues.NewService(f.pool, seekableOnly{f.store}, notifier, sender, []string{operatorEmail}, "http://app.example.com"), outbox
}

// An imported issue keeps its status, priority, tags, assignee, comments,
// internal notes and files; nobody is told; a second run of the same rows
// creates nothing at all — no issue, entry, file or stored blob.
func TestIntegrationIssue_ImportTwiceCreatesNothing(t *testing.T) {
	f := newFixture(t)
	svc, outbox := f.importService()
	reporter := issues.Member{TenantID: f.tenant.ID, UserID: f.reporter.ID, Role: "linguist"}
	rows := []issues.ImportedIssue{
		{
			Key: "Sheet A!2", Kind: "bug", Severity: "blocker", Area: "editor", Title: "Invented: ruler jumps",
			Description: "Invented row for the import test.", Status: "in_progress", Priority: "high",
			Tags: []string{"mvp", "blocks-testing"}, AssigneeID: &f.operator.ID,
			Comments: []issues.ImportedComment{
				{Body: "Happens on every second segment."},
				{Body: "Invented engineer note.", Internal: true, AuthorID: &f.operator.ID},
			},
			Files: []issues.Upload{issues.BytesUpload("shot.png", pngBytes)},
		},
		{
			Key: "Sheet A!3", Kind: "enhancement", Area: "tm", Title: "Invented: show the match score",
			Description: "Invented row.", Status: "resolved", Confirmed: true, Priority: "low",
			Comments: []issues.ImportedComment{{Body: "Unowned invented note.", Internal: true}},
		},
		{
			Key: "Sheet B", Kind: "feature_request", Area: "other", Title: "Invented spec sheet",
			Description: "Invented spec.", Status: "needs_info",
		},
	}
	run := func() int {
		created := 0
		for _, r := range rows {
			ok, err := svc.Import(context.Background(), reporter, r)
			if err != nil {
				t.Fatalf("import %s: %v", r.Key, err)
			}
			if ok {
				created++
			}
		}
		return created
	}
	if n := run(); n != 3 {
		t.Fatalf("first run created %d", n)
	}
	counts := func() (issueCount, entries, files, blobs int) {
		t.Helper()
		ctx := context.Background()
		for _, q := range []struct {
			sql string
			out *int
		}{
			{"SELECT count(*) FROM issues", &issueCount},
			{"SELECT count(*) FROM issue_comments", &entries},
			{"SELECT count(*) FROM issue_attachments", &files},
		} {
			if err := f.pool.QueryRow(ctx, q.sql).Scan(q.out); err != nil {
				t.Fatal(err)
			}
		}
		return issueCount, entries, files, f.store.Len()
	}
	i1, e1, f1, b1 := counts()
	if i1 != 3 || e1 != 3 || f1 != 1 || b1 != 1 {
		t.Fatalf("after the first run: %d issues, %d entries, %d files, %d blobs", i1, e1, f1, b1)
	}
	if n := run(); n != 0 {
		t.Errorf("second run created %d", n)
	}
	if i2, e2, f2, b2 := counts(); i2 != i1 || e2 != e1 || f2 != f1 || b2 != b1 {
		t.Errorf("second run changed counts: %d/%d/%d/%d issues/entries/files/blobs", i2, e2, f2, b2)
	}
	for _, to := range []string{operatorEmail, "rep@test.com", "col@test.com"} {
		if got := outbox.For(to); len(got) != 0 {
			t.Errorf("the import emailed %s: %v", to, got)
		}
	}
	// The resolved row rings no bell either, the org managers' included.
	var bells int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM notifications").Scan(&bells); err != nil || bells != 0 {
		t.Errorf("notifications after the import: %d (%v)", bells, err)
	}

	// The org sees the imported issue as filed, without the internal note.
	var list struct{ Data []issues.IssueView }
	rec := f.get(t, "/api/issues?status=", f.reporter.ID, f.tenant.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	testutil.DecodeJSON(t, rec, &list)
	byTitle := map[string]issues.IssueView{}
	for _, i := range list.Data {
		byTitle[i.Title] = i
	}
	ruler := byTitle["Invented: ruler jumps"]
	if ruler.Status != "in_progress" || ruler.Assignee == nil || ruler.LastActivityBy != "reporter" ||
		ruler.Context == nil || !strings.Contains(string(ruler.Context), `"via":"Import"`) {
		t.Errorf("imported bug = %+v", ruler)
	}
	if s := byTitle["Invented: show the match score"]; s.Status != "resolved" || s.ConfirmedAt == nil || s.ResolvedAt == nil {
		t.Errorf("imported resolved = %+v", s)
	}
	if s := byTitle["Invented spec sheet"]; s.Status != "needs_info" || s.LastActivityBy != "operator" {
		t.Errorf("imported spec = %+v", s)
	}
	d := decodeDetail(t, f.get(t, issuePath(ruler.ID, ""), f.reporter.ID, f.tenant.ID))
	if len(d.Comments) != 1 || *d.Comments[0].Body != "Happens on every second segment." || len(d.Issue.Attachments) != 1 {
		t.Errorf("org view of the imported bug: %+v", d)
	}
	op := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(ruler.ID, ""), nil))
	if len(op.Comments) != 2 || !op.Comments[1].Internal || op.Issue.ImportKey == nil || *op.Issue.ImportKey != "Sheet A!2" ||
		op.Issue.Priority == nil || *op.Issue.Priority != "high" || len(op.Issue.Tags) != 2 {
		t.Errorf("operator view of the imported bug: %+v %+v", op.Issue, op.Comments)
	}
}

// The import refuses an assignee or note author outside the support team, a
// confirmed issue that is not resolved, and a duplicate, before it stores
// anything.
func TestIntegrationIssue_ImportRefusesWhatCannotBe(t *testing.T) {
	f := newFixture(t)
	svc, _ := f.importService()
	reporter := issues.Member{TenantID: f.tenant.ID, UserID: f.reporter.ID, Role: "linguist"}
	base := issues.ImportedIssue{Key: "K!1", Kind: "enhancement", Area: "other", Title: "Invented", Description: "Invented.", Status: "open"}
	for name, mutate := range map[string]func(*issues.ImportedIssue){
		"assignee outside the team": func(i *issues.ImportedIssue) { i.AssigneeID = &f.colleague.ID },
		"note author outside": func(i *issues.ImportedIssue) {
			i.Comments = []issues.ImportedComment{{Body: "x", Internal: true, AuthorID: &f.colleague.ID}}
		},
		"confirmed but open": func(i *issues.ImportedIssue) { i.Confirmed = true },
		"duplicate":          func(i *issues.ImportedIssue) { i.Status = "duplicate" },
		"no key":             func(i *issues.ImportedIssue) { i.Key = "" },
		"unknown priority":   func(i *issues.ImportedIssue) { i.Priority = "urgent" },
	} {
		in := base
		mutate(&in)
		if ok, err := svc.Import(context.Background(), reporter, in); err == nil || ok {
			t.Errorf("%s: imported (%v, %v)", name, ok, err)
		}
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM issues").Scan(&n); err != nil || n != 0 {
		t.Errorf("issues after refusals: %d (%v)", n, err)
	}
	// The row each case changed is valid as it stands.
	if ok, err := svc.Import(context.Background(), reporter, base); !ok || err != nil {
		t.Errorf("the unchanged row: (%v, %v)", ok, err)
	}
}
