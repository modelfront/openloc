package worker_test

// An `ai` workflow step completes itself once its QE+APE run has finished:
// the job advances to the next step, the next assignee
// is taken from the blueprint, a following AI-running step is chained, and a
// hybrid step is left for its human. Real DB via testutil.
//
// Anti-tautology: remove the autoCompleteAIStep call from HandleAIStep and
// every "advanced" assertion below fails on the step still being in_progress.

import (
	"context"
	"errors"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
	"openloc/internal/worker"
	"openloc/internal/workflow"
)

type recordingEnqueuer struct{ jobs []int64 }

func (r *recordingEnqueuer) EnqueueAIStep(jobID, _ int64, _ *int64) error {
	r.jobs = append(r.jobs, jobID)
	return nil
}

// stepSpec is one blueprint step for seedAIWorkflow.
type stepSpec struct {
	name, stepType string
	router         *int64
	assignee       *int64
}

// seedAIWorkflow writes the blueprint steps onto the project's profile and
// materializes a job sitting on step 0 (in_progress, unassigned — an ai step
// has nobody). Returns the assignment ids in step order.
func seedAIWorkflow(t *testing.T, q db.Querier, profileID, jobID int64, specs []stepSpec) []int64 {
	t.Helper()
	ctx := context.Background()
	ids := make([]int64, 0, len(specs))
	for i, sp := range specs {
		ws, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: profileID, Name: sp.name, StepOrder: int32(i), RoleRequired: "linguist",
			AiRouterID: sp.router, StepType: sp.stepType, DefaultAssignee: sp.assignee,
		})
		if err != nil {
			t.Fatalf("create step %q: %v", sp.name, err)
		}
		status := "pending"
		if i == 0 {
			status = "in_progress"
		}
		a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID: jobID, WorkflowStepID: ws.ID, Status: status,
		})
		if err != nil {
			t.Fatalf("create assignment %q: %v", sp.name, err)
		}
		ids = append(ids, a.ID)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &ids[0]}); err != nil {
		t.Fatalf("set current step: %v", err)
	}
	return ids
}

func runAIStep(t *testing.T, h *worker.Handler, tenantID, jobID int64, actor *int64) {
	t.Helper()
	task := pipelineTask(t, worker.TaskAIStep, worker.PipelinePayload{TenantID: tenantID, JobID: jobID, TriggeredBy: actor})
	if err := h.HandleAIStep(context.Background(), task); err != nil {
		t.Fatalf("HandleAIStep: %v", err)
	}
}

func assignmentStatuses(t *testing.T, q db.Querier, jobID int64) []db.ListJobStepAssignmentsRow {
	t.Helper()
	rows, err := q.ListJobStepAssignments(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestIntegrationAIStepAutoCompletes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "AutoCorp")
	uploader := testutil.SeedUser(t, q, tenant.ID, "up@auto.test", "pass")
	reviewer := testutil.SeedUserWithRole(t, q, tenant.ID, "rev@auto.test", "pass", "linguist")
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "APE", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	router := testutil.SeedRouterForEngine(t, q, tenant.ID, engine.ID)
	project := testutil.SeedProject(t, q, tenant.ID, "Auto", "en", []string{"de"})

	newJob := func(name string) db.Job {
		f := testutil.SeedFile(t, q, project.ID, name, db.FileFormatTxt)
		return testutil.SeedJob(t, q, f.ID, "de")
	}
	newHandler := func(res pipeline.AIStepResult) (*worker.Handler, *recordingEnqueuer) {
		enq := &recordingEnqueuer{}
		svc := workflow.NewService(q, pipeline.NewPgxTxRunner(pool), nil).WithAIStep(enq)
		return worker.NewHandler(&fakePipeline{aiResult: res}, q).WithWorkflow(svc), enq
	}

	t.Run("ai step advances to the human step with its default assignee", func(t *testing.T) {
		job := newJob("a.txt")
		ids := seedAIWorkflow(t, q, project.SettingsProfileID, job.ID, []stepSpec{
			{name: "Auto APE", stepType: "ai", router: &router},
			{name: "Review", stepType: "human", assignee: &reviewer.ID},
		})
		h, enq := newHandler(pipeline.AIStepResult{Total: 10, AutoApproved: 7, NeedsReview: 3})
		runAIStep(t, h, tenant.ID, job.ID, &uploader.ID)

		rows := assignmentStatuses(t, q, job.ID)
		if rows[0].Status != "completed" {
			t.Errorf("ai step should be completed, got %q", rows[0].Status)
		}
		if rows[1].Status != "in_progress" || rows[1].AssignedTo == nil || *rows[1].AssignedTo != reviewer.ID {
			t.Errorf("review step should be in_progress on the reviewer, got %+v", rows[1])
		}
		got, err := q.GetJob(ctx, job.ID)
		if err != nil || got.CurrentStepID == nil || *got.CurrentStepID != ids[1] {
			t.Errorf("job should sit on the review assignment %d, got %v (err=%v)", ids[1], got.CurrentStepID, err)
		}
		if len(enq.jobs) != 0 {
			t.Errorf("a human next step must not enqueue an AI run, got %v", enq.jobs)
		}
		details := findEventForFile(t, q, project.ID, tenant.ID, "workflow.step_auto_completed", "a.txt")
		if details["from_step"] != "Auto APE" || details["to_step"] != "Review" || details["auto_approved"] != float64(7) {
			t.Errorf("unexpected auto-complete event details: %v", details)
		}
	})

	t.Run("a following AI-running step is chained", func(t *testing.T) {
		job := newJob("b.txt")
		seedAIWorkflow(t, q, project.SettingsProfileID, job.ID, []stepSpec{
			{name: "Auto APE", stepType: "ai", router: &router},
			{name: "AI review", stepType: "hybrid", router: &router, assignee: &reviewer.ID},
		})
		h, enq := newHandler(pipeline.AIStepResult{})
		runAIStep(t, h, tenant.ID, job.ID, &uploader.ID)
		if len(enq.jobs) != 1 || enq.jobs[0] != job.ID {
			t.Fatalf("expected one chained AI enqueue for job %d, got %v", job.ID, enq.jobs)
		}
		rows := assignmentStatuses(t, q, job.ID)
		if rows[1].Status != "in_progress" || rows[1].AssignedTo == nil {
			t.Errorf("hybrid step should be in_progress with its assignee, got %+v", rows[1])
		}
	})

	t.Run("ai step as the last step completes the workflow", func(t *testing.T) {
		job := newJob("c.txt")
		seedAIWorkflow(t, q, project.SettingsProfileID, job.ID, []stepSpec{
			{name: "Translation", stepType: "human"},
			{name: "Auto APE", stepType: "ai", router: &router},
		})
		// Move the job onto the ai step by hand.
		rows := assignmentStatuses(t, q, job.ID)
		if err := q.CompleteStepAssignment(ctx, rows[0].ID); err != nil {
			t.Fatal(err)
		}
		if err := q.StartStepAssignment(ctx, rows[1].ID); err != nil {
			t.Fatal(err)
		}
		if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &rows[1].ID}); err != nil {
			t.Fatal(err)
		}
		h, _ := newHandler(pipeline.AIStepResult{})
		runAIStep(t, h, tenant.ID, job.ID, nil)
		got, err := q.GetJob(ctx, job.ID)
		if err != nil || got.CurrentStepID != nil {
			t.Errorf("workflow should be complete (no current step), got %v (err=%v)", got.CurrentStepID, err)
		}
		// The ai step was the final step: with no human to complete the job, the
		// worker must flip the job to completed itself, or it sits forever
		// in_progress and its project never auto-completes.
		if got.Status != "completed" {
			t.Errorf("a completed workflow ending in an ai step should complete the job, got %q", got.Status)
		}
		details := findEventForFile(t, q, project.ID, tenant.ID, "workflow.step_auto_completed", "c.txt")
		if details["workflow_completed"] != true {
			t.Errorf("expected workflow_completed=true, got %v", details)
		}
	})

	t.Run("a final ai step auto-completes the project", func(t *testing.T) {
		// Own project so the auto-complete rule (all jobs done → completed)
		// isn't held back by the other subtests' unfinished jobs.
		proj := testutil.SeedProject(t, q, tenant.ID, "SoloAI", "en", []string{"de"})
		f := testutil.SeedFile(t, q, proj.ID, "solo.txt", db.FileFormatTxt)
		job := testutil.SeedJob(t, q, f.ID, "de")
		seedAIWorkflow(t, q, proj.SettingsProfileID, job.ID, []stepSpec{
			{name: "Auto APE", stepType: "ai", router: &router},
		})
		h, _ := newHandler(pipeline.AIStepResult{})
		runAIStep(t, h, tenant.ID, job.ID, nil)

		got, err := q.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "completed" {
			t.Errorf("job should be completed after its final ai step, got %q", got.Status)
		}
		proj2, err := q.GetProjectByID(ctx, proj.ID)
		if err != nil {
			t.Fatal(err)
		}
		if proj2.Status != db.ProjectStatusCompleted {
			t.Errorf("project auto-complete should flip to completed, got %q", proj2.Status)
		}
	})

	t.Run("a skipped run still completes the ai step", func(t *testing.T) {
		job := newJob("d.txt")
		seedAIWorkflow(t, q, project.SettingsProfileID, job.ID, []stepSpec{
			{name: "Auto APE", stepType: "ai", router: &router},
			{name: "Review", stepType: "human"},
		})
		h, _ := newHandler(pipeline.AIStepResult{SkipReason: pipeline.PhaseSkipNoExtensions})
		runAIStep(t, h, tenant.ID, job.ID, &uploader.ID)
		rows := assignmentStatuses(t, q, job.ID)
		if rows[0].Status != "completed" || rows[1].Status != "in_progress" {
			t.Errorf("skip should still advance: got %q / %q", rows[0].Status, rows[1].Status)
		}
		if rows[1].AssignedTo != nil {
			t.Errorf("no default assignee → unassigned, got %v", *rows[1].AssignedTo)
		}
		details := findEventForFile(t, q, project.ID, tenant.ID, "workflow.step_auto_completed", "d.txt")
		if details["skip_reason"] != "no_extensions" {
			t.Errorf("expected skip_reason in details, got %v", details)
		}
	})

	t.Run("a hybrid step is left for its human", func(t *testing.T) {
		job := newJob("e.txt")
		seedAIWorkflow(t, q, project.SettingsProfileID, job.ID, []stepSpec{
			{name: "AI review", stepType: "hybrid", router: &router},
			{name: "Proofread", stepType: "human"},
		})
		h, _ := newHandler(pipeline.AIStepResult{AutoApproved: 5})
		runAIStep(t, h, tenant.ID, job.ID, &uploader.ID)
		rows := assignmentStatuses(t, q, job.ID)
		if rows[0].Status != "in_progress" || rows[1].Status != "pending" {
			t.Errorf("hybrid step must not auto-advance: got %q / %q", rows[0].Status, rows[1].Status)
		}
	})

	t.Run("a failed run leaves the ai step open and the job failed", func(t *testing.T) {
		job := newJob("g.txt")
		seedAIWorkflow(t, q, project.SettingsProfileID, job.ID, []stepSpec{
			{name: "Auto APE", stepType: "ai", router: &router},
			{name: "Review", stepType: "human", assignee: &reviewer.ID},
		})
		svc := workflow.NewService(q, pipeline.NewPgxTxRunner(pool), nil).WithAIStep(&recordingEnqueuer{})
		h := worker.NewHandler(&fakePipeline{err: errors.New("predict down")}, q).WithWorkflow(svc)
		task := pipelineTask(t, worker.TaskAIStep, worker.PipelinePayload{TenantID: tenant.ID, JobID: job.ID, TriggeredBy: &uploader.ID})
		if err := h.HandleAIStep(ctx, task); err == nil {
			t.Fatal("HandleAIStep must return the pipeline error so asynq retries it")
		}
		rows := assignmentStatuses(t, q, job.ID)
		if rows[0].Status != "in_progress" || rows[1].Status != "pending" {
			t.Errorf("a failed run must not advance: got %q / %q", rows[0].Status, rows[1].Status)
		}
		if ev := lookupEventForFile(t, q, project.ID, tenant.ID, "workflow.step_auto_completed", "g.txt"); ev != nil {
			t.Errorf("no auto-complete event on failure, got %v", ev)
		}
		got, err := q.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "failed" || got.ErrorMessage == nil || !strings.Contains(*got.ErrorMessage, "predict down") {
			t.Errorf("job should be failed with the pipeline error, got status=%q error=%v", got.Status, got.ErrorMessage)
		}
		if got.CurrentStepID == nil {
			t.Errorf("job should still sit on the ai step %d, got no current step", rows[0].ID)
		} else if *got.CurrentStepID != rows[0].ID {
			t.Errorf("job should still sit on the ai step %d, got %d", rows[0].ID, *got.CurrentStepID)
		}
	})

	t.Run("no workflow service leaves the step open", func(t *testing.T) {
		job := newJob("f.txt")
		seedAIWorkflow(t, q, project.SettingsProfileID, job.ID, []stepSpec{
			{name: "Auto APE", stepType: "ai", router: &router},
		})
		h := worker.NewHandler(&fakePipeline{}, q)
		runAIStep(t, h, tenant.ID, job.ID, nil)
		if rows := assignmentStatuses(t, q, job.ID); rows[0].Status != "in_progress" {
			t.Errorf("without WithWorkflow the step stays in_progress, got %q", rows[0].Status)
		}
	})
}
