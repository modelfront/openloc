package issues_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/emaillayout"
	"openloc/internal/notification"
)

// Every issue email goes through the shared layout, its button opens the
// issue on the right side (console for operators, the app for the reporter),
// and the title the reporter typed is escaped.
func TestIntegrationIssue_EmailsUseSharedLayout(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Export <b>does</b> nothing")
	console := fmt.Sprintf(`<a href="http://localhost:5173/platform/issues/%d" style="display: inline-block;`, issue.ID)

	assertEmail(t, "new issue", newestBody(t, f.outbox, operatorEmail),
		emaillayout.HeaderMarker, ">Organization</td>", ">Acme Org</td>", "rep@test.com", ">Description</div>",
		console, ">Open the issue</a>")

	rec := f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"),
		map[string]string{"status": "needs_info", "note": "Which file format?"})
	if rec.Code != http.StatusOK {
		t.Fatalf("needs info: %d %s", rec.Code, rec.Body.String())
	}
	assertEmail(t, "issue update", newestBody(t, f.outbox, "rep@test.com"),
		emaillayout.HeaderMarker, ">Comment</div>", "Which file format?",
		fmt.Sprintf(`<a href="http://localhost:5173/issues/%d" style="display: inline-block;`, issue.ID), ">View the issue</a>")

	rec = f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID, map[string]string{"body": "A DOCX file."})
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body.String())
	}
	assertEmail(t, "reply", newestBody(t, f.outbox, operatorEmail),
		emaillayout.HeaderMarker, ">Reply</div>", "A DOCX file.", ">Reporter</td>", console, ">Open the issue</a>")
}

func newestBody(t *testing.T, o *notification.Outbox, to string) string {
	t.Helper()
	emails := o.For(to)
	if len(emails) == 0 {
		t.Fatalf("no email to %s", to)
	}
	return emails[0].Body
}

func assertEmail(t *testing.T, name, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s email missing %q:\n%s", name, want, body)
		}
	}
	if strings.Contains(body, "<b>does</b>") {
		t.Errorf("%s email does not escape the title:\n%s", name, body)
	}
}

// Every email about one issue, to either side, has the same subject and the
// same thread, and the new-issue email starts the thread, so mail apps keep
// the issue's emails in one conversation.
func TestIntegrationIssue_EmailsThreadPerIssue(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Export does nothing")
	f.report(t, "Search is slow")

	rec := f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"),
		map[string]string{"status": "needs_info", "note": "Which file format?"})
	if rec.Code != http.StatusOK {
		t.Fatalf("needs info: %d %s", rec.Code, rec.Body.String())
	}
	rec = f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID, map[string]string{"body": "A DOCX file."})
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body.String())
	}
	decodeDetail(t, f.operatorMultipart(t, platformPath(issue.ID, "/comments"), map[string]string{"body": "Thanks, looking."}))

	subject := fmt.Sprintf("Issue #%d: Export does nothing", issue.ID)
	var thread string
	check := func(to string, e notification.OutboxEmail) {
		t.Helper()
		if e.Subject != subject {
			t.Errorf("email to %s: subject %q, want %q", to, e.Subject, subject)
		}
		if thread == "" {
			thread = e.ThreadID
		}
		if e.ThreadID == "" || e.ThreadID != thread {
			t.Errorf("email to %s %q: thread %q, want one thread %q", to, e.Subject, e.ThreadID, thread)
		}
	}
	var ops []notification.OutboxEmail
	for _, e := range f.outbox.For(operatorEmail) {
		if strings.Contains(e.Subject, "Search is slow") {
			if e.ThreadID == "" || e.ThreadID == thread {
				t.Errorf("another issue's email %q has thread %q", e.Subject, e.ThreadID)
			}
			continue
		}
		ops = append(ops, e)
	}
	if len(ops) != 2 {
		t.Fatalf("operator emails about the issue = %d, want 2 (new issue, reply)", len(ops))
	}
	for i := len(ops) - 1; i >= 0; i-- { // oldest first: the new issue sets the thread
		check(operatorEmail, ops[i])
	}
	if !ops[1].ThreadRoot || ops[0].ThreadRoot {
		t.Errorf("roots: new issue %v, reply %v; want only the new issue", ops[1].ThreadRoot, ops[0].ThreadRoot)
	}
	reporter := f.outbox.For("rep@test.com")
	if len(reporter) != 2 {
		t.Fatalf("reporter emails = %d, want 2", len(reporter))
	}
	for _, e := range reporter {
		check("rep@test.com", e)
		if e.ThreadRoot {
			t.Errorf("a reporter email claims to start the thread: %q", e.Subject)
		}
	}
}
