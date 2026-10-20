package workflow_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/testutil"
	"openloc/internal/workflow"
)

// A rejection can send the job back several steps. The reopened step looks as
// it was left, except where something changed after it: that text comes back
// unconfirmed without an AI verdict, and a verdict a later step's AI gave is
// dropped. Reopening with the AI run again reviews every translated, unlocked
// segment, people's text included, and needs a manager.

func (f roundsFixture) owner() workflow.Actor {
	return workflow.Actor{UserID: f.ownerID, Role: "owner"}
}

func (f roundsFixture) rejectTo(t *testing.T, actor workflow.Actor, step int, rerun bool) (workflow.RejectResult, error) {
	t.Helper()
	return f.svc.Reject(f.ctx, f.tenantID, f.jobID, actor, workflow.RejectOpts{
		Reason: "look again", ToStepID: &f.steps[step], RerunAI: rerun,
	})
}

func (f roundsFixture) predict(t *testing.T, i int, outcome db.PredictOutcome, status db.TranslationStatus, note string) {
	t.Helper()
	tr := f.tr(t, i)
	if _, err := f.q.UpdateTranslationPredict(f.ctx, db.UpdateTranslationPredictParams{
		ID: tr.ID, ActiveText: tr.ActiveText,
		PredictOutcome: db.NullPredictOutcome{PredictOutcome: outcome, Valid: true},
		Status:         status, QeNote: &note,
	}); err != nil {
		t.Fatalf("predict %d: %v", i, err)
	}
}

func (f roundsFixture) stepStatusOf(t *testing.T, step int) string {
	t.Helper()
	rows, err := f.q.ListJobStepAssignments(f.ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	return rows[step].Status
}

func wantNoVerdict(t *testing.T, label string, tr db.Translation) {
	t.Helper()
	if tr.PredictOutcome.Valid || tr.QeNote != nil {
		t.Errorf("%s: verdict %v, QE note %v, want none", label, tr.PredictOutcome, tr.QeNote)
	}
}

func TestIntegrationRollback_SeveralStepsBack(t *testing.T) {
	f := setupRoundsSteps(t,
		testutil.WorkflowStepSpec{Name: "Translation", StepType: "human"},
		testutil.WorkflowStepSpec{Name: "Review", StepType: "human"},
		testutil.WorkflowStepSpec{Name: "Check", StepType: "hybrid"},
		testutil.WorkflowStepSpec{Name: "Final", StepType: "human"},
	)

	// Translation: 0 confirmed, 2 auto-approved by an AI with a score.
	f.confirm(t, 0, "uno")
	f.predict(t, 2, db.PredictOutcomeApproved, db.TranslationStatusAutoApproved, "90")
	f.advance(t)
	// Review: 0 confirmed unchanged, 1 rewritten and confirmed.
	f.confirm(t, 0, "uno")
	f.confirm(t, 1, "dos")
	f.advance(t)
	// Check: a person rewrites 0, the step's AI scores 3.
	f.draft(t, 0, "uno, otra vez")
	f.predict(t, 3, db.PredictOutcomeApproved, db.TranslationStatusAutoApproved, "80")
	f.advance(t)
	queued := len(f.enq.AISteps)

	res, err := f.rejectTo(t, f.owner(), 1, false)
	if err != nil {
		t.Fatalf("reject to Review: %v", err)
	}
	if res.ReopenedStepID != f.steps[1] || res.RerunAI {
		t.Errorf("result = %+v, want Review reopened without an AI run", res)
	}
	if n := len(f.enq.AISteps) - queued; n != 0 {
		t.Errorf("the rejection queued %d AI runs, want none", n)
	}
	for i, want := range []string{"completed", "in_progress", "pending", "rejected"} {
		if got := f.stepStatusOf(t, i); got != want {
			t.Errorf("step %d: status %s, want %s", i, got, want)
		}
	}
	if j, _ := f.q.GetJob(f.ctx, f.jobID); j.CurrentStepID == nil || *j.CurrentStepID != f.steps[1] {
		t.Errorf("current step = %v, want Review (%d)", j.CurrentStepID, f.steps[1])
	}

	// Changed after Review: unconfirmed, marked edited, no verdict.
	s0 := f.tr(t, 0)
	wantStatus(t, "rewritten after Review", s0.Status, db.TranslationStatusNeedsReview)
	if !s0.StepEdited {
		t.Error("text changed after the reopened step must come back as a draft")
	}
	// Review's own work comes back.
	wantStatus(t, "confirmed in Review", f.tr(t, 1).Status, db.TranslationStatusApproved)
	// An earlier verdict on text nobody changed stays.
	s2 := f.tr(t, 2)
	if s2.PredictOutcome.PredictOutcome != db.PredictOutcomeApproved || s2.QeNote == nil || *s2.QeNote != "90" {
		t.Errorf("Translation's verdict on unchanged text = %v / %v, want approved / 90", s2.PredictOutcome, s2.QeNote)
	}
	// The undone Check step's verdict is dropped.
	wantNoVerdict(t, "scored by the undone Check step", f.tr(t, 3))
	wantStatus(t, "scored by the undone Check step", f.tr(t, 3).Status, db.TranslationStatusNeedsReview)

	// Forward again: the reset steps run in order.
	f.advance(t)
	if got := f.stepStatusOf(t, 2); got != "in_progress" {
		t.Errorf("after Review completes, Check is %s, want in_progress", got)
	}
	if n := len(f.enq.AISteps) - queued; n != 1 {
		t.Errorf("reaching Check again queued %d AI runs, want 1", n)
	}
}

func TestIntegrationRollback_Refusals(t *testing.T) {
	f := setupRoundsSteps(t,
		testutil.WorkflowStepSpec{Name: "Translation", StepType: "human"},
		testutil.WorkflowStepSpec{Name: "Auto", StepType: "ai"},
		testutil.WorkflowStepSpec{Name: "Review", StepType: "human"},
		testutil.WorkflowStepSpec{Name: "Final", StepType: "human"},
	)
	f.advance(t)
	f.advance(t)
	linguistID := testutil.SeedUserWithRole(t, f.q, f.tenantID, "rollback-linguist@test.test", "pw", "linguist").ID
	if err := f.q.AssignStepToUser(f.ctx, db.AssignStepToUserParams{ID: f.steps[2], AssignedTo: &linguistID}); err != nil {
		t.Fatal(err)
	}
	linguist := workflow.Actor{UserID: linguistID, Role: "linguist"}

	var ce *httputil.ClientError
	var fe *httputil.ForbiddenError
	if _, err := f.rejectTo(t, linguist, 1, false); !errors.As(err, &fe) {
		t.Errorf("assignee sends back to an AI step: err = %v, want forbidden (it runs the AI again)", err)
	}
	if _, err := f.rejectTo(t, f.owner(), 0, true); !errors.As(err, &ce) {
		t.Errorf("re-run on a human step: err = %v, want a client error", err)
	}
	if _, err := f.rejectTo(t, f.owner(), 3, false); !errors.As(err, &ce) {
		t.Errorf("send forward: err = %v, want a client error", err)
	}
	if _, err := f.rejectTo(t, f.owner(), 2, false); !errors.As(err, &ce) {
		t.Errorf("send to the current step: err = %v, want a client error", err)
	}
	foreign := f.otherJobStep(t)
	if _, err := f.svc.Reject(f.ctx, f.tenantID, f.jobID, f.owner(), workflow.RejectOpts{ToStepID: &foreign}); !errors.As(err, &ce) {
		t.Errorf("another job's step: err = %v, want a client error", err)
	}
	if got := f.stepStatusOf(t, 2); got != "in_progress" {
		t.Fatalf("a refused rejection changed the current step to %s", got)
	}

	// The assignee may go back further without the AI.
	if _, err := f.rejectTo(t, linguist, 0, false); err != nil {
		t.Fatalf("assignee sends back to Translation: %v", err)
	}
	if got := f.stepStatusOf(t, 1); got != "pending" {
		t.Errorf("the AI step in between is %s, want pending", got)
	}
}

func TestIntegrationRollback_RerunAIReviewsEverything(t *testing.T) {
	f := setupRoundsSteps(t,
		testutil.WorkflowStepSpec{Name: "Check", StepType: "hybrid"},
		testutil.WorkflowStepSpec{Name: "Review", StepType: "human"},
	)
	// Check: 0 written and confirmed by a person, 1 auto-approved with a score,
	// 2 locked, 3 untouched machine text.
	f.confirm(t, 0, "uno")
	f.predict(t, 1, db.PredictOutcomeApproved, db.TranslationStatusAutoApproved, "90")
	f.confirm(t, 2, "dos")
	if err := f.q.LockTranslation(f.ctx, f.tr(t, 2).ID); err != nil {
		t.Fatal(err)
	}
	f.advance(t)

	res, err := f.rejectTo(t, f.owner(), 0, true)
	if err != nil {
		t.Fatalf("reject with re-run: %v", err)
	}
	if !res.RerunAI || len(f.enq.AISteps) != 1 || f.enq.AISteps[0].JobID != f.jobID {
		t.Fatalf("result %+v, queued %+v, want one AI run for the job", res, f.enq.AISteps)
	}
	for _, i := range []int{0, 1, 3} {
		tr := f.tr(t, i)
		wantStatus(t, "reviewed again", tr.Status, db.TranslationStatusNeedsReview)
		wantNoVerdict(t, "reviewed again", tr)
	}
	if tr := f.tr(t, 2); !tr.Locked || tr.Status != db.TranslationStatusApproved {
		t.Errorf("locked segment: locked=%v status=%s, want locked approved", tr.Locked, tr.Status)
	}
	if got := f.tr(t, 0).ActiveText; got == nil || *got != "uno" {
		t.Errorf("a person's text = %v, want it kept for the AI to review", got)
	}

	// The run takes people's text too, never a locked segment.
	rows, err := f.q.ListPreTranslatedByFile(f.ctx, db.ListPreTranslatedByFileParams{FileID: f.fileID, TargetLanguage: "es", ReviewAll: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, r := range rows {
		got[r.ID] = true
	}
	for i, want := range []bool{true, true, false, true} {
		if got[f.tr(t, i).ID] != want {
			t.Errorf("segment %d in the review-all run = %v, want %v", i, got[f.tr(t, i).ID], want)
		}
	}
	// The write takes a person's text only in that run.
	text := "uno"
	write := func(reviewAll bool) error {
		_, err := f.q.UpdateTranslationPredict(f.ctx, db.UpdateTranslationPredictParams{
			ID: f.tr(t, 0).ID, ActiveText: &text, ReviewAll: reviewAll,
			PredictOutcome: db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeApproved, Valid: true},
			Status:         db.TranslationStatusAutoApproved,
		})
		return err
	}
	if err := write(false); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("a normal run's write on a person's text: err = %v, want no row", err)
	}
	if err := write(true); err != nil {
		t.Errorf("the review-all run's write on a person's text: %v", err)
	}

	// The round knows it reviews everything; the next one does not.
	active, err := f.q.GetActiveStepForJob(f.ctx, f.jobID)
	if err != nil || !active.AiReviewAll {
		t.Fatalf("reopened Check: ai_review_all = %v (err %v), want true", active.AiReviewAll, err)
	}
	f.advance(t)
	if _, err := f.svc.Reject(f.ctx, f.tenantID, f.jobID, f.owner(), workflow.RejectOpts{}); err != nil {
		t.Fatal(err)
	}
	if active, _ := f.q.GetActiveStepForJob(f.ctx, f.jobID); active.AiReviewAll {
		t.Error("a plain reopen must not review everything")
	}
}

func TestIntegrationRollback_PersonEditDropsQEScore(t *testing.T) {
	f := setupRounds(t, "Translation", "Review")
	f.predict(t, 0, db.PredictOutcomeRejected, db.TranslationStatusNeedsReview, "40")
	f.predict(t, 1, db.PredictOutcomeRejected, db.TranslationStatusNeedsReview, "45")

	f.confirm(t, 0, *f.tr(t, 0).ActiveText)
	if f.tr(t, 0).QeNote == nil {
		t.Error("a confirm that keeps the text must keep its QE score")
	}
	f.draft(t, 1, "otra cosa")
	if n := f.tr(t, 1).QeNote; n != nil {
		t.Errorf("a person's new text kept the QE score %q of the old one", *n)
	}
	if !f.tr(t, 1).PredictOutcome.Valid {
		t.Error("the verdict stays for analytics; only the score shown on the segment goes")
	}
}

// otherJobStep gives a second job of the fixture's project an assignment of
// the same first workflow step, and returns it.
func (f roundsFixture) otherJobStep(t *testing.T) int64 {
	t.Helper()
	file, err := f.q.GetFile(f.ctx, f.fileID)
	if err != nil {
		t.Fatal(err)
	}
	other := testutil.SeedJob(t, f.q, testutil.SeedFile(t, f.q, file.ProjectID, "other.txt", db.FileFormatTxt).ID, "es")
	first, err := f.q.GetCurrentStepForJob(f.ctx, f.steps[0])
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.q.CreateJobStepAssignment(f.ctx, db.CreateJobStepAssignmentParams{
		JobID: other.ID, WorkflowStepID: first.WorkflowStepID, Status: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// After a rollback, a step the job comes back to gets its own earlier result
// back on every segment nobody changed since it ended; a segment changed since
// starts unconfirmed, as in any new step.
func TestIntegrationRollback_ReenteredStepGetsItsWorkBack(t *testing.T) {
	f := setupRounds(t, "Translation", "Review", "Proofreading", "Final check")
	f.confirm(t, 0, "uno")
	f.confirm(t, 1, "dos")
	f.advance(t)
	// Review: 0 confirmed unchanged, 2 left as a draft, 1 and 3 untouched.
	f.confirm(t, 0, "uno")
	f.draft(t, 2, "tres, revisado")
	f.advance(t)
	// Proofreading sends the job back to Translation, which rewrites 1.
	if _, err := f.rejectTo(t, f.owner(), 0, false); err != nil {
		t.Fatal(err)
	}
	f.confirm(t, 1, "dos, otra vez")
	f.advance(t)

	wantStatus(t, "confirmed in Review, unchanged since", f.tr(t, 0).Status, db.TranslationStatusApproved)
	s1 := f.tr(t, 1)
	wantStatus(t, "rewritten in Translation after the rollback", s1.Status, db.TranslationStatusNeedsReview)
	if s1.StepEdited {
		t.Error("a segment changed since Review ended starts unedited in Review, like any new step")
	}
	s2 := f.tr(t, 2)
	wantStatus(t, "Review's draft", s2.Status, db.TranslationStatusNeedsReview)
	if !s2.StepEdited {
		t.Error("Review's own draft, unchanged since, must come back as a draft")
	}
	wantStatus(t, "untouched", f.tr(t, 3).Status, db.TranslationStatusNeedsReview)
	if got := f.stepStatusOf(t, 1); got != "in_progress" {
		t.Errorf("Review is %s, want in_progress", got)
	}
}
