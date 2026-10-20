package workflow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/autostep"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/notification"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/testutil"
	"openloc/internal/workflow"
)

// The comment written when completing a step is the hand-off note: it is
// stored on the step that starts next and returned by the job's steps API,
// which the editor reads to show it to whoever works that step.

type noteJob struct {
	projectID int64
	jobID     int64
	// Assignment ids, in step order.
	steps []int64
}

// seedNoteJob gives a job of its own project one assignment per step type, in
// order. The first is in progress and the job's current step; the rest wait.
func seedNoteJob(t *testing.T, q db.Querier, tenantID int64, name string, stepTypes ...string) noteJob {
	t.Helper()
	return seedNoteJobFor(t, q, tenantID, name, nil, stepTypes...)
}

// seedNoteJobFor is seedNoteJob whose steps after the first name next as their
// default assignee (an AI step takes nobody).
func seedNoteJobFor(t *testing.T, q db.Querier, tenantID int64, name string, next *int64, stepTypes ...string) noteJob {
	t.Helper()
	project := testutil.SeedProject(t, q, tenantID, name, "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, name+".txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")
	specs := make([]testutil.WorkflowStepSpec, 0, len(stepTypes))
	for i, stepType := range stepTypes {
		spec := testutil.WorkflowStepSpec{Name: fmt.Sprintf("%s %d", stepType, i), StepType: stepType}
		if stepType == "ai" {
			router := testutil.SeedAIRouter(t, q, tenantID, fmt.Sprintf("%s router %d", name, i), nil)
			spec.RouterID = &router.ID
		}
		if i == 0 {
			spec.Status = "in_progress"
		} else if stepType != "ai" {
			spec.DefaultAssignee = next
		}
		specs = append(specs, spec)
	}
	return noteJob{
		projectID: project.ID,
		jobID:     job.ID,
		steps:     testutil.SeedWorkflow(t, q, project.SettingsProfileID, job.ID, specs...),
	}
}

// stepNotes reads each step's handoff_note through the steps API, keyed by
// assignment id.
func stepNotes(t *testing.T, router http.Handler, jobID, userID, tenantID int64) map[int64]*string {
	t.Helper()
	rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/workflow/steps", jobID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data []struct {
			ID          int64   `json:"id"`
			HandoffNote *string `json:"handoff_note"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	notes := make(map[int64]*string, len(resp.Data))
	for _, s := range resp.Data {
		notes[s.ID] = s.HandoffNote
	}
	return notes
}

func wantNote(t *testing.T, what string, got *string, want string) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: handoff_note is null, want %q", what, want)
	} else if *got != want {
		t.Errorf("%s: handoff_note = %q, want %q", what, *got, want)
	}
}

func wantNoNote(t *testing.T, what string, got *string) {
	t.Helper()
	if got != nil {
		t.Errorf("%s: handoff_note = %q, want null", what, *got)
	}
}

func advanceWith(t *testing.T, router http.Handler, jobID int64, body any, userID, tenantID int64) {
	t.Helper()
	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", jobID), body, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

func TestIntegrationHandoffNote(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()
	ownerID, tenantID := testutil.RegisterUser(t, q, "handoff-note")

	t.Run("the comment lands on the next step and in the history", func(t *testing.T) {
		nj := seedNoteJob(t, q, tenantID, "note-stored", "human", "human")
		advanceWith(t, router, nj.jobID, map[string]any{"comment": "  Please check terminology\n"}, ownerID, tenantID)

		notes := stepNotes(t, router, nj.jobID, ownerID, tenantID)
		wantNote(t, "next step", notes[nj.steps[1]], "Please check terminology")
		wantNoNote(t, "completed step", notes[nj.steps[0]])

		events, err := q.ListProjectEvents(ctx, db.ListProjectEventsParams{
			ProjectID: nj.projectID, TenantID: tenantID, Limit: 10, EventPrefix: projectevents.TypeStepAdvanced,
		})
		if err != nil {
			t.Fatalf("list project events: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("want 1 %s event, got %d", projectevents.TypeStepAdvanced, len(events))
		}
		var details map[string]any
		if err := json.Unmarshal(events[0].Details, &details); err != nil {
			t.Fatalf("event details: %v", err)
		}
		if details["handoff_note"] != "Please check terminology" {
			t.Errorf("event handoff_note = %v, want the comment", details["handoff_note"])
		}
	})

	t.Run("no comment, or only spaces, leaves the note null", func(t *testing.T) {
		for _, body := range []any{nil, map[string]any{"comment": "   "}} {
			nj := seedNoteJob(t, q, tenantID, fmt.Sprintf("note-none-%v", body != nil), "human", "human")
			advanceWith(t, router, nj.jobID, body, ownerID, tenantID)
			wantNoNote(t, fmt.Sprintf("body %v", body), stepNotes(t, router, nj.jobID, ownerID, tenantID)[nj.steps[1]])
		}
	})

	t.Run("a body that is not the expected JSON is refused and advances nothing", func(t *testing.T) {
		nj := seedNoteJob(t, q, tenantID, "note-bad-body", "human", "human")
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", nj.jobID),
			"Please check terminology", ownerID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		active, err := q.GetActiveStepForJob(ctx, nj.jobID)
		if err != nil {
			t.Fatalf("active step: %v", err)
		}
		if active.ID != nj.steps[0] {
			t.Errorf("active step moved to %d, want %d", active.ID, nj.steps[0])
		}
	})

	t.Run("a control character or an over-long comment is refused and advances nothing", func(t *testing.T) {
		nj := seedNoteJob(t, q, tenantID, "note-invalid", "human", "human", "human")
		// A refused comment is the user's mistake, not a server failure.
		logs := testutil.CaptureLogs(t)
		defer func() {
			if recs := testutil.ErrorRecords(t, logs); len(recs) > 0 {
				t.Errorf("a refused comment logged %d error record(s), first: %v", len(recs), recs[0])
			}
		}()
		for name, body := range map[string]map[string]any{
			"NUL byte":  {"comment": "check\x00terms"},
			"over-long": {"comment": strings.Repeat("x", workflow.MaxNoteLength+1)},
		} {
			rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", nj.jobID), body, ownerID, tenantID)
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
			active, err := q.GetActiveStepForJob(ctx, nj.jobID)
			if err != nil {
				t.Fatalf("%s: active step: %v", name, err)
			}
			if active.ID != nj.steps[0] || active.Status != "in_progress" {
				t.Errorf("%s: active step %d/%s, want %d in_progress", name, active.ID, active.Status, nj.steps[0])
			}
		}
		// A comment exactly at the cap, with the line breaks and tabs a person
		// types inside it, is stored as written.
		atCap := "line one\n\tline two\r\n" + strings.Repeat("é", workflow.MaxNoteLength-len([]rune("line one\n\tline two\r\n")))
		if n := len([]rune(atCap)); n != workflow.MaxNoteLength {
			t.Fatalf("test input has %d runes, want %d", n, workflow.MaxNoteLength)
		}
		advanceWith(t, router, nj.jobID, map[string]any{"comment": atCap}, ownerID, tenantID)
		wantNote(t, "comment at the cap", stepNotes(t, router, nj.jobID, ownerID, tenantID)[nj.steps[1]], atCap)

		// The reject reason follows the same rule.
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/reject", nj.jobID),
			map[string]any{"reason": "bad\x00"}, ownerID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		active, err := q.GetActiveStepForJob(ctx, nj.jobID)
		if err != nil {
			t.Fatalf("active step after refused reject: %v", err)
		}
		if active.ID != nj.steps[1] || active.Status != "in_progress" {
			t.Errorf("refused reject moved the active step: %d/%s, want %d in_progress", active.ID, active.Status, nj.steps[1])
		}
	})

	t.Run("each hand-off replaces the note: a later one without a comment clears it", func(t *testing.T) {
		nj := seedNoteJob(t, q, tenantID, "note-replaced", "human", "human")
		advanceWith(t, router, nj.jobID, map[string]any{"comment": "First pass done"}, ownerID, tenantID)
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/reject", nj.jobID),
			map[string]any{"reason": "Wrong terms"}, ownerID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)

		advanceWith(t, router, nj.jobID, nil, ownerID, tenantID)
		wantNoNote(t, "after a second hand-off without a comment", stepNotes(t, router, nj.jobID, ownerID, tenantID)[nj.steps[1]])
	})

	t.Run("sending the job back clears the note on the step that sent it", func(t *testing.T) {
		nj := seedNoteJob(t, q, tenantID, "note-sent-back", "human", "human")
		advanceWith(t, router, nj.jobID, map[string]any{"comment": "First pass done"}, ownerID, tenantID)
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/reject", nj.jobID),
			map[string]any{"reason": "Wrong terms"}, ownerID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)

		wantNoNote(t, "sent-back step", stepNotes(t, router, nj.jobID, ownerID, tenantID)[nj.steps[1]])
	})

	t.Run("an AI step passes the note on to the step after it", func(t *testing.T) {
		nj := seedNoteJob(t, q, tenantID, "note-through-ai", "human", "ai", "human")
		advanceWith(t, router, nj.jobID, map[string]any{"comment": "Check the glossary"}, ownerID, tenantID)
		autostep.Complete(ctx, q, workflow.NewService(q, pipeline.NewPgxTxRunner(pool), nil), tenantID, nj.jobID, "ai", nil, nil)

		notes := stepNotes(t, router, nj.jobID, ownerID, tenantID)
		active, err := q.GetActiveStepForJob(ctx, nj.jobID)
		if err != nil {
			t.Fatalf("active step: %v", err)
		}
		if active.ID != nj.steps[2] {
			t.Fatalf("the AI step did not complete: active step %d, want %d", active.ID, nj.steps[2])
		}
		wantNote(t, "step after the AI step", notes[nj.steps[2]], "Check the glossary")
	})

	t.Run("another tenant can neither write nor read the note", func(t *testing.T) {
		nj := seedNoteJob(t, q, tenantID, "note-tenant", "human", "human")
		otherID, otherTenantID := testutil.RegisterUser(t, q, "handoff-note-other")

		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", nj.jobID),
			map[string]any{"comment": "Injected"}, otherID, otherTenantID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/workflow/steps", nj.jobID), nil, otherID, otherTenantID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		wantNoNote(t, "after another tenant's advance", stepNotes(t, router, nj.jobID, ownerID, tenantID)[nj.steps[1]])
	})
}

// assignments records the assignment notifications a workflow service sends.
func assignments(q db.Querier, tx pipeline.TxRunner) (*workflow.Service, func() []notification.Event) {
	var sent []notification.Event
	svc := workflow.NewService(q, tx, func(_ context.Context, ev notification.Event) {
		if ev.Type == notification.EventAssignment {
			sent = append(sent, ev)
		}
	})
	return svc, func() []notification.Event { return sent }
}

// The next assignee's notification carries the hand-off note, so they read it
// before opening the job.
func TestIntegrationHandoffNoteNotifiesNextAssignee(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, tenantID := testutil.RegisterUser(t, q, "handoff-notify")
	reviewer := testutil.SeedUserWithRole(t, q, tenantID, "reviewer@handoff-notify.test", "pw", "linguist")
	owner := workflow.Actor{UserID: ownerID, Role: "owner"}

	for _, tc := range []struct{ name, comment, want string }{
		{"with a comment", "  Check section 3\nfirst.\n", "Check section 3\nfirst."},
		{"without one", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nj := seedNoteJobFor(t, q, tenantID, "notify-"+tc.name, &reviewer.ID, "human", "human")
			svc, sent := assignments(q, pipeline.NewPgxTxRunner(pool))
			if _, err := svc.Advance(ctx, tenantID, nj.jobID, owner, tc.comment); err != nil {
				t.Fatalf("advance: %v", err)
			}
			got := sent()
			if len(got) != 1 || got[0].RecipientID != reviewer.ID {
				t.Fatalf("want one assignment notification to the reviewer, got %+v", got)
			}
			if got[0].Comment != tc.want {
				t.Errorf("notification comment = %q, want %q", got[0].Comment, tc.want)
			}
		})
	}

	t.Run("across an AI step", func(t *testing.T) {
		nj := seedNoteJobFor(t, q, tenantID, "notify-through-ai", &reviewer.ID, "human", "ai", "human")
		svc, sent := assignments(q, pipeline.NewPgxTxRunner(pool))
		if _, err := svc.Advance(ctx, tenantID, nj.jobID, owner, "Check the glossary"); err != nil {
			t.Fatalf("advance: %v", err)
		}
		if got := sent(); len(got) != 0 {
			t.Fatalf("the AI step has nobody to notify, got %+v", got)
		}
		autostep.Complete(ctx, q, svc, tenantID, nj.jobID, "ai", nil, nil)

		got := sent()
		if len(got) != 1 || got[0].RecipientID != reviewer.ID {
			t.Fatalf("want one assignment notification to the reviewer after the AI step, got %+v", got)
		}
		if got[0].Comment != "Check the glossary" {
			t.Errorf("notification comment = %q, want the translator's comment", got[0].Comment)
		}
	})
}
