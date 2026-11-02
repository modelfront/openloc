package job_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// failingEnqueuer is the worker's enqueuer when Redis refuses the task: it
// marks the job queued first, then fails. calls records the jobs it was given.
type failingEnqueuer struct {
	q     db.Querier
	calls []int64
}

func (e *failingEnqueuer) EnqueuePipeline(_ int64, _, _ string, jobID, _ int64, _ pipeline.TranslatePhases, _ *int64) error {
	e.calls = append(e.calls, jobID)
	if err := e.q.UpdateJobStatus(context.Background(), db.UpdateJobStatusParams{ID: jobID, Status: "queued", CurrentStep: "Queued for pre-translation"}); err != nil {
		return err
	}
	return errors.New("redis: connection refused")
}

func (failingEnqueuer) EnqueueAnalysis(int64, int64) error       { return nil }
func (failingEnqueuer) EnqueueAIStep(int64, int64, *int64) error { return nil }

// The bulk bar's Apply TM / Apply MT queue a job only under the checks of the
// job's own button: a running job and a job past the first workflow step are
// skipped, a completed task is reopened first, and a job the queue refuses is
// put back as it was.
func TestIntegrationProjectPretranslate_SelectionChecks(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	enq := &testutil.RecordingEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(pool, enq)
	tenant := testutil.SeedTenant(t, q, "PretranslateChecksCo")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@pretranslatechecks.test", "pass")

	type result struct {
		Queued  int `json:"queued"`
		Skipped int `json:"skipped"`
	}
	// newJob seeds a project whose workflow has two human steps, and one job
	// at the step `at` (0 or 1) with that task in `status`.
	newJob := func(t *testing.T, name string, at int, status string) (db.Project, db.Job, []int64) {
		t.Helper()
		project := testutil.SeedProject(t, q, tenant.ID, name, "en", []string{"de"})
		job := testutil.SeedJob(t, q, testutil.SeedFile(t, q, project.ID, name+".txt", db.FileFormatTxt).ID, "de")
		var assignments []int64
		for i, stepName := range []string{"Translation", "Review"} {
			ws, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
				SettingsProfileID: project.SettingsProfileID, Name: stepName, StepOrder: int32(i), RoleRequired: "linguist", StepType: "human",
			})
			if err != nil {
				t.Fatal(err)
			}
			st := "pending"
			if i < at {
				st = "completed"
			} else if i == at {
				st = status
			}
			a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: job.ID, WorkflowStepID: ws.ID, AssignedTo: &user.ID, Status: st})
			if err != nil {
				t.Fatal(err)
			}
			assignments = append(assignments, a.ID)
		}
		if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &assignments[at]}); err != nil {
			t.Fatal(err)
		}
		return project, job, assignments
	}
	post := func(t *testing.T, r http.Handler, project db.Project, job db.Job) result {
		t.Helper()
		enq.Pipelines = nil
		rec := testutil.DoRequest(t, r, "POST", fmt.Sprintf("/api/projects/%d/pretranslate", project.ID),
			map[string]any{"job_ids": []int64{job.ID}, "phase": "tm"}, user.ID, tenant.ID)
		var res result
		if rec.Code < 300 {
			testutil.DecodeJSON(t, rec, &res)
		} else {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		return res
	}
	exec := func(t *testing.T, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a running job is skipped", func(t *testing.T) {
		project, job, _ := newJob(t, "Running", 0, "in_progress")
		exec(t, "UPDATE jobs SET status = 'in_progress', current_step = 'Translating 1 / 4' WHERE id = $1", job.ID)
		if res := post(t, router, project, job); res.Queued != 0 || res.Skipped != 1 || len(enq.Pipelines) != 0 {
			t.Errorf("got %+v and %d queued tasks, want 0 queued, 1 skipped", res, len(enq.Pipelines))
		}
	})

	t.Run("a job past the first step is skipped", func(t *testing.T) {
		project, job, _ := newJob(t, "Review", 1, "in_progress")
		if res := post(t, router, project, job); res.Queued != 0 || res.Skipped != 1 || len(enq.Pipelines) != 0 {
			t.Errorf("got %+v and %d queued tasks, want 0 queued, 1 skipped", res, len(enq.Pipelines))
		}
	})

	t.Run("a completed first-step task is reopened and queued", func(t *testing.T) {
		project, job, assignments := newJob(t, "Done", 0, "completed")
		exec(t, "UPDATE jobs SET status = 'completed' WHERE id = $1", job.ID)
		if res := post(t, router, project, job); res.Queued != 1 || len(enq.Pipelines) != 1 || !enq.Pipelines[0].Phases.Manual {
			t.Fatalf("got %+v and %v, want the job queued as a manual TM run", res, enq.Pipelines)
		}
		var st string
		if err := pool.QueryRow(ctx, "SELECT status FROM job_step_assignments WHERE id = $1", assignments[0]).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if st != "in_progress" {
			t.Errorf("first-step task = %q, want reopened (in_progress)", st)
		}
	})

	t.Run("a job the queue refuses is put back as it was", func(t *testing.T) {
		project, job, _ := newJob(t, "Refused", 0, "in_progress")
		exec(t, "UPDATE jobs SET status = 'failed', current_step = 'Failed', error_message = 'MT engine rejected the key', translated_count = 120, total_count = 404 WHERE id = $1", job.ID)
		refuser := &failingEnqueuer{q: q}
		refusing := testutil.NewTestRouterWithEnqueuer(pool, refuser)
		if res := post(t, refusing, project, job); res.Queued != 0 || res.Skipped != 1 {
			t.Errorf("got %+v, want 0 queued, 1 skipped", res)
		}
		if len(refuser.calls) != 1 || refuser.calls[0] != job.ID {
			t.Fatalf("enqueuer calls = %v, want job %d once (the restore is what this case checks)", refuser.calls, job.ID)
		}
		got := jobRow(t, pool, job.ID)
		if got.status != "failed" || got.step != "Failed" || got.errMsg == nil || *got.errMsg != "MT engine rejected the key" {
			t.Errorf("job = %+v, want it back as failed with its message", got)
		}
		if got.translated == nil || *got.translated != 120 || got.total == nil || *got.total != 404 {
			t.Errorf("job progress = %v / %v, want the failed run's 120 / 404 back", got.translated, got.total)
		}
	})

	t.Run("a reopened job the queue refuses keeps its reopened state", func(t *testing.T) {
		project, job, assignments := newJob(t, "ReopenRefused", 0, "completed")
		exec(t, "UPDATE jobs SET status = 'completed', current_step = 'Translation complete' WHERE id = $1", job.ID)
		refuser := &failingEnqueuer{q: q}
		refusing := testutil.NewTestRouterWithEnqueuer(pool, refuser)
		if res := post(t, refusing, project, job); res.Queued != 0 || res.Skipped != 1 {
			t.Errorf("got %+v, want 0 queued, 1 skipped", res)
		}
		if len(refuser.calls) != 1 || refuser.calls[0] != job.ID {
			t.Fatalf("enqueuer calls = %v, want job %d once (the restore is what this case checks)", refuser.calls, job.ID)
		}
		var task string
		if err := pool.QueryRow(ctx, "SELECT status FROM job_step_assignments WHERE id = $1", assignments[0]).Scan(&task); err != nil {
			t.Fatal(err)
		}
		got := jobRow(t, pool, job.ID)
		if task != "in_progress" || got.status != "in_progress" || got.step != pipeline.ReopenedStepLabel || got.errMsg != nil {
			t.Errorf("task %q, job %+v; want the reopened state (task and job in progress, step %q, no error)", task, got, pipeline.ReopenedStepLabel)
		}
	})
}

type jobState struct {
	status, step      string
	errMsg            *string
	translated, total *int32
}

func jobRow(t *testing.T, pool *pgxpool.Pool, id int64) jobState {
	t.Helper()
	var s jobState
	if err := pool.QueryRow(context.Background(), "SELECT status, current_step, error_message, translated_count, total_count FROM jobs WHERE id = $1", id).Scan(&s.status, &s.step, &s.errMsg, &s.translated, &s.total); err != nil {
		t.Fatal(err)
	}
	return s
}
