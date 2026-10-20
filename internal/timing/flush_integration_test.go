package timing_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/timing"
)

// readStages fetches a job's persisted stage map, decoded.
func readStages(t *testing.T, q db.Querier, jobID, tenantID int64) (db.JobTiming, map[string]timing.Stage) {
	t.Helper()
	row, err := q.GetJobTiming(context.Background(), db.GetJobTimingParams{
		JobID: jobID, TenantID: tenantID,
	})
	if err != nil {
		t.Fatalf("GetJobTiming: %v", err)
	}
	var stages map[string]timing.Stage
	if err := json.Unmarshal(row.Stages, &stages); err != nil {
		t.Fatalf("decode stages %s: %v", row.Stages, err)
	}
	return row, stages
}

// seedJob builds the minimum tenant → project → file → job chain a timing row
// needs to satisfy its two foreign keys.
func seedJob(t *testing.T, q db.Querier) (tenantID, jobID int64) {
	t.Helper()
	_, tenantID = testutil.RegisterUser(t, q, "timing")
	project := testutil.SeedProject(t, q, tenantID, "Timing", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")
	return tenantID, job.ID
}

// TestIntegrationFlush_AccumulatesAcrossRuns is the load-bearing test for
// the "accumulate, don't overwrite" rule. Two flushes for the same
// job — the shape a user produces by clicking "Apply MT" twice — must add n and
// ms, take the larger max_ms, add err, and let the newer meta win.
//
// Anti-tautology: change merge_stage_timings to `EXCLUDED.stages` (plain
// overwrite) in migration 086 and the n/ms assertions below go RED.
func TestIntegrationFlush_AccumulatesAcrossRuns(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	_ = pool
	ctx := context.Background()
	tenantID, jobID := seedJob(t, q)

	// First run: 20 MT chunks on deepl, one of which failed.
	r1 := timing.New()
	for i := 0; i < 19; i++ {
		r1.Record(timing.StageMTEngineCall, 100*time.Millisecond, nil, timing.Meta("engine", "deepl"))
	}
	r1.Record(timing.StageMTEngineCall, 3100*time.Millisecond, errors.New("upstream 502"), timing.Meta("engine", "deepl"))
	r1.Record(timing.StageTMExactLookup, 200*time.Millisecond, nil)
	r1.SetQueueWait(1500 * time.Millisecond)
	// compute_ms is the recorder's own lifetime — how long the task was
	// alive — not the sum of its stages. The two differ by whatever the task
	// does outside instrumented spans, so it needs real elapsed time to
	// measure, which synthetic Record durations don't provide.
	time.Sleep(10 * time.Millisecond)
	r1.Flush(ctx, q, tenantID, jobID)

	// Second run on a different engine: 5 more chunks, none as slow as the
	// worst of run 1, plus a stage run 1 never touched.
	r2 := timing.New()
	for i := 0; i < 5; i++ {
		r2.Record(timing.StageMTEngineCall, 200*time.Millisecond, nil, timing.Meta("engine", "google"))
	}
	r2.Record(timing.StageAIPredictLLM, 900*time.Millisecond, nil, timing.Meta("model", "claude-opus-5"))
	r2.SetQueueWait(500 * time.Millisecond)
	r2.Flush(ctx, q, tenantID, jobID)

	row, stages := readStages(t, q, jobID, tenantID)

	mt := stages[timing.StageMTEngineCall]
	if mt.N != 25 {
		t.Errorf("mt.engine_call n = %d, want 25 (20 + 5 accumulated)", mt.N)
	}
	// 19*100 + 3100 + 5*200 = 1900 + 3100 + 1000
	if mt.SelfMS != 6000 {
		t.Errorf("mt.engine_call self_ms = %d, want 6000", mt.SelfMS)
	}
	// Record() supplies leaf observations, so total tracks self exactly.
	if mt.TotalMS != 6000 {
		t.Errorf("mt.engine_call total_ms = %d, want 6000 (leaf spans have no children)", mt.TotalMS)
	}
	if mt.MaxMS != 3100 {
		t.Errorf("mt.engine_call max_ms = %d, want 3100 (run 1's outlier must survive run 2)", mt.MaxMS)
	}
	if mt.Err != 1 {
		t.Errorf("mt.engine_call err = %d, want 1", mt.Err)
	}
	if got := mt.Meta["engine"]; got != "google" {
		t.Errorf("mt.engine_call meta.engine = %q, want %q (newer run wins)", got, "google")
	}

	// Stages present in only one of the two runs must survive untouched.
	if tm := stages[timing.StageTMExactLookup]; tm.N != 1 || tm.SelfMS != 200 {
		t.Errorf("tm.exact_lookup = %+v, want n=1 self_ms=200 (run-1-only stage dropped by merge)", tm)
	}
	if ai := stages[timing.StageAIPredictLLM]; ai.N != 1 || ai.Meta["model"] != "claude-opus-5" {
		t.Errorf("ai.predict_llm = %+v, want n=1 model=claude-opus-5", ai)
	}

	// Zero-error stages stay out of the blob (jsonb_strip_nulls). The typed
	// decode above can't see this — Stage.Err is omitempty — so read the raw keys.
	var raw map[string]map[string]any
	if err := json.Unmarshal(row.Stages, &raw); err != nil {
		t.Fatalf("decode raw stages %s: %v", row.Stages, err)
	}
	if v, ok := raw[timing.StageTMExactLookup]["err"]; ok {
		t.Errorf("tm.exact_lookup carries err=%v, want no err key (zero-error stage)", v)
	}
	if v := raw[timing.StageMTEngineCall]["err"]; v != float64(1) {
		t.Errorf("mt.engine_call raw err = %v, want 1", v)
	}

	if row.Runs != 2 {
		t.Errorf("runs = %d, want 2", row.Runs)
	}
	if row.QueueWaitMs != 2000 {
		t.Errorf("queue_wait_ms = %d, want 2000 (1500 + 500)", row.QueueWaitMs)
	}
	// Run 1 slept 10ms before flushing; run 2 didn't sleep. Accumulated
	// compute must cover at least run 1's lifetime.
	if row.ComputeMs < 10 {
		t.Errorf("compute_ms = %d, want >= 10 (accumulated recorder lifetimes)", row.ComputeMs)
	}
	if !row.FirstStartedAt.Valid || !row.LastFinishedAt.Valid {
		t.Error("first_started_at / last_finished_at must both be set")
	}
	if row.FirstStartedAt.Time.After(row.LastFinishedAt.Time) {
		t.Errorf("first_started_at %v is after last_finished_at %v",
			row.FirstStartedAt.Time, row.LastFinishedAt.Time)
	}
}

// TestIntegrationFlush_FansIngestAcrossJobs covers ingest fan-out:
// an upload's ingest timings are file-scoped, so the same Recorder flushes once
// per job the upload created and every job's row carries its own copy.
func TestIntegrationFlush_FansIngestAcrossJobs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	_ = pool
	ctx := context.Background()

	_, tenantID := testutil.RegisterUser(t, q, "timing-fan")
	project := testutil.SeedProject(t, q, tenantID, "Fan", "en", []string{"fr", "de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	frJob := testutil.SeedJob(t, q, file.ID, "fr")
	deJob := testutil.SeedJob(t, q, file.ID, "de")

	rec := timing.New()
	rec.Record(timing.StageIngest, 920*time.Millisecond, nil, timing.Meta("format", "txt"))
	rec.Flush(ctx, q, tenantID, frJob.ID)
	rec.Flush(ctx, q, tenantID, deJob.ID)

	for _, jobID := range []int64{frJob.ID, deJob.ID} {
		_, stages := readStages(t, q, jobID, tenantID)
		ingest := stages[timing.StageIngest]
		if ingest.N != 1 || ingest.SelfMS != 920 {
			t.Errorf("job %d ingest = %+v, want n=1 self_ms=920", jobID, ingest)
		}
		if ingest.Meta["format"] != "txt" {
			t.Errorf("job %d ingest meta = %v, want format=txt", jobID, ingest.Meta)
		}
	}
}

// TestIntegrationFlush_TenantScoped guards the isolation boundary: job_timings
// carries its own tenant_id so the read path never has to join back through
// files → projects, which means the column is the only thing standing between
// tenants. A mismatched tenant must find nothing.
func TestIntegrationFlush_TenantScoped(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	_ = pool
	ctx := context.Background()

	tenantID, jobID := seedJob(t, q)
	_, otherTenant := testutil.RegisterUser(t, q, "timing-other")

	rec := timing.New()
	rec.Record(timing.StageTaskTranslate, time.Second, nil)
	rec.Flush(ctx, q, tenantID, jobID)

	if _, err := q.GetJobTiming(ctx, db.GetJobTimingParams{JobID: jobID, TenantID: otherTenant}); err == nil {
		t.Fatal("GetJobTiming returned another tenant's timing row")
	}
}

// TestIntegrationFlush_NoopsWhenEmpty keeps the table free of rows for jobs
// that never did measurable work — otherwise every job row gets a companion
// row of zeros and "which jobs have timings" stops meaning anything.
func TestIntegrationFlush_NoopsWhenEmpty(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	_ = pool
	ctx := context.Background()

	tenantID, jobID := seedJob(t, q)
	timing.New().Flush(ctx, q, tenantID, jobID)

	if _, err := q.GetJobTiming(ctx, db.GetJobTimingParams{JobID: jobID, TenantID: tenantID}); err == nil {
		t.Fatal("empty recorder wrote a row; want no row")
	}
}

// TestIntegrationUpsert_SpanSurvivesClientClockSkew pins the
// wall-clock span to a single clock: the recorder sends its *lifetime* and the
// database derives both span ends from its own clock, so start <= finish holds
// under host clock skew (Docker's VM clock is routinely tens of ms out after a
// host sleep, and workers' clocks are not comparable to each other).
//
// Anti-tautology: a query that takes the caller's absolute start time verbatim
// fails this deterministically.
func TestIntegrationUpsert_SpanSurvivesClientClockSkew(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenantID, jobID := seedJob(t, q)

	// A task that ran for 40ms. The caller says only for how long it ran, not
	// *when* it started — which is what makes the host's clock irrelevant.
	if err := q.UpsertJobTiming(ctx, db.UpsertJobTimingParams{
		JobID: jobID, TenantID: tenantID,
		Stages:      []byte(`{"mt.engine_call":{"n":1,"self_ms":40,"total_ms":40,"max_ms":40}}`),
		ComputeMs:   40,
		QueueWaitMs: 0,
	}); err != nil {
		t.Fatalf("UpsertJobTiming: %v", err)
	}

	row, _ := readStages(t, q, jobID, tenantID)
	if row.FirstStartedAt.Time.After(row.LastFinishedAt.Time) {
		t.Errorf("first_started_at %v is after last_finished_at %v — the span is inverted",
			row.FirstStartedAt.Time, row.LastFinishedAt.Time)
	}
	// The derived start must still reflect the reported lifetime rather than
	// collapsing to the flush instant.
	if span := row.LastFinishedAt.Time.Sub(row.FirstStartedAt.Time); span < 40*time.Millisecond {
		t.Errorf("span = %v, want >= 40ms (the reported lifetime)", span)
	}
}
