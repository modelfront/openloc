package worker_test

// A task payload names a tenant, a job and (for translation) a file. The
// handlers refuse, before writing anything, a job that belongs to another
// tenant or to another file. Real DB via testutil.
//
// Anti-tautology: the "own job" case runs the same handler on the same job and
// must change its status, so the rejected cases fail on the guard, not on
// something the setup lacks.

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/worker"
)

func TestIntegrationWorker_RejectsJobOfAnotherTenantOrFile(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	owner := testutil.SeedTenant(t, q, "JobOwnerCorp")
	other := testutil.SeedTenant(t, q, "OtherCorp")
	project := testutil.SeedProject(t, q, owner.ID, "Owned", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "a.txt", db.FileFormatTxt)
	sibling := testutil.SeedFile(t, q, project.ID, "b.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	type handlerFunc func(*worker.Handler, context.Context, *asynq.Task) error
	pipelineH := (*worker.Handler).HandlePipeline
	translateH := (*worker.Handler).HandleTranslate
	aiStepH := (*worker.Handler).HandleAIStep

	rejected := []struct {
		name     string
		taskType string
		handle   handlerFunc
		tenantID int64
		fileID   int64
	}{
		{"pipeline, other tenant", worker.TaskPipeline, pipelineH, other.ID, file.ID},
		{"translate, other tenant", worker.TaskTranslate, translateH, other.ID, file.ID},
		{"ai step, other tenant", worker.TaskAIStep, aiStepH, other.ID, 0},
		{"pipeline, other file", worker.TaskPipeline, pipelineH, owner.ID, sibling.ID},
		{"translate, other file", worker.TaskTranslate, translateH, owner.ID, sibling.ID},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			before := getJob(t, q, job.ID)
			fp := &fakePipeline{}
			task := pipelineTask(t, tc.taskType, worker.PipelinePayload{
				FileID: tc.fileID, SourceLanguage: "en", TargetLanguage: "de",
				TenantID: tc.tenantID, JobID: job.ID,
			})

			err := tc.handle(worker.NewHandler(fp, q), ctx, task)
			if !errors.Is(err, asynq.SkipRetry) {
				t.Fatalf("handler error = %v, want a SkipRetry rejection", err)
			}
			if fp.calls != 0 {
				t.Errorf("pipeline ran %d times on a rejected payload", fp.calls)
			}
			after := getJob(t, q, job.ID)
			if after.Status != before.Status || after.CurrentStep != before.CurrentStep || !equalPtr(after.ErrorMessage, before.ErrorMessage) {
				t.Errorf("job changed: status %q→%q, step %q→%q", before.Status, after.Status, before.CurrentStep, after.CurrentStep)
			}
			if _, err := q.GetJobTiming(ctx, db.GetJobTimingParams{JobID: job.ID, TenantID: tc.tenantID}); !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("a timing row was written for the rejected payload (err=%v)", err)
			}
		})
	}

	t.Run("own job runs", func(t *testing.T) {
		fp := &fakePipeline{}
		task := pipelineTask(t, worker.TaskPipeline, worker.PipelinePayload{
			FileID: file.ID, SourceLanguage: "en", TargetLanguage: "de",
			TenantID: owner.ID, JobID: job.ID,
		})
		if err := worker.NewHandler(fp, q).HandlePipeline(ctx, task); err != nil {
			t.Fatalf("HandlePipeline: %v", err)
		}
		if fp.calls != 1 {
			t.Errorf("pipeline ran %d times, want 1", fp.calls)
		}
		if got := getJob(t, q, job.ID).CurrentStep; got != "Pipeline complete" {
			t.Errorf("current_step = %q, want %q", got, "Pipeline complete")
		}
	})

	// A job deleted while its task sat in the queue is not a foreign job: the
	// task finishes without running and without an error to retry.
	t.Run("deleted job is dropped, not rejected", func(t *testing.T) {
		gone := testutil.SeedJob(t, q, file.ID, "fr")
		if err := q.DeleteJob(ctx, gone.ID); err != nil {
			t.Fatalf("DeleteJob: %v", err)
		}
		fp := &fakePipeline{}
		task := pipelineTask(t, worker.TaskPipeline, worker.PipelinePayload{
			FileID: file.ID, SourceLanguage: "en", TargetLanguage: "fr",
			TenantID: owner.ID, JobID: gone.ID,
		})
		if err := worker.NewHandler(fp, q).HandlePipeline(ctx, task); err != nil {
			t.Fatalf("HandlePipeline on a deleted job: %v", err)
		}
		if fp.calls != 0 {
			t.Errorf("pipeline ran %d times on a deleted job", fp.calls)
		}
	})
}

func getJob(t *testing.T, q db.Querier, id int64) db.Job {
	t.Helper()
	job, err := q.GetJob(context.Background(), id)
	if err != nil {
		t.Fatalf("GetJob(%d): %v", id, err)
	}
	return job
}

func equalPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
