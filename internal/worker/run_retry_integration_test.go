package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
	"openloc/internal/worker"
)

// scriptedPipeline answers each TranslateFile call with the next result and
// records the phases it was asked to run. A result's before hook runs first.
type scriptedPipeline struct {
	results []scriptedResult
	calls   []pipeline.TranslatePhases
}

type scriptedResult struct {
	outcome pipeline.TranslateOutcome
	err     error
	before  func()
}

func (s *scriptedPipeline) TranslateFile(_ context.Context, _, _ int64, _, _ string, _ pipeline.ProgressFunc, phases pipeline.TranslatePhases) (pipeline.TranslateOutcome, error) {
	r := s.results[len(s.calls)]
	s.calls = append(s.calls, phases)
	if r.before != nil {
		r.before()
	}
	return r.outcome, r.err
}

func (s *scriptedPipeline) RunAIStep(context.Context, int64, int64, pipeline.ProgressFunc) (pipeline.AIStepResult, error) {
	return pipeline.AIStepResult{}, nil
}

type handleFunc func(context.Context, *asynq.Task) error

// deliver runs one delivery of the task: a first attempt or a retry, and the
// last attempt or one asynq tries again after if it fails.
func deliver(t *testing.T, handle handleFunc, task *asynq.Task, retry, last bool) error {
	t.Helper()
	defer worker.SetRetryForTest(retry)()
	defer worker.SetLastAttemptForTest(last)()
	return handle(context.Background(), task)
}

type retryFixture struct {
	pool    *pgxpool.Pool
	q       db.Querier
	tenant  db.Tenant
	project db.Project
}

func newRetryFixture(t *testing.T) retryFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	tenant := testutil.SeedTenant(t, q, "RetryOutcomeCorp")
	return retryFixture{pool: pool, q: q, tenant: tenant,
		project: testutil.SeedProject(t, q, tenant.ID, "Retry Outcome", "en", []string{"de"})}
}

// job seeds a file and its job, and the task that pre-translates it.
func (f retryFixture) job(t *testing.T, name, taskType string) (db.Job, *asynq.Task) {
	t.Helper()
	file := testutil.SeedFile(t, f.q, f.project.ID, name, db.FileFormatTxt)
	job := testutil.SeedJob(t, f.q, file.ID, "de")
	return job, pipelineTask(t, taskType, worker.PipelinePayload{
		FileID: file.ID, SourceLanguage: "en", TargetLanguage: "de",
		TenantID: f.tenant.ID, JobID: job.ID, Phases: asked,
	})
}

// failFirstAttempt delivers a first attempt that must fail and save what it
// filled for the retry.
func (f retryFixture) failFirstAttempt(t *testing.T, handle handleFunc, task *asynq.Task, jobID int64) {
	t.Helper()
	if err := deliver(t, handle, task, false, false); err == nil {
		t.Fatal("first attempt: no error, want its failure")
	}
	if !f.saved(t, jobID) {
		t.Fatal("the failed attempt saved no outcome")
	}
}

// failThenRetry is failFirstAttempt, then the retry, which must succeed.
func (f retryFixture) failThenRetry(t *testing.T, handle handleFunc, task *asynq.Task, jobID int64) {
	t.Helper()
	f.failFirstAttempt(t, handle, task, jobID)
	if err := deliver(t, handle, task, true, false); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

// requestStop flags the job the way the Stop button does.
func (f retryFixture) requestStop(t *testing.T, jobID int64) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `UPDATE jobs SET stop_requested_at = now() WHERE id = $1`, jobID); err != nil {
		t.Fatalf("request a stop: %v", err)
	}
}

func (f retryFixture) saved(t *testing.T, jobID int64) bool {
	t.Helper()
	_, err := f.q.GetJobRunOutcome(context.Background(), db.GetJobRunOutcomeParams{JobID: jobID, TenantID: f.tenant.ID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetJobRunOutcome: %v", err)
	}
	return err == nil
}

func (f retryFixture) completed(t *testing.T, fileName string) map[string]any {
	t.Helper()
	return findEventForFile(t, f.q, f.project.ID, f.tenant.ID, "job.pipeline_completed", fileName)
}

var (
	asked        = pipeline.TranslatePhases{UseTM: true, UseMT: true}
	mtOnly       = pipeline.TranslatePhases{UseMT: true}
	providerDown = &pipeline.MTFailureError{Err: errors.New("provider 503")}
	fileFailed   = errors.New("get file: connection reset")
)

// A pre-translation whose MT provider fails partway is retried, and the retry
// does only the work left: the TM phase is finished, so it runs MT alone. The
// History row of the finished run reports both attempts: the memory the first
// attempt filled from and every segment MT saved. Both task handlers do this.
func TestIntegrationWorkerRetriedRunReportsTheWholeRun(t *testing.T) {
	f := newRetryFixture(t)
	for _, tc := range []struct {
		name, taskType string
		handle         func(*worker.Handler) handleFunc
	}{
		{"translate task", worker.TaskTranslate, func(h *worker.Handler) handleFunc { return h.HandleTranslate }},
		{"pipeline task", worker.TaskPipeline, func(h *worker.Handler) handleFunc { return h.HandlePipeline }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job, task := f.job(t, tc.name+".txt", tc.taskType)
			fake := &scriptedPipeline{results: []scriptedResult{
				{outcome: pipeline.TranslateOutcome{TMExact: 50, TMFuzzy: 3, TMNames: []string{"Default TM"}, MTTranslated: 30, MTEngine: "DeepL Pro"}, err: providerDown},
				{outcome: pipeline.TranslateOutcome{MTTranslated: 40, MTEngine: "DeepL Pro"}},
			}}
			handle := tc.handle(worker.NewHandler(fake, f.q))

			f.failThenRetry(t, handle, task, job.ID)

			if want := []pipeline.TranslatePhases{asked, mtOnly}; !reflect.DeepEqual(fake.calls, want) {
				t.Errorf("phases per attempt = %+v, want %+v (the retry skips the finished TM phase)", fake.calls, want)
			}
			d := f.completed(t, tc.name+".txt")
			if d["tm_exact"] != float64(50) || d["tm_fuzzy"] != float64(3) || d["mt_translated"] != float64(70) || d["mt_engine"] != "DeepL Pro" {
				t.Errorf("completed details = %v, want 50 exact, 3 fuzzy and 70 MT segments with DeepL Pro", d)
			}
			if names, _ := d["tm_names"].([]any); len(names) != 1 || names[0] != "Default TM" {
				t.Errorf("tm_names = %v, want [Default TM] from the first attempt", d["tm_names"])
			}
			if f.saved(t, job.ID) {
				t.Error("the finished run left its saved outcome behind")
			}
		})
	}
}

// Every failed attempt adds to what the run has filled: two MT failures and a
// failure before MT, then the attempt that finishes. TM ran once, in the
// first attempt, and every later attempt skips it.
func TestIntegrationWorkerRetriedRunAddsUpEveryAttempt(t *testing.T) {
	f := newRetryFixture(t)
	job, task := f.job(t, "four-tries.txt", worker.TaskTranslate)
	fake := &scriptedPipeline{results: []scriptedResult{
		{outcome: pipeline.TranslateOutcome{TMExact: 50, TMNames: []string{"Default TM"}, MTTranslated: 30, MTEngine: "DeepL Pro"}, err: providerDown},
		{outcome: pipeline.TranslateOutcome{MTTranslated: 20, MTEngine: "DeepL Pro"}, err: providerDown},
		{err: fileFailed},
		{outcome: pipeline.TranslateOutcome{MTTranslated: 40, MTEngine: "DeepL Pro"}},
	}}
	h := worker.NewHandler(fake, f.q)
	for i, retry := range []bool{false, true, true} {
		if err := deliver(t, h.HandleTranslate, task, retry, false); err == nil {
			t.Fatalf("attempt %d: no error, want its failure", i+1)
		}
	}
	if err := deliver(t, h.HandleTranslate, task, true, true); err != nil {
		t.Fatalf("last attempt: %v", err)
	}

	if want := []pipeline.TranslatePhases{asked, mtOnly, mtOnly, mtOnly}; !reflect.DeepEqual(fake.calls, want) {
		t.Errorf("phases per attempt = %+v, want TM and MT once, then MT only", fake.calls)
	}
	d := f.completed(t, "four-tries.txt")
	if d["tm_exact"] != float64(50) || d["mt_translated"] != float64(90) {
		t.Errorf("completed details = %v, want 50 exact and 90 MT segments", d)
	}
	if names, _ := d["tm_names"].([]any); len(names) != 1 || names[0] != "Default TM" {
		t.Errorf("tm_names = %v, want [Default TM]", d["tm_names"])
	}
	if f.saved(t, job.ID) {
		t.Error("the finished run left its saved outcome behind")
	}
}

// How the attempts add up around the main case, and when the saved outcome
// is dropped.
func TestIntegrationWorkerRetriedRunEdges(t *testing.T) {
	f := newRetryFixture(t)

	t.Run("a TM failure leaves TM to the retry", func(t *testing.T) {
		job, task := f.job(t, "tm-failed.txt", worker.TaskTranslate)
		fake := &scriptedPipeline{results: []scriptedResult{
			{err: errors.New("list TM-targetable: connection reset")},
			{outcome: pipeline.TranslateOutcome{TMExact: 5, TMNames: []string{"Default TM"}, MTTranslated: 2, MTEngine: "DeepL Pro"}},
		}}
		f.failThenRetry(t, worker.NewHandler(fake, f.q).HandleTranslate, task, job.ID)
		if want := []pipeline.TranslatePhases{asked, asked}; !reflect.DeepEqual(fake.calls, want) {
			t.Errorf("phases per attempt = %+v, want TM and MT both times", fake.calls)
		}
		if d := f.completed(t, "tm-failed.txt"); d["tm_exact"] != float64(5) || d["mt_translated"] != float64(2) {
			t.Errorf("completed details = %v, want the retry's own 5 exact and 2 MT", d)
		}
	})

	t.Run("MT with nothing left after an earlier attempt still ran", func(t *testing.T) {
		job, task := f.job(t, "mt-done.txt", worker.TaskTranslate)
		fake := &scriptedPipeline{results: []scriptedResult{
			{outcome: pipeline.TranslateOutcome{TMSkipReason: pipeline.PhaseSkipNoAssignment, MTTranslated: 30, MTEngine: "DeepL Pro"},
				err: &pipeline.MTFailureError{Err: errors.New("progress write failed")}},
			{outcome: pipeline.TranslateOutcome{MTSkipReason: pipeline.PhaseSkipNoSegments}},
		}}
		f.failThenRetry(t, worker.NewHandler(fake, f.q).HandleTranslate, task, job.ID)
		d := f.completed(t, "mt-done.txt")
		if d["tm_skipped"] != "no_assignment" || d["mt_translated"] != float64(30) || d["mt_engine"] != "DeepL Pro" {
			t.Errorf("completed details = %v, want tm_skipped no_assignment and 30 MT with DeepL Pro", d)
		}
		if _, skipped := d["mt_skipped"]; skipped {
			t.Errorf("MT translated 30 segments in this run, but the row says it was skipped: %v", d)
		}
	})

	t.Run("an MT engine gone by the retry still reads as missing", func(t *testing.T) {
		job, task := f.job(t, "mt-gone.txt", worker.TaskPipeline)
		fake := &scriptedPipeline{results: []scriptedResult{
			{outcome: pipeline.TranslateOutcome{TMExact: 1, TMNames: []string{"Default TM"}, MTTranslated: 30, MTEngine: "DeepL Pro"}, err: providerDown},
			{outcome: pipeline.TranslateOutcome{MTSkipReason: pipeline.PhaseSkipNoProvider}},
		}}
		f.failThenRetry(t, worker.NewHandler(fake, f.q).HandlePipeline, task, job.ID)
		if d := f.completed(t, "mt-gone.txt"); d["mt_skipped"] != "no_provider" {
			t.Errorf("completed details = %v, want mt_skipped no_provider: segments are still empty", d)
		}
		got, err := f.q.GetJob(context.Background(), job.ID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if got.MtSkipReason == nil || *got.MtSkipReason != string(pipeline.PhaseSkipNoProvider) {
			t.Errorf("job mt_skip_reason = %v, want no_provider so the step stays open", got.MtSkipReason)
		}
	})

	t.Run("a first attempt drops what an older run left", func(t *testing.T) {
		job, task := f.job(t, "stale.txt", worker.TaskTranslate)
		f.seedSaved(t, job.ID, time.Now().Add(-time.Hour))
		droppedFirst := func() {
			if f.saved(t, job.ID) {
				t.Error("the first attempt ran before dropping the older run's outcome")
			}
		}
		fake := &scriptedPipeline{results: []scriptedResult{
			{outcome: pipeline.TranslateOutcome{TMExact: 1, TMNames: []string{"Default TM"}, MTTranslated: 2, MTEngine: "DeepL Pro"}, before: droppedFirst},
		}}
		if err := deliver(t, worker.NewHandler(fake, f.q).HandleTranslate, task, false, false); err != nil {
			t.Fatalf("HandleTranslate: %v", err)
		}
		if want := []pipeline.TranslatePhases{asked}; !reflect.DeepEqual(fake.calls, want) {
			t.Errorf("phases = %+v, want TM and MT", fake.calls)
		}
		if d := f.completed(t, "stale.txt"); d["tm_exact"] != float64(1) || d["mt_translated"] != float64(2) {
			t.Errorf("completed details = %v, want this run's 1 exact and 2 MT only", d)
		}
	})

	t.Run("a retry ignores what another run left", func(t *testing.T) {
		job, task := f.job(t, "other-run.txt", worker.TaskTranslate)
		f.seedSaved(t, job.ID, time.Now().Add(-time.Hour))
		fake := &scriptedPipeline{results: []scriptedResult{
			{outcome: pipeline.TranslateOutcome{TMExact: 1, TMNames: []string{"Default TM"}, MTTranslated: 2, MTEngine: "DeepL Pro"}},
		}}
		if err := deliver(t, worker.NewHandler(fake, f.q).HandleTranslate, task, true, false); err != nil {
			t.Fatalf("HandleTranslate: %v", err)
		}
		if want := []pipeline.TranslatePhases{asked}; !reflect.DeepEqual(fake.calls, want) {
			t.Errorf("phases = %+v, want TM and MT: the other run's finished TM is not this run's", fake.calls)
		}
		if d := f.completed(t, "other-run.txt"); d["tm_exact"] != float64(1) || d["mt_translated"] != float64(2) {
			t.Errorf("completed details = %v, want this run's 1 exact and 2 MT only", d)
		}
	})

	t.Run("a final failure drops the saved outcome", func(t *testing.T) {
		job, task := f.job(t, "final.txt", worker.TaskTranslate)
		fake := &scriptedPipeline{results: []scriptedResult{
			{outcome: pipeline.TranslateOutcome{TMExact: 1, MTTranslated: 2}, err: providerDown},
			{err: providerDown},
		}}
		h := worker.NewHandler(fake, f.q)
		f.failFirstAttempt(t, h.HandleTranslate, task, job.ID)
		if err := deliver(t, h.HandleTranslate, task, true, true); err == nil {
			t.Fatal("last attempt: no error")
		}
		if f.saved(t, job.ID) {
			t.Error("a run that failed for good left its saved outcome behind")
		}
	})

	t.Run("a run stopped between attempts drops the saved outcome", func(t *testing.T) {
		job, task := f.job(t, "stopped-queued.txt", worker.TaskTranslate)
		fake := &scriptedPipeline{results: []scriptedResult{
			{outcome: pipeline.TranslateOutcome{TMExact: 1, MTTranslated: 2}, err: providerDown},
		}}
		h := worker.NewHandler(fake, f.q)
		f.failFirstAttempt(t, h.HandleTranslate, task, job.ID)
		f.requestStop(t, job.ID)
		if err := deliver(t, h.HandleTranslate, task, true, false); err != nil {
			t.Fatalf("retry of a stopped job: %v, want nil", err)
		}
		if len(fake.calls) != 1 {
			t.Errorf("TranslateFile ran %d times, want once: the stopped retry must not run", len(fake.calls))
		}
		if f.saved(t, job.ID) {
			t.Error("a run stopped between attempts left its saved outcome behind")
		}
	})

	t.Run("a stopped run drops the saved outcome", func(t *testing.T) {
		job, task := f.job(t, "stopped.txt", worker.TaskTranslate)
		fake := &scriptedPipeline{results: []scriptedResult{
			{outcome: pipeline.TranslateOutcome{TMExact: 1, MTTranslated: 2}, err: providerDown},
			{err: context.Canceled, before: func() { f.requestStop(t, job.ID) }},
		}}
		h := worker.NewHandler(fake, f.q)
		f.failFirstAttempt(t, h.HandleTranslate, task, job.ID)
		if err := deliver(t, h.HandleTranslate, task, true, false); err != nil {
			t.Fatalf("stopped retry: %v, want nil (a stop is not retried)", err)
		}
		if f.saved(t, job.ID) {
			t.Error("a stopped run left its saved outcome behind")
		}
	})
}

// A saved outcome belongs to the job's tenant: another tenant's id writes,
// reads and deletes nothing, even with the right job id.
func TestIntegrationJobRunOutcomeIsTenantScoped(t *testing.T) {
	f := newRetryFixture(t)
	ctx := context.Background()
	other := testutil.SeedTenant(t, f.q, "OtherRetryCorp")
	job, _ := f.job(t, "scoped.txt", worker.TaskTranslate)
	rows := func() int {
		t.Helper()
		var n int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM job_run_outcomes WHERE job_id = $1`, job.ID).Scan(&n); err != nil {
			t.Fatalf("count saved outcomes: %v", err)
		}
		return n
	}
	outcome := []byte(`{"run":"2026-10-16T12:00:00Z","mt_translated":1}`)

	if err := f.q.SaveJobRunOutcome(ctx, db.SaveJobRunOutcomeParams{JobID: job.ID, TenantID: other.ID, Outcome: outcome}); err != nil {
		t.Fatalf("save with another tenant: %v", err)
	}
	if n := rows(); n != 0 {
		t.Fatalf("another tenant's save wrote %d rows for this job, want 0", n)
	}
	if err := f.q.SaveJobRunOutcome(ctx, db.SaveJobRunOutcomeParams{JobID: job.ID, TenantID: f.tenant.ID, Outcome: outcome}); err != nil || rows() != 1 {
		t.Fatalf("save with the job's tenant: err %v, %d rows; want one row", err, rows())
	}
	if _, err := f.q.GetJobRunOutcome(ctx, db.GetJobRunOutcomeParams{JobID: job.ID, TenantID: other.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("another tenant read the saved outcome: err %v, want no rows", err)
	}
	if err := f.q.DeleteJobRunOutcome(ctx, db.DeleteJobRunOutcomeParams{JobID: job.ID, TenantID: other.ID}); err != nil {
		t.Fatalf("delete with another tenant: %v", err)
	}
	if n := rows(); n != 1 {
		t.Errorf("another tenant's delete left %d rows, want the job's row kept", n)
	}
}

// seedSaved stores an outcome another run left: TM finished, 99 of each.
func (f retryFixture) seedSaved(t *testing.T, jobID int64, run time.Time) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"run": run, "tm_done": true, "tm_exact": 99, "tm_names": []string{"Old TM"}, "mt_translated": 99})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.q.SaveJobRunOutcome(context.Background(), db.SaveJobRunOutcomeParams{JobID: jobID, TenantID: f.tenant.ID, Outcome: raw}); err != nil {
		t.Fatalf("seed a saved outcome: %v", err)
	}
	if !f.saved(t, jobID) {
		t.Fatal("the seeded outcome is not readable")
	}
}
