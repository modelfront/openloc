package worker_test

// A pre-translation that asked for MT and found no engine for the job's target
// language does not count as done: the Pre-translation step stays open, the
// job carries the reason, and the finished event says MT was skipped. A project
// with no MT router at all, a run with nothing left for MT, or one that never
// asked for MT completes as usual, and the next run replaces the reason.

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
	"openloc/internal/worker"
	"openloc/internal/workflow"
)

func TestIntegrationPretranslateWithoutMTEngine(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "NoMTCorp")
	uploader := testutil.SeedUser(t, q, tenant.ID, "up@nomt.test", "pass")

	newHandler := func(outcome pipeline.TranslateOutcome) *worker.Handler {
		svc := workflow.NewService(q, pipeline.NewPgxTxRunner(pool), nil).WithAIStep(&recordingEnqueuer{})
		return worker.NewHandler(&fakePipeline{outcome: outcome}, q).WithWorkflow(svc)
	}
	seedJob := func(t *testing.T, name string) (db.Project, db.Job) {
		t.Helper()
		proj := testutil.SeedProject(t, q, tenant.ID, name, "en", []string{"de"})
		f := testutil.SeedFile(t, q, proj.ID, name+".txt", db.FileFormatTxt)
		job := testutil.SeedJob(t, q, f.ID, "de")
		seedAIWorkflow(t, q, proj.SettingsProfileID, job.ID, []stepSpec{
			{name: "Pre-translation", stepType: "pretranslate"},
		})
		return proj, job
	}
	reasonOf := func(t *testing.T, jobID int64) *string {
		t.Helper()
		got, err := q.GetJob(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		return got.MtSkipReason
	}

	for _, reason := range []pipeline.PhaseSkipReason{pipeline.PhaseSkipNoRoute, pipeline.PhaseSkipNoProvider} {
		t.Run("MT skipped for "+string(reason)+" leaves the step open", func(t *testing.T) {
			proj, job := seedJob(t, "skip-"+string(reason))
			h := newHandler(pipeline.TranslateOutcome{MTSkipReason: reason})
			if err := runPipeline(t, h, tenant.ID, job, &uploader.ID); err != nil {
				t.Fatalf("HandlePipeline: %v", err)
			}

			if rows := assignmentStatuses(t, q, job.ID); rows[0].Status != "in_progress" {
				t.Errorf("Pre-translation step = %q, want in_progress", rows[0].Status)
			}
			got, err := q.GetJob(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "in_progress" {
				t.Errorf("job status = %q, want in_progress", got.Status)
			}
			if got.MtSkipReason == nil || *got.MtSkipReason != string(reason) {
				t.Errorf("job mt_skip_reason = %v, want %q", got.MtSkipReason, reason)
			}
			details := findEventForFile(t, q, proj.ID, tenant.ID, "job.pipeline_completed", "skip-"+string(reason)+".txt")
			if details["mt_skipped"] != string(reason) {
				t.Errorf("pipeline_completed details = %v, want mt_skipped=%q", details, reason)
			}
			if ev := lookupEventForFile(t, q, proj.ID, tenant.ID, "workflow.step_auto_completed", "skip-"+string(reason)+".txt"); ev != nil {
				t.Errorf("no auto-complete expected, got %v", ev)
			}
		})
	}

	t.Run("no MT router at all is not missing MT: nothing stored, step completes", func(t *testing.T) {
		_, job := seedJob(t, "norouter")
		h := newHandler(pipeline.TranslateOutcome{MTSkipReason: pipeline.PhaseSkipNoAssignment})
		if err := runPipeline(t, h, tenant.ID, job, &uploader.ID); err != nil {
			t.Fatalf("HandlePipeline: %v", err)
		}
		if r := reasonOf(t, job.ID); r != nil {
			t.Errorf("mt_skip_reason = %q, want NULL", *r)
		}
		if rows := assignmentStatuses(t, q, job.ID); rows[0].Status != "completed" {
			t.Errorf("Pre-translation step = %q, want completed", rows[0].Status)
		}
	})

	t.Run("MT with nothing left to translate completes the step", func(t *testing.T) {
		_, job := seedJob(t, "nosegs")
		h := newHandler(pipeline.TranslateOutcome{MTSkipReason: pipeline.PhaseSkipNoSegments})
		if err := runPipeline(t, h, tenant.ID, job, &uploader.ID); err != nil {
			t.Fatalf("HandlePipeline: %v", err)
		}
		if rows := assignmentStatuses(t, q, job.ID); rows[0].Status != "completed" {
			t.Errorf("Pre-translation step = %q, want completed", rows[0].Status)
		}
		if r := reasonOf(t, job.ID); r != nil {
			t.Errorf("mt_skip_reason = %q, want NULL", *r)
		}
	})

	t.Run("a later run with an engine clears the reason and completes the step", func(t *testing.T) {
		_, job := seedJob(t, "fixed")
		if err := runPipeline(t, newHandler(pipeline.TranslateOutcome{MTSkipReason: pipeline.PhaseSkipNoRoute}), tenant.ID, job, &uploader.ID); err != nil {
			t.Fatalf("first run: %v", err)
		}
		if r := reasonOf(t, job.ID); r == nil {
			t.Fatal("first run should record the reason")
		}
		if err := runPipeline(t, newHandler(pipeline.TranslateOutcome{}), tenant.ID, job, &uploader.ID); err != nil {
			t.Fatalf("second run: %v", err)
		}
		if r := reasonOf(t, job.ID); r != nil {
			t.Errorf("mt_skip_reason after a good run = %q, want NULL", *r)
		}
		if rows := assignmentStatuses(t, q, job.ID); rows[0].Status != "completed" {
			t.Errorf("Pre-translation step = %q, want completed", rows[0].Status)
		}
	})
}
