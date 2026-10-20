package job_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// rerunFixture is a German job with one segment whose first task is in
// progress, with a linguist assigned to a later step so the job group's
// linguist gate passes.
type rerunFixture struct {
	pool          *pgxpool.Pool
	q             db.Querier
	ownerID       int64
	linguist      int64
	tenantID      int64
	projectID     int64
	fileID        int64
	jobID         int64
	translationID int64
}

// newRerunFixture builds the job. firstStepType is the first step's type:
// "" for a human step, "ai" or "hybrid" for a step with an AI router.
func newRerunFixture(t *testing.T, prefix, firstStepType string) rerunFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ownerID, tenantID := testutil.RegisterUser(t, q, prefix)
	ling := testutil.SeedUserWithRole(t, q, tenantID, prefix+"-ling@test.com", "pw", "linguist")
	proj := testutil.SeedProject(t, q, tenantID, prefix+" project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, proj.ID, prefix+".txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 0)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	first := testutil.WorkflowStepSpec{Name: "Translate", Assignee: &ling.ID, Status: "in_progress"}
	switch firstStepType {
	case "ai":
		router := testutil.SeedAIRouter(t, q, tenantID, prefix+" router", nil)
		first = testutil.WorkflowStepSpec{Name: "QE", StepType: "ai", RouterID: &router.ID, Status: "in_progress"}
	case "hybrid":
		router := testutil.SeedAIRouter(t, q, tenantID, prefix+" router", nil)
		first = testutil.WorkflowStepSpec{Name: "Post-edit", StepType: "hybrid", RouterID: &router.ID, Assignee: &ling.ID, Status: "in_progress"}
	}
	testutil.SeedWorkflow(t, q, proj.SettingsProfileID, job.ID, first,
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &ling.ID})
	return rerunFixture{pool: pool, q: q, ownerID: ownerID, linguist: ling.ID, tenantID: tenantID,
		projectID: proj.ID, fileID: file.ID, jobID: job.ID, translationID: tr.ID}
}

// fail marks the job failed in the run the label names.
func (f rerunFixture) fail(t *testing.T, label, msg string) {
	t.Helper()
	ctx := context.Background()
	if err := f.q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: f.jobID, Status: "in_progress", CurrentStep: label}); err != nil {
		t.Fatal(err)
	}
	if err := f.q.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: f.jobID, ErrorMessage: &msg}); err != nil {
		t.Fatal(err)
	}
}

func (f rerunFixture) rerun(t *testing.T, router http.Handler, userID, tenantID int64, role string) int {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/rerun", f.jobID), nil, userID, tenantID, role)
	return rec.Code
}

func TestIntegrationRerun_AIStep(t *testing.T) {
	f := newRerunFixture(t, "rerun-ai", "ai")
	enq := &testutil.RecordingEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(f.pool, enq)
	ctx := context.Background()
	f.fail(t, "AI step (QE): 3 / 9", "provider 503")

	if code := f.rerun(t, router, f.linguist, f.tenantID, "linguist"); code != http.StatusForbidden {
		t.Fatalf("linguist re-running the AI step: status %d, want 403", code)
	}
	if len(enq.AISteps) != 0 {
		t.Fatalf("a refused re-run must not enqueue")
	}

	if code := f.rerun(t, router, f.ownerID, f.tenantID, "owner"); code != http.StatusAccepted {
		t.Fatalf("manager re-run: status %d, want 202", code)
	}
	if len(enq.AISteps) != 1 || enq.AISteps[0].JobID != f.jobID || enq.AISteps[0].TriggeredBy == nil || *enq.AISteps[0].TriggeredBy != f.ownerID {
		t.Fatalf("AI-step enqueues = %+v, want one for job %d by %d", enq.AISteps, f.jobID, f.ownerID)
	}
	if len(enq.Pipelines) != 0 {
		t.Errorf("an AI-step re-run must not enqueue pre-translation")
	}
	job, err := f.q.GetJob(ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "queued" || job.ErrorMessage != nil || !pipeline.IsAIStepLabel(job.CurrentStep) {
		t.Errorf("after re-run: status=%q error=%v step=%q; want queued, cleared, AI-step label", job.Status, job.ErrorMessage, job.CurrentStep)
	}

	events := testutil.ProjectEvents(t, f.q, f.projectID, f.tenantID, "job.rerun_started")
	if len(events) != 1 {
		t.Fatalf("job.rerun_started events = %d, want 1", len(events))
	}
	if details := events[0].Data; details["phase"] != "ai_step" || details["error"] != "provider 503" {
		t.Errorf("rerun details = %v", details)
	}

	// The job is queued now, not failed: a second click has nothing to re-run.
	if code := f.rerun(t, router, f.ownerID, f.tenantID, "owner"); code != http.StatusConflict {
		t.Errorf("second re-run: status %d, want 409", code)
	}
}

func TestIntegrationRerun_PretranslationByAssignedLinguist(t *testing.T) {
	f := newRerunFixture(t, "rerun-pre", "")
	enq := &testutil.RecordingEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(f.pool, enq)
	f.fail(t, "MT: 5 / 10 segments", "MT provider 500")

	if code := f.rerun(t, router, f.linguist, f.tenantID, "linguist"); code != http.StatusAccepted {
		t.Fatalf("assigned linguist re-running pre-translation: status %d, want 202", code)
	}
	if len(enq.Pipelines) != 1 || enq.Pipelines[0].JobID != f.jobID || !reflect.DeepEqual(enq.Pipelines[0].Phases, pipeline.FullPipeline) {
		t.Fatalf("pipeline enqueues = %+v, want one full pre-translation for job %d", enq.Pipelines, f.jobID)
	}
	if len(enq.AISteps) != 0 {
		t.Errorf("a pre-translation re-run must not enqueue the AI step")
	}
}

func TestIntegrationRerun_Refusals(t *testing.T) {
	f := newRerunFixture(t, "rerun-refuse", "")
	ctx := context.Background()

	t.Run("not failed", func(t *testing.T) {
		router := testutil.NewTestRouterWithEnqueuer(f.pool, &testutil.RecordingEnqueuer{})
		if code := f.rerun(t, router, f.ownerID, f.tenantID, "owner"); code != http.StatusConflict {
			t.Errorf("status %d, want 409", code)
		}
	})

	f.fail(t, "MT: 5 / 10 segments", "MT provider 500")

	t.Run("another tenant", func(t *testing.T) {
		otherID, otherTenant := testutil.RegisterUser(t, f.q, "rerun-other")
		router := testutil.NewTestRouterWithEnqueuer(f.pool, &testutil.RecordingEnqueuer{})
		if code := f.rerun(t, router, otherID, otherTenant, "owner"); code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
	})

	t.Run("no background processing", func(t *testing.T) {
		router := testutil.NewTestRouter(f.pool)
		if code := f.rerun(t, router, f.ownerID, f.tenantID, "owner"); code != http.StatusServiceUnavailable {
			t.Errorf("status %d, want 503", code)
		}
	})

	t.Run("enqueue fails", func(t *testing.T) {
		router := testutil.NewTestRouterWithEnqueuer(f.pool, &testutil.RecordingEnqueuer{Err: errors.New("redis down")})
		if code := f.rerun(t, router, f.ownerID, f.tenantID, "owner"); code != http.StatusInternalServerError {
			t.Errorf("status %d, want 500", code)
		}
		job, err := f.q.GetJob(ctx, f.jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != "failed" || job.ErrorMessage == nil || !strings.Contains(*job.ErrorMessage, "MT provider 500") {
			t.Errorf("a failed enqueue must leave the task errored; status=%q error=%v", job.Status, job.ErrorMessage)
		}
	})
}

// The editor and the task matrix offer Re-run from the job's failed_phase, so
// both job endpoints carry it, and only on a failed job.
func TestIntegrationJobResponse_CarriesFailedPhase(t *testing.T) {
	f := newRerunFixture(t, "failed-phase", "")
	router := testutil.NewTestRouter(f.pool)
	phases := func(t *testing.T) (get, list any) {
		t.Helper()
		var one struct {
			Data map[string]any `json:"data"`
		}
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d", f.jobID), nil, f.ownerID, f.tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
			t.Fatal(err)
		}
		var all struct {
			Data []map[string]any `json:"data"`
		}
		rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/jobs", f.projectID), nil, f.ownerID, f.tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
			t.Fatal(err)
		}
		if len(all.Data) != 1 {
			t.Fatalf("project jobs = %d, want 1", len(all.Data))
		}
		return one.Data["failed_phase"], all.Data[0]["failed_phase"]
	}

	if get, list := phases(t); get != nil || list != nil {
		t.Errorf("a job that has not failed carries failed_phase: get=%v list=%v", get, list)
	}
	for _, c := range []struct{ label, want string }{
		{"MT: 5 / 10 segments", "pretranslation"},
		{"AI step (QE): 3 / 9", "ai_step"},
	} {
		f.fail(t, c.label, "boom")
		if get, list := phases(t); get != c.want || list != c.want {
			t.Errorf("failed in %q: failed_phase get=%v list=%v, want %q", c.label, get, list, c.want)
		}
	}
}

// Re-running pre-translation writes into the current task, so it has Apply
// TM/MT's gate: a job whose current task is past the first step is refused,
// even when the failure is an old pre-translation error.
func TestIntegrationRerun_PretranslationPastTheFirstStep(t *testing.T) {
	f := newRerunFixture(t, "rerun-step2", "")
	ctx := context.Background()
	steps, err := f.q.ListJobStepAssignments(ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.q.CompleteStepAssignment(ctx, steps[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE job_step_assignments SET status = 'in_progress' WHERE id = $1`, steps[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: f.jobID, CurrentStepID: &steps[1].ID}); err != nil {
		t.Fatal(err)
	}
	f.fail(t, "MT: 5 / 10 segments", "MT provider 500")
	enq := &testutil.RecordingEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(f.pool, enq)

	if code := f.rerun(t, router, f.ownerID, f.tenantID, "owner"); code != http.StatusForbidden {
		t.Fatalf("re-running pre-translation in the second step: status %d, want 403", code)
	}
	if len(enq.Pipelines) != 0 {
		t.Errorf("a refused re-run must not enqueue, got %+v", enq.Pipelines)
	}
	job, err := f.q.GetJob(ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "failed" {
		t.Errorf("a refused re-run left the job %q, want it still failed", job.Status)
	}
}
