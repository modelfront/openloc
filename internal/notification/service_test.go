package notification

import (
	"html/template"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

func makeEvent(t EventType) Event {
	return Event{
		Type:        t,
		TenantID:    1,
		RecipientID: 10,
		ActorID:     20,
		ActorName:   "Alice",
		JobID:       100,
		ProjectID:   50,
		ProjectName: "Demo Project",
		FileName:    "demo.txt",
		TargetLang:  "es",
		StepName:    "Review",
		Reason:      "Missed terminology",
		DeadlineAt:  time.Date(2026, 4, 15, 14, 30, 0, 0, time.UTC),
	}
}

const testBaseURL = "https://app.example.com"

// renderHTML is e's email markup; a render error fails the test.
func renderHTML(t *testing.T, e Event, baseURL string) string {
	t.Helper()
	email, err := RenderHTML(e, baseURL)
	if err != nil {
		t.Fatalf("RenderHTML(%s): %v", e.Type, err)
	}
	return email.HTML()
}

var allEventTypes = []EventType{
	EventAssignment,
	EventRejection,
	EventCompletion,
	EventDeadline,
}

// ---------------------------------------------------------------------------
// Subject tests
// ---------------------------------------------------------------------------

func TestSubject_AllTypes(t *testing.T) {
	tests := []struct {
		typ  EventType
		want string
	}{
		{EventAssignment, "You've been assigned to Review — demo.txt (es)"},
		{EventRejection, "Step rejected: Review — demo.txt (es)"},
		{EventCompletion, "Workflow completed: demo.txt (es)"},
		{EventDeadline, "Deadline approaching: demo.txt (es)"},
	}
	for _, tt := range tests {
		t.Run(string(tt.typ), func(t *testing.T) {
			got := Subject(makeEvent(tt.typ))
			if got != tt.want {
				t.Errorf("Subject(%s) = %q, want %q", tt.typ, got, tt.want)
			}
		})
	}
}

func TestSubject_ContainsKeyFields(t *testing.T) {
	for _, typ := range allEventTypes {
		t.Run(string(typ), func(t *testing.T) {
			e := makeEvent(typ)
			subj := Subject(e)
			if !strings.Contains(subj, "es") {
				t.Errorf("Subject(%s) should contain target lang 'es', got %q", typ, subj)
			}
			if !strings.Contains(subj, "demo.txt") {
				t.Errorf("Subject(%s) should contain file name 'demo.txt', got %q", typ, subj)
			}
			// Assignment and rejection should include step name
			if typ == EventAssignment || typ == EventRejection {
				if !strings.Contains(subj, "Review") {
					t.Errorf("Subject(%s) should contain step name 'Review', got %q", typ, subj)
				}
			}
		})
	}
}

func TestSubject_UnknownType(t *testing.T) {
	e := makeEvent("unknown_type")
	got := Subject(e)
	if got != "OpenLoc notification" {
		t.Errorf("Subject(unknown) = %q, want fallback", got)
	}
}

// ---------------------------------------------------------------------------
// BodyPreview tests
// ---------------------------------------------------------------------------

func TestBodyPreview_NonEmpty(t *testing.T) {
	for _, typ := range allEventTypes {
		t.Run(string(typ), func(t *testing.T) {
			got := BodyPreview(makeEvent(typ))
			if got == "" {
				t.Errorf("BodyPreview(%s) is empty", typ)
			}
		})
	}
}

func TestBodyPreview_Assignment(t *testing.T) {
	got := BodyPreview(makeEvent(EventAssignment))
	for _, want := range []string{"Review", "demo.txt", "es", "Demo Project"} {
		if !strings.Contains(got, want) {
			t.Errorf("BodyPreview(assignment) missing %q: %s", want, got)
		}
	}
}

func TestBodyPreview_RejectionWithReason(t *testing.T) {
	got := BodyPreview(makeEvent(EventRejection))
	if !strings.Contains(got, "Missed terminology") {
		t.Errorf("BodyPreview(rejection) should contain reason, got %q", got)
	}
	if !strings.Contains(got, "Alice") {
		t.Errorf("BodyPreview(rejection) should contain actor name, got %q", got)
	}
}

func TestBodyPreview_RejectionNoReason(t *testing.T) {
	e := makeEvent(EventRejection)
	e.Reason = ""
	got := BodyPreview(e)
	if strings.Contains(got, "Reason:") {
		t.Errorf("BodyPreview(rejection) without reason should not contain 'Reason:', got %q", got)
	}
	if !strings.Contains(got, "Alice") {
		t.Errorf("BodyPreview(rejection) should still contain actor name, got %q", got)
	}
}

func TestBodyPreview_Completion(t *testing.T) {
	got := BodyPreview(makeEvent(EventCompletion))
	for _, want := range []string{"demo.txt", "es", "Demo Project"} {
		if !strings.Contains(got, want) {
			t.Errorf("BodyPreview(completion) missing %q: %s", want, got)
		}
	}
}

func TestBodyPreview_Deadline(t *testing.T) {
	got := BodyPreview(makeEvent(EventDeadline))
	if !strings.Contains(got, "Apr 15, 2026") {
		t.Errorf("BodyPreview(deadline) should contain formatted date, got %q", got)
	}
}

func TestBodyPreview_UnknownType(t *testing.T) {
	e := makeEvent("bogus")
	if got := BodyPreview(e); got != "" {
		t.Errorf("BodyPreview(unknown) should be empty, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// RenderHTML tests
// ---------------------------------------------------------------------------

func TestRenderHTML_AllTypesProduceValidHTML(t *testing.T) {
	for _, typ := range allEventTypes {
		t.Run(string(typ), func(t *testing.T) {
			html := renderHTML(t, makeEvent(typ), testBaseURL)
			if html == "" {
				t.Fatalf("RenderHTML(%s) returned empty HTML", typ)
			}
			if !strings.Contains(html, "<!DOCTYPE html>") {
				t.Error("missing DOCTYPE")
			}
			if !strings.Contains(html, "</html>") {
				t.Error("missing closing html tag")
			}
			// The details table names project, file and language.
			for _, want := range []string{">Demo Project</td>", ">demo.txt</td>", ">Spanish (es)</td>"} {
				if !strings.Contains(html, want) {
					t.Errorf("details table missing %q", want)
				}
			}
		})
	}
}

func TestRenderHTML_AssignmentContent(t *testing.T) {
	html := renderHTML(t, makeEvent(EventAssignment), testBaseURL)
	if !strings.Contains(html, "assigned") {
		t.Error("assignment HTML should mention 'assigned'")
	}
	if !strings.Contains(html, "Review") {
		t.Error("assignment HTML should contain step name")
	}
	if !strings.Contains(html, "Alice") {
		t.Error("assignment HTML should contain actor name")
	}
}

func TestRenderHTML_RejectionIncludesReason(t *testing.T) {
	e := makeEvent(EventRejection)
	e.Reason = "Numbers do not match source"
	html := renderHTML(t, e, testBaseURL)
	if !strings.Contains(html, "Numbers do not match source") {
		t.Error("rejection HTML should contain the reason text")
	}
	if !strings.Contains(html, ">Reason</div>") {
		t.Error("rejection HTML should have a 'Reason' label")
	}
}

func TestRenderHTML_RejectionNoReason(t *testing.T) {
	e := makeEvent(EventRejection)
	e.Reason = ""
	html := renderHTML(t, e, testBaseURL)
	if strings.Contains(html, ">Reason</div>") {
		t.Error("rejection HTML without reason should not contain the reason box")
	}
}

func TestRenderHTML_CompletionContent(t *testing.T) {
	html := renderHTML(t, makeEvent(EventCompletion), testBaseURL)
	if !strings.Contains(html, "completed") {
		t.Error("completion HTML should mention 'completed'")
	}
}

func TestRenderHTML_DeadlineContent(t *testing.T) {
	html := renderHTML(t, makeEvent(EventDeadline), testBaseURL)
	if !strings.Contains(html, "April 15, 2026") {
		t.Error("deadline HTML should contain formatted deadline date")
	}
	if !strings.Contains(html, "Review") {
		t.Error("deadline HTML should contain current step name")
	}
}

func TestRenderHTML_AssignmentNoActor(t *testing.T) {
	e := makeEvent(EventAssignment)
	e.ActorName = ""
	html := renderHTML(t, e, testBaseURL)
	// Detail should be absent when no actor
	if strings.Contains(html, "Assigned by") {
		t.Error("assignment HTML without actor should not contain 'Assigned by'")
	}
}

func TestRenderHTML_HTMLEscaping(t *testing.T) {
	e := makeEvent(EventRejection)
	e.Reason = "<script>alert('xss')</script>"
	html := renderHTML(t, e, testBaseURL)
	if strings.Contains(html, "<script>") {
		t.Error("HTML should escape script tags in reason")
	}
}

func TestBodyPreview_AssignmentComment(t *testing.T) {
	e := makeEvent(EventAssignment)
	if got := BodyPreview(e); strings.Contains(got, "Comment:") {
		t.Errorf("BodyPreview(assignment) without a comment should not contain 'Comment:', got %q", got)
	}
	e.Comment = "Section 3 uses the new term."
	want := "You've been assigned to the \"Review\" step for demo.txt (es) in project Demo Project. Comment: Section 3 uses the new term."
	if got := BodyPreview(e); got != want {
		t.Errorf("BodyPreview(assignment) with a comment:\n got %q\nwant %q", got, want)
	}
}

func TestRenderHTML_AssignmentComment(t *testing.T) {
	e := makeEvent(EventAssignment)
	if html := renderHTML(t, e, testBaseURL); strings.Contains(html, ">Comment</div>") {
		t.Errorf("assignment email without a comment should have no Comment box:\n%s", html)
	}

	e.Comment = "<script>alert('xss')</script> Section 3\nuses the new term."
	html := renderHTML(t, e, testBaseURL)
	if !strings.Contains(html, ">Comment</div>") {
		t.Errorf("assignment email should quote the comment under a Comment label:\n%s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt; Section 3\nuses the new term.") {
		t.Errorf("assignment email should carry the escaped comment, line break kept:\n%s", html)
	}
	if strings.Contains(html, "<script>") {
		t.Error("HTML should escape script tags in the comment")
	}
}

func TestRenderHTML_ButtonOpensJobInEditor(t *testing.T) {
	labels := map[EventType]string{
		EventAssignment: "Open task",
		EventRejection:  "Open job",
		EventCompletion: "Open job",
		EventDeadline:   "Open job",
	}
	for _, typ := range allEventTypes {
		t.Run(string(typ), func(t *testing.T) {
			html := renderHTML(t, makeEvent(typ), testBaseURL+"/")
			if !strings.Contains(html, `href="https://app.example.com/projects/50/jobs/100/edit"`) {
				t.Errorf("missing button link to the job:\n%s", html)
			}
			if !strings.Contains(html, ">"+labels[typ]+"</a>") {
				t.Errorf("missing %q button:\n%s", labels[typ], html)
			}
		})
	}
}

// An event whose project could not be resolved has no job page to link to.
func TestRenderHTML_NoButtonWithoutProject(t *testing.T) {
	e := makeEvent(EventCompletion)
	e.ProjectID = 0
	html := renderHTML(t, e, testBaseURL)
	if strings.Contains(html, "/jobs/") {
		t.Errorf("button rendered without a project:\n%s", html)
	}
}

func TestRenderHTML_FooterLinksToNotificationSettings(t *testing.T) {
	html := renderHTML(t, makeEvent(EventAssignment), testBaseURL)
	if !strings.Contains(html, `href="https://app.example.com/settings"`) {
		t.Errorf("missing notification settings link:\n%s", html)
	}
}

// Without the app's origin a link would be relative, which no mail client
// can open, so the email has none.
func TestRenderHTML_NoBaseURLNoLinks(t *testing.T) {
	html := renderHTML(t, makeEvent(EventAssignment), "")
	if strings.Contains(html, "href=") {
		t.Errorf("links rendered without a base URL:\n%s", html)
	}
}

func TestRenderHTML_PreheaderIsBodyPreview(t *testing.T) {
	e := makeEvent(EventRejection)
	html := renderHTML(t, e, testBaseURL)
	if want := template.HTMLEscapeString(BodyPreview(e)); !strings.Contains(html, want) {
		t.Errorf("preheader missing %q:\n%s", want, html)
	}
}

func TestRenderHTML_IssueUpdate(t *testing.T) {
	e := Event{
		Type: EventIssueUpdated, IssueID: 7, IssueTitle: "Export does nothing",
		IssueStatus: "Needs info", Reason: "Which file format?", Link: testBaseURL + "/issues/7",
	}
	html := renderHTML(t, e, testBaseURL)
	for _, want := range []string{
		"Your issue has an update", "Export does nothing", ">Needs info</strong>",
		">Comment</div>", "Which file format?",
		`<a href="https://app.example.com/issues/7" style="display: inline-block;`, ">View the issue</a>",
		`href="https://app.example.com/settings"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("issue update email missing %q:\n%s", want, html)
		}
	}
	// An issue has no job, so the email has no job details or job link.
	for _, unwanted := range []string{">Project</td>", "/jobs/"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("issue update email has %q:\n%s", unwanted, html)
		}
	}
}
