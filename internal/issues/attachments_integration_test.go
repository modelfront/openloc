package issues_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/file"
	"openloc/internal/issues"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// An image is shown inline; any other file, an HTML page included, is a
// download of opaque bytes that the browser never renders.
func TestIntegrationIssue_AttachmentsServeSafely(t *testing.T) {
	f := newFixture(t)
	page := []byte("<!doctype html><script>alert(1)</script>")
	issue := f.report(t, "Files", png("screen.png"), upload{"notes.txt", []byte("steps to reproduce")},
		upload{"page.html", page}, upload{"../../etc/passwd", []byte("root")})
	if len(issue.Attachments) != 4 {
		t.Fatalf("attachments = %+v", issue.Attachments)
	}
	get := func(i int) *bytes.Buffer {
		t.Helper()
		rec := f.get(t, issuePath(issue.ID, "/attachments/"+itoa(issue.Attachments[i].ID)), f.reporter.ID, f.tenant.ID)
		if rec.Code != http.StatusOK {
			t.Fatalf("attachment %d: %d", i, rec.Code)
		}
		h := rec.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("attachment %d headers = %v", i, h)
		}
		return rec.Body
	}

	img := f.get(t, issuePath(issue.ID, "/attachments/"+itoa(issue.Attachments[0].ID)), f.reporter.ID, f.tenant.ID)
	if img.Header().Get("Content-Type") != "image/png" || img.Header().Get("Content-Disposition") != "inline" ||
		!bytes.Equal(get(0).Bytes(), pngBytes) {
		t.Errorf("image: %v", img.Header())
	}
	for i, name := range map[int]string{1: "notes.txt", 2: "page.html"} {
		rec := f.get(t, issuePath(issue.ID, "/attachments/"+itoa(issue.Attachments[i].ID)), f.reporter.ID, f.tenant.ID)
		if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("%s content type = %q", name, ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="`+name+`"`) {
			t.Errorf("%s disposition = %q", name, cd)
		}
	}
	if !bytes.Equal(get(2).Bytes(), page) {
		t.Error("the HTML file's bytes changed")
	}
	if got := issue.Attachments[3].Filename; got != "passwd" {
		t.Errorf("path in a filename kept: %q", got)
	}
}

// A comment's files belong to its issue: another issue's route does not serve
// them, and files alone make a comment.
func TestIntegrationIssue_CommentFilesBelongToTheirIssue(t *testing.T) {
	f := newFixture(t)
	a := f.report(t, "Issue A")
	b := f.report(t, "Issue B")
	d := decodeDetail(t, f.asMember(t, issuePath(a.ID, "/comments"), f.reporter.ID, f.tenant.ID, nil,
		png("one.png"), upload{"two.log", []byte("log line")}))
	if len(d.Comments) != 1 || d.Comments[0].Body != nil || len(d.Comments[0].Attachments) != 2 || len(d.Issue.Attachments) != 0 {
		t.Fatalf("detail = %+v", d)
	}
	id := itoa(d.Comments[0].Attachments[1].ID)
	if rec := f.get(t, issuePath(a.ID, "/attachments/"+id), f.reporter.ID, f.tenant.ID); rec.Code != http.StatusOK {
		t.Errorf("own issue: %d", rec.Code)
	}
	if rec := f.get(t, issuePath(b.ID, "/attachments/"+id), f.reporter.ID, f.tenant.ID); rec.Code != http.StatusNotFound {
		t.Errorf("other issue: %d", rec.Code)
	}
	if rec := f.asOperator(t, http.MethodGet, platformPath(b.ID, "/attachments/"+id), nil); rec.Code != http.StatusNotFound {
		t.Errorf("other issue, operator: %d", rec.Code)
	}
}

// The operator reads the reporter's files and sends their own, which the
// reporter reads in turn.
func TestIntegrationIssue_OperatorFiles(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Both ways", upload{"report.csv", []byte("a,b\n1,2\n")})
	rec := f.asOperator(t, http.MethodGet, platformPath(issue.ID, "/attachments/"+itoa(issue.Attachments[0].ID)), nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "a,b\n1,2\n" {
		t.Errorf("operator reads the reporter's file: %d %q", rec.Code, rec.Body.String())
	}
	d := decodeDetail(t, f.operatorMultipart(t, platformPath(issue.ID, "/comments"),
		map[string]string{"body": "Try this file."}, upload{"fixed.docx", []byte("PK\x03\x04docx")}))
	files := d.Comments[len(d.Comments)-1].Attachments
	if len(files) != 1 {
		t.Fatalf("operator comment files = %+v", files)
	}
	rec = f.get(t, issuePath(issue.ID, "/attachments/"+itoa(files[0].ID)), f.reporter.ID, f.tenant.ID)
	if rec.Code != http.StatusOK || rec.Body.String() != "PK\x03\x04docx" {
		t.Errorf("reporter reads the operator's file: %d", rec.Code)
	}
}

// cancelAfterPut stores the blob, then cancels the request, so the insert fails.
type cancelAfterPut struct {
	*file.MemoryStore
	cancel context.CancelFunc
}

func (c cancelAfterPut) Put(ctx context.Context, key string, body io.Reader) error {
	err := c.MemoryStore.Put(ctx, key, body)
	c.cancel()
	return err
}

// An issue that is not saved leaves none of its files behind.
func TestIntegrationIssue_UnsavedIssueLeavesNoBlob(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	store := cancelAfterPut{MemoryStore: file.NewMemoryStore(), cancel: cancel}
	svc := issues.NewService(f.pool, store, nil, nil, []string{operatorEmail}, "http://localhost")
	_, err := svc.Create(ctx, issues.Member{TenantID: f.tenant.ID, UserID: f.reporter.ID, Role: "linguist"}, issues.NewIssue{
		Kind: "bug", Severity: "major", Title: "Lost", Description: "Never saved",
		Files: []issues.Upload{issues.BytesUpload("a.png", pngBytes), issues.BytesUpload("b.txt", []byte("text"))},
	})
	if err == nil {
		t.Fatal("create succeeded with a cancelled context")
	}
	if store.Len() != 0 {
		t.Errorf("%d blobs left behind", store.Len())
	}
}

// Only bugs carry a severity. An operator corrects severity and area without
// telling the reporter, within the rules of the issue's kind; each change is an
// internal history entry. A field left out of the request stays as it is.
func TestIntegrationIssue_Triage(t *testing.T) {
	f := newFixture(t)
	fields := map[string]string{"kind": "feature_request", "title": "Dark mode", "description": "Please."}
	rec := f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, fields)
	if rec.Code != http.StatusCreated {
		t.Fatalf("feature request: %d %s", rec.Code, rec.Body.String())
	}
	var created struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, rec, &created)
	if created.Data.Severity != nil || created.Data.Area != "other" {
		t.Errorf("feature request = %+v", created.Data)
	}
	bug := f.report(t, "Wrong severity")

	if rec := testutil.DoRequest(t, f.router, http.MethodPatch, platformPath(bug.ID, ""),
		map[string]string{"severity": "blocker", "area": "qa"}, f.colleague.ID, f.tenant.ID); rec.Code != http.StatusForbidden {
		t.Errorf("org owner triage: %d", rec.Code)
	}
	d := decodeDetail(t, f.asOperator(t, http.MethodPatch, platformPath(bug.ID, ""), map[string]string{"severity": "blocker", "area": "qa"}))
	if d.Issue.Severity == nil || *d.Issue.Severity != "blocker" || d.Issue.Area != "qa" || len(d.Comments) != 2 {
		t.Fatalf("after triage: %+v", d)
	}
	for i, want := range []issues.ChangeView{{Field: "severity", From: ptr.To("major"), To: ptr.To("blocker")}, {Field: "area", From: ptr.To("editor"), To: ptr.To("qa")}} {
		c := d.Comments[i]
		if !c.Internal || c.Change == nil || c.Change.Field != want.Field || *c.Change.From != *want.From || *c.Change.To != *want.To {
			t.Errorf("entry %d = %+v %+v, want %+v", i, c, c.Change, want)
		}
	}
	if s := subjects(f.outbox, "rep@test.com"); len(s) != 0 {
		t.Errorf("triage emailed the reporter: %v", s)
	}
	assertIssueNotification(t, f, bug.ID, 0)

	for name, c := range map[string]struct {
		id   int64
		body map[string]string
		want int
	}{
		"bug without severity":              {bug.ID, map[string]string{"severity": "", "area": "qa"}, http.StatusBadRequest},
		"severity on a non-bug":             {created.Data.ID, map[string]string{"severity": "minor"}, http.StatusBadRequest},
		"unknown area":                      {bug.ID, map[string]string{"severity": "minor", "area": "billing"}, http.StatusBadRequest},
		"unknown issue":                     {999999, map[string]string{"severity": "minor"}, http.StatusNotFound},
		"non-bug, area only works":          {created.Data.ID, map[string]string{"area": "editor"}, http.StatusOK},
		"bug, area only keeps the severity": {bug.ID, map[string]string{"area": "editor"}, http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			if rec := f.asOperator(t, http.MethodPatch, platformPath(c.id, ""), c.body); rec.Code != c.want {
				t.Errorf("status %d %s", rec.Code, rec.Body.String())
			}
		})
	}
	if d := decodeDetail(t, f.get(t, issuePath(bug.ID, ""), f.reporter.ID, f.tenant.ID)); d.Issue.Severity == nil || *d.Issue.Severity != "blocker" {
		t.Errorf("an area-only PATCH changed the severity: %v", testutil.Show(d.Issue.Severity))
	}
}

// Deleting an org removes its issues' attachment rows (cascade) and queues
// their files; the sweep deletes the files, and a second run has nothing left.
func TestIntegrationIssue_DeletedOrgFilesArePurged(t *testing.T) {
	f := newFixture(t)
	f.report(t, "With files", png("a.png"), upload{"b.log", []byte("log")})
	keep := f.asMember(t, "/api/issues", f.outsider.ID, f.other.ID, bugFields("Other org"), upload{"c.txt", []byte("kept")})
	if keep.Code != http.StatusCreated {
		t.Fatalf("other org's report: %d", keep.Code)
	}
	if f.store.Len() != 3 {
		t.Fatalf("stored files = %d, want 3", f.store.Len())
	}
	if err := f.q.DeleteTenant(context.Background(), f.tenant.ID); err != nil {
		t.Fatal(err)
	}
	purger := issues.NewAttachmentPurger(f.q, f.store)
	n, err := purger.Purge(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("purge = %d, %v; want 2", n, err)
	}
	if f.store.Len() != 1 {
		t.Errorf("files left = %d, want the other org's 1", f.store.Len())
	}
	if n, err := purger.Purge(context.Background()); err != nil || n != 0 {
		t.Errorf("second purge = %d, %v; want 0", n, err)
	}
}
