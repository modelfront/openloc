package worker_test

// A Pre-translation step completes itself once its pre-translation pipeline
// has finished: the task is completed, the job is completed (nobody is left to
// POST /complete), and nothing is written to TM. Real DB
// via testutil.
//
// Anti-tautology: remove the autoCompleteStep call from HandlePipeline and the
// "completed" assertions fail on the step still being in_progress.

import (
	"context"
	"errors"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
	"openloc/internal/worker"
	"openloc/internal/workflow"
)

func runPipeline(t *testing.T, h *worker.Handler, tenantID int64, job db.Job, actor *int64) error {
	t.Helper()
	payload := worker.PipelinePayload{
		TenantID: tenantID, JobID: job.ID, FileID: job.FileID,
		SourceLanguage: "en", TargetLanguage: job.TargetLanguage,
		Phases:      pipeline.TranslatePhases{UseTM: true, UseMT: true},
		TriggeredBy: actor,
	}
	return h.HandlePipeline(context.Background(), pipelineTask(t, worker.TaskPipeline, payload))
}

func TestIntegrationPretranslateStepAutoCompletes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "PretranslateCorp")
	uploader := testutil.SeedUser(t, q, tenant.ID, "up@pre.test", "pass")

	newHandler := func(p *fakePipeline) *worker.Handler {
		svc := workflow.NewService(q, pipeline.NewPgxTxRunner(pool), nil).WithAIStep(&recordingEnqueuer{})
		return worker.NewHandler(p, q).WithWorkflow(svc)
	}

	t.Run("the step and the job complete, without TM write-back", func(t *testing.T) {
		// Own project so the auto-complete rule sees only this job.
		proj := testutil.SeedProject(t, q, tenant.ID, "PreOnly", "en", []string{"de"})
		f := testutil.SeedFile(t, q, proj.ID, "pre.txt", db.FileFormatTxt)
		job := testutil.SeedJob(t, q, f.ID, "de")
		seedAIWorkflow(t, q, proj.SettingsProfileID, job.ID, []stepSpec{
			{name: "Pre-translation", stepType: "pretranslate"},
		})
		h := newHandler(&fakePipeline{})
		if err := runPipeline(t, h, tenant.ID, job, &uploader.ID); err != nil {
			t.Fatalf("HandlePipeline: %v", err)
		}

		rows := assignmentStatuses(t, q, job.ID)
		if rows[0].Status != "completed" {
			t.Errorf("Pre-translation step should be completed, got %q", rows[0].Status)
		}
		got, err := q.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "completed" || got.CurrentStepID != nil {
			t.Errorf("job should be completed with no current step, got status=%q step=%v", got.Status, got.CurrentStepID)
		}
		details := findEventForFile(t, q, proj.ID, tenant.ID, "workflow.step_auto_completed", "pre.txt")
		if details["from_step"] != "Pre-translation" || details["step_type"] != "pretranslate" || details["workflow_completed"] != true {
			t.Errorf("unexpected auto-complete event details: %v", details)
		}
		p2, err := q.GetProjectByID(ctx, proj.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p2.Status != db.ProjectStatusCompleted {
			t.Errorf("project auto-complete should flip to completed, got %q", p2.Status)
		}
	})

	t.Run("a human first step is left for its human", func(t *testing.T) {
		proj := testutil.SeedProject(t, q, tenant.ID, "Human", "en", []string{"de"})
		f := testutil.SeedFile(t, q, proj.ID, "human.txt", db.FileFormatTxt)
		job := testutil.SeedJob(t, q, f.ID, "de")
		seedAIWorkflow(t, q, proj.SettingsProfileID, job.ID, []stepSpec{
			{name: "Translation", stepType: "human", assignee: &uploader.ID},
		})
		h := newHandler(&fakePipeline{})
		if err := runPipeline(t, h, tenant.ID, job, &uploader.ID); err != nil {
			t.Fatalf("HandlePipeline: %v", err)
		}
		if rows := assignmentStatuses(t, q, job.ID); rows[0].Status != "in_progress" {
			t.Errorf("a human step must not auto-complete, got %q", rows[0].Status)
		}
		got, err := q.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "in_progress" {
			t.Errorf("job should stay in_progress, got %q", got.Status)
		}
	})

	t.Run("a failed pre-translation leaves the step open and the job failed", func(t *testing.T) {
		proj := testutil.SeedProject(t, q, tenant.ID, "Failed", "en", []string{"de"})
		f := testutil.SeedFile(t, q, proj.ID, "fail.txt", db.FileFormatTxt)
		job := testutil.SeedJob(t, q, f.ID, "de")
		seedAIWorkflow(t, q, proj.SettingsProfileID, job.ID, []stepSpec{
			{name: "Pre-translation", stepType: "pretranslate"},
		})
		h := newHandler(&fakePipeline{err: errors.New("mt down")})
		if err := runPipeline(t, h, tenant.ID, job, &uploader.ID); err == nil {
			t.Fatal("HandlePipeline must return the pipeline error so asynq retries it")
		}
		if rows := assignmentStatuses(t, q, job.ID); rows[0].Status != "in_progress" {
			t.Errorf("a failed run must not complete the step, got %q", rows[0].Status)
		}
		if ev := lookupEventForFile(t, q, proj.ID, tenant.ID, "workflow.step_auto_completed", "fail.txt"); ev != nil {
			t.Errorf("no auto-complete event on failure, got %v", ev)
		}
		got, err := q.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "failed" {
			t.Errorf("job should be failed, got %q", got.Status)
		}
	})
}
