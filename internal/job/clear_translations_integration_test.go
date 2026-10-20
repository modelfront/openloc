package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// setActiveText sets a translation's visible target text via raw SQL (the
// seed helper leaves it NULL). Mirrors clear_integration_test's
// setTranslationFields for the fields this test needs.
func setActiveText(t *testing.T, pool *pgxpool.Pool, id int64, text string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE translations SET active_text = $2 WHERE id = $1", id, text); err != nil {
		t.Fatalf("setActiveText(%d): %v", id, err)
	}
}

func setLocked(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE translations SET locked = true WHERE id = $1", id); err != nil {
		t.Fatalf("setLocked(%d): %v", id, err)
	}
}

// A job whose workflow ran to completion ends up
// with current_step_id == nil and every step 'completed'. In that state the
// CAT editor computes read-only (workflow present, no in_progress step) and
// the user is stuck — clearing + re-pretranslating the file leaves it
// permanently uneditable with no way to revert progress.
//
// Contract: clearing translations on a completed-workflow job is a "revert
// progress" action — it must reopen the first step (in_progress) and set it
// as the job's current step, restoring an editable state.
//
// Anti-tautology RED proof: remove the workflow-reopen block from
// HandleClearTranslations. The post-condition below (an active step exists +
// current_step_id points at the first assignment) flips to fail — no step is
// in_progress and current_step_id stays nil.
func TestIntegrationClearTranslations_ReopensCompletedWorkflow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ClearRevertCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@clear-revert.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Clear Revert", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// Drive a single-step workflow to COMPLETION: mark the step completed and
	// clear current_step_id to nil — exactly the state HandleAdvanceStep
	// leaves after the final step.
	assignmentID := testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &user.ID)
	if err := q.CompleteStepAssignment(ctx, assignmentID); err != nil {
		t.Fatalf("complete step: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: nil}); err != nil {
		t.Fatalf("clear current step: %v", err)
	}

	// Pre-condition: no in_progress step (this is what renders read-only).
	if _, err := q.GetActiveStepForJob(ctx, job.ID); err == nil {
		t.Fatal("pre-condition: expected NO active step on a completed workflow — test would be tautological")
	}

	// Act: clear translations (the revert action the user reached for).
	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", job.ID),
		map[string]any{"scope": "all"}, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Post-condition: the first step is reopened (in_progress) and is the
	// job's current step — the file is editable again.
	active, err := q.GetActiveStepForJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("post-condition: expected an active step after clear, got err: %v", err)
	}
	if active.ID != assignmentID {
		t.Errorf("expected first step %d reopened, got %d", assignmentID, active.ID)
	}
	post, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if post.CurrentStepID == nil || *post.CurrentStepID != assignmentID {
		t.Errorf("expected current_step_id=%d after revert, got %v", assignmentID, post.CurrentStepID)
	}
}

// The exact clear count shown in the dialog (GetJobTranslationCounts.
// clearable_*) must equal the number the clear actually affects — locked,
// approved (in non-confirmed scope), and already-empty rows excluded, so the
// dialog never needs an "up to N" upper bound. Anti-tautology: revert the clearable_* count
// columns to plain pre_translated+needs_review (ignoring locked) and the
// count-vs-cleared equality below breaks.
func TestIntegrationClearTranslations_ExactCountMatchesCleared(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ExactClearCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@exact-clear.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Exact Clear", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// seg1: needs_review WITH text → clearable in both scopes.
	s1 := testutil.SeedSegment(t, q, file.ID, "one", 1)
	tr1 := testutil.SeedTranslation(t, q, s1.ID, "de", db.TranslationStatusNeedsReview)
	setActiveText(t, pool, tr1.ID, "eins")
	// seg2: approved WITH text → clearable in "all" only.
	s2 := testutil.SeedSegment(t, q, file.ID, "two", 2)
	tr2 := testutil.SeedTranslation(t, q, s2.ID, "de", db.TranslationStatusApproved)
	setActiveText(t, pool, tr2.ID, "zwei")
	// seg3: needs_review WITH text but LOCKED → clearable in neither scope.
	s3 := testutil.SeedSegment(t, q, file.ID, "three", 3)
	tr3 := testutil.SeedTranslation(t, q, s3.ID, "de", db.TranslationStatusNeedsReview)
	setActiveText(t, pool, tr3.ID, "drei")
	setLocked(t, pool, tr3.ID)
	// seg4: untranslated (no text) → clearing is a no-op, excluded from both.
	s4 := testutil.SeedSegment(t, q, file.ID, "four", 4)
	testutil.SeedTranslation(t, q, s4.ID, "de", db.TranslationStatusNeedsReview)

	// The counts the dialog reads.
	c, err := q.GetJobTranslationCounts(ctx, db.GetJobTranslationCountsParams{
		FileID: file.ID, TargetLanguage: "de",
	})
	if err != nil {
		t.Fatalf("GetJobTranslationCounts: %v", err)
	}
	if c.ClearableNonConfirmed != 1 {
		t.Errorf("clearable_non_confirmed: want 1 (seg1 only), got %d", c.ClearableNonConfirmed)
	}
	if c.ClearableAll != 2 {
		t.Errorf("clearable_all: want 2 (seg1 + seg2), got %d", c.ClearableAll)
	}

	// Clearing non-confirmed must affect exactly clearable_non_confirmed rows.
	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", job.ID),
		map[string]any{"scope": "non_confirmed"}, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var body struct {
		Cleared int `json:"cleared"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode clear response: %v", err)
	}
	if body.Cleared != int(c.ClearableNonConfirmed) {
		t.Errorf("cleared count %d must equal the shown clearable_non_confirmed %d",
			body.Cleared, c.ClearableNonConfirmed)
	}
}

// Clearing a finished workflow reopens the job as well as its first step, so it
// can be completed again (Complete refuses a job still marked completed). Only
// a manager may revert a finished workflow; a linguist is refused.
func TestIntegrationClearTranslations_ReopensCompletedJob(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	ownerID, tenantID := testutil.RegisterUser(t, q, "clear-reopen-job")
	ling := testutil.SeedUserWithRole(t, q, tenantID, "clear-reopen-ling@test.com", "pw", "linguist")
	project := testutil.SeedProject(t, q, tenantID, "Clear Reopen Job", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedWorkflow(t, q, project.SettingsProfileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translate", Assignee: &ling.ID, Status: "completed"})
	if err := q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: job.ID, Status: "completed", CurrentStep: "completed"}); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/jobs/%d/clear-translations", job.ID)

	rec := testutil.DoRequestWithRole(t, router, "POST", path, map[string]any{"scope": "all"}, ling.ID, tenantID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	rec = testutil.DoRequest(t, router, "POST", path, map[string]any{"scope": "all"}, ownerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	post, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if post.Status == "completed" {
		t.Errorf("job status = %q after its workflow reopened, want it open again", post.Status)
	}
}

// Clearing a finished workflow starts it again from the first step, so the
// comment a step received in the round before is gone: the steps API returns
// no hand-off note on any step.
func TestIntegrationClearTranslations_DropsHandOffNotes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ClearNoteCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@clear-note.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Clear Note", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	steps := testutil.SeedWorkflow(t, q, project.SettingsProfileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translation", Assignee: &user.ID, Status: "completed"},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &user.ID, Status: "completed"},
	)
	if _, err := pool.Exec(ctx, "UPDATE job_step_assignments SET handoff_note = 'Check section 2' WHERE id = $1", steps[1]); err != nil {
		t.Fatalf("set note: %v", err)
	}

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", job.ID),
		map[string]any{"scope": "all"}, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	after, err := q.ListJobStepAssignments(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].Status != "in_progress" {
		t.Fatalf("workflow not reopened from the first step: %+v", after)
	}
	for i, s := range after {
		if s.HandoffNote != nil {
			t.Errorf("step %d (%s) keeps hand-off note %q after the clear", i, s.Status, *s.HandoffNote)
		}
	}
}
