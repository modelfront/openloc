package worker_test

// A queued Apply TM / Apply MT (Phases.Manual — the bulk bar's) does what the
// job's own button does in the request: the Pre-translation step completes
// only after a whole-job MT run that found an engine, and a phase with nothing
// configured fails the job with the button's message. An automatic run (the
// upload hook) completes the step after any phases.
//
// Skipped with -short.

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
	"openloc/internal/worker"
	"openloc/internal/workflow"
)

func TestIntegrationManualPhasesInTheWorker(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "ManualPhasesCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "u@manual.test", "pass")

	run := func(t *testing.T, name string, phases pipeline.TranslatePhases, outcome pipeline.TranslateOutcome) db.Job {
		t.Helper()
		proj := testutil.SeedProject(t, q, tenant.ID, name, "en", []string{"de"})
		f := testutil.SeedFile(t, q, proj.ID, name+".txt", db.FileFormatTxt)
		job := testutil.SeedJob(t, q, f.ID, "de")
		seedAIWorkflow(t, q, proj.SettingsProfileID, job.ID, []stepSpec{
			{name: "Pre-translation", stepType: "pretranslate"},
			{name: "Review", stepType: "human", assignee: &user.ID},
		})
		svc := workflow.NewService(q, pipeline.NewPgxTxRunner(pool), nil).WithAIStep(&recordingEnqueuer{})
		h := worker.NewHandler(&fakePipeline{outcome: outcome}, q).WithWorkflow(svc)
		payload := worker.PipelinePayload{
			TenantID: tenant.ID, JobID: job.ID, FileID: job.FileID,
			SourceLanguage: "en", TargetLanguage: "de", Phases: phases, TriggeredBy: &user.ID,
		}
		if err := h.HandlePipeline(ctx, pipelineTask(t, worker.TaskPipeline, payload)); err != nil {
			t.Fatalf("HandlePipeline: %v", err)
		}
		return job
	}
	firstStep := func(t *testing.T, job db.Job) string {
		t.Helper()
		return assignmentStatuses(t, q, job.ID)[0].Status
	}

	t.Run("a queued Apply TM leaves the Pre-translation step open", func(t *testing.T) {
		job := run(t, "ManualTM", pipeline.TranslatePhases{UseTM: true, Manual: true}, pipeline.TranslateOutcome{})
		if got := firstStep(t, job); got != "in_progress" {
			t.Errorf("Pre-translation step = %q, want in_progress (Apply MT must still be possible)", got)
		}
	})

	t.Run("a queued Apply MT completes it", func(t *testing.T) {
		job := run(t, "ManualMT", pipeline.TranslatePhases{UseMT: true, Manual: true}, pipeline.TranslateOutcome{})
		if got := firstStep(t, job); got != "completed" {
			t.Errorf("Pre-translation step = %q, want completed", got)
		}
	})

	t.Run("an automatic TM-only run completes it", func(t *testing.T) {
		job := run(t, "AutoTM", pipeline.TranslatePhases{UseTM: true}, pipeline.TranslateOutcome{})
		if got := firstStep(t, job); got != "completed" {
			t.Errorf("Pre-translation step = %q, want completed", got)
		}
	})

	t.Run("a queued Apply TM with no TM fails the job with the button's message", func(t *testing.T) {
		job := run(t, "ManualNoTM", pipeline.TranslatePhases{UseTM: true, Manual: true},
			pipeline.TranslateOutcome{TMSkipReason: pipeline.PhaseSkipNoAssignment})
		got, err := q.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "failed" || got.ErrorMessage == nil || !strings.Contains(*got.ErrorMessage, "No TM configured for en → de") {
			t.Errorf("job = %q / %v, want failed with the no-TM message", got.Status, got.ErrorMessage)
		}
		if st := firstStep(t, job); st != "in_progress" {
			t.Errorf("Pre-translation step = %q, want in_progress", st)
		}
	})

	// The project page polls a job only while it reads as running
	// (IsPipelineRunning). A state that reads as finished but not failed,
	// written just before the failure, stops the polling there, and the page
	// never shows the failure.
	t.Run("a queued Apply TM with no TM goes from running straight to failed", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `
			CREATE TABLE test_job_states (seq bigserial, job_id bigint, status text, current_step text);
			CREATE FUNCTION test_log_job_state() RETURNS trigger AS $$
			BEGIN
				INSERT INTO test_job_states (job_id, status, current_step) VALUES (NEW.id, NEW.status, NEW.current_step);
				RETURN NEW;
			END $$ LANGUAGE plpgsql;
			CREATE TRIGGER test_log_job_state AFTER UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION test_log_job_state();`); err != nil {
			t.Fatal(err)
		}
		// The package's tests share one database: leave nothing behind.
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_log_job_state ON jobs;
				DROP FUNCTION IF EXISTS test_log_job_state();
				DROP TABLE IF EXISTS test_job_states;`); err != nil {
				t.Errorf("drop trigger: %v", err)
			}
		})
		job := run(t, "ManualNoTMStates", pipeline.TranslatePhases{UseTM: true, Manual: true},
			pipeline.TranslateOutcome{TMSkipReason: pipeline.PhaseSkipNoAssignment})
		rows, err := pool.Query(ctx, `SELECT status, coalesce(current_step, '') FROM test_job_states WHERE job_id = $1 ORDER BY seq`, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		// From the run's start: the seeding before it writes the job too.
		seen, started := 0, false
		for rows.Next() {
			var status, step string
			if err := rows.Scan(&status, &step); err != nil {
				t.Fatal(err)
			}
			started = started || pipeline.IsPipelineRunning(status, step)
			if !started {
				continue
			}
			seen++
			if status != "failed" && !pipeline.IsPipelineRunning(status, step) {
				t.Errorf("job read as finished but not failed: %q / %q", status, step)
			}
		}
		if seen == 0 {
			t.Fatal("no job state was logged")
		}
	})
}
