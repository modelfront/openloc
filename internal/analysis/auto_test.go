package analysis_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/analysis"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// fakeAutoEnqueuer records EnqueueAnalysis calls so tests can assert the
// coalesced auto-analysis was (or wasn't) dispatched.
type fakeAutoEnqueuer struct{ calls []int64 }

func (f *fakeAutoEnqueuer) EnqueueAnalysis(analysisID, tenantID int64) error {
	f.calls = append(f.calls, analysisID)
	return nil
}

func countAutoAnalyses(t *testing.T, q db.Querier, projectID, tenantID int64) int {
	t.Helper()
	rows, err := q.ListAnalysesByProject(context.Background(), db.ListAnalysesByProjectParams{ProjectID: projectID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("list analyses: %v", err)
	}
	n := 0
	for _, r := range rows {
		if r.IsAuto {
			n++
		}
	}
	return n
}

// TestAutoAnalysis_NoPretranslate_OnePerUpload: an upload with analyze=true on a
// no-pretranslate project ensures one pending auto analysis and promotes it
// immediately (jobs are analysis-ready at creation).
func TestAutoAnalysis_NoPretranslate_OnePerUpload(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "auto-ana-basic")
	project := testutil.SeedProject(t, q, tenant.ID, "auto-basic", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedJob(t, q, file.ID, "fr")

	enq := &fakeAutoEnqueuer{}
	analysis.EnsurePendingAutoAnalysis(ctx, q, tenant.ID, project.ID, project.SourceLanguage)
	analysis.PromoteProjectAutoAnalysisIfReady(ctx, q, enq, tenant.ID, project.ID)

	if n := countAutoAnalyses(t, q, project.ID, tenant.ID); n != 1 {
		t.Fatalf("auto analyses: want 1, got %d", n)
	}
	if len(enq.calls) != 1 {
		t.Errorf("enqueue calls: want 1, got %d", len(enq.calls))
	}
}

// TestAutoAnalysis_Coalesces: two files in one upload (two EnsurePending calls)
// produce ONE pending auto analysis, and it enqueues once.
func TestAutoAnalysis_Coalesces(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "auto-ana-coalesce")
	project := testutil.SeedProject(t, q, tenant.ID, "auto-coalesce", "en", []string{"de"})
	f1 := testutil.SeedFile(t, q, project.ID, "a.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, f1.ID, "de")
	f2 := testutil.SeedFile(t, q, project.ID, "b.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, f2.ID, "de")

	enq := &fakeAutoEnqueuer{}
	// Two uploads in the same batch each ensure the pending row.
	analysis.EnsurePendingAutoAnalysis(ctx, q, tenant.ID, project.ID, project.SourceLanguage)
	analysis.EnsurePendingAutoAnalysis(ctx, q, tenant.ID, project.ID, project.SourceLanguage)

	if n := countAutoAnalyses(t, q, project.ID, tenant.ID); n != 1 {
		t.Fatalf("auto analyses after 2 ensures: want 1 (coalesced), got %d", n)
	}

	analysis.PromoteProjectAutoAnalysisIfReady(ctx, q, enq, tenant.ID, project.ID)
	if len(enq.calls) != 1 {
		t.Errorf("enqueue calls: want 1, got %d", len(enq.calls))
	}
}

// TestAutoAnalysis_NotRequested_NoOp: without an EnsurePending call (analyze
// unchecked), promotion finds no pending auto analysis and does nothing.
func TestAutoAnalysis_NotRequested_NoOp(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "auto-ana-off")
	project := testutil.SeedProject(t, q, tenant.ID, "auto-off", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, file.ID, "de")

	enq := &fakeAutoEnqueuer{}
	analysis.PromoteProjectAutoAnalysisIfReady(ctx, q, enq, tenant.ID, project.ID)

	if n := countAutoAnalyses(t, q, project.ID, tenant.ID); n != 0 {
		t.Errorf("auto analyses: want 0, got %d", n)
	}
	if len(enq.calls) != 0 {
		t.Errorf("enqueue calls: want 0, got %d", len(enq.calls))
	}
}

// TestAutoAnalysis_PretranslateNotReady: with tm_on_upload set, promotion waits
// until every job is past pretranslate. A file still 'created' (or mid-pipeline)
// blocks; once all jobs complete, promotion enqueues exactly once.
func TestAutoAnalysis_PretranslateNotReady(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "auto-ana-gate")
	project := testutil.SeedProject(t, q, tenant.ID, "auto-gate", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	jobDE := testutil.SeedJob(t, q, file.ID, "de")
	jobFR := testutil.SeedJob(t, q, file.ID, "fr")
	if _, err := pool.Exec(ctx, `UPDATE settings_profiles SET tm_on_upload=true WHERE id=$1`, project.SettingsProfileID); err != nil {
		t.Fatalf("enable tm_on_upload: %v", err)
	}

	enq := &fakeAutoEnqueuer{}
	analysis.EnsurePendingAutoAnalysis(ctx, q, tenant.ID, project.ID, project.SourceLanguage)

	// Only DE done — FR still 'created'. Not ready → no enqueue.
	markJobComplete(t, pool, jobDE.ID)
	analysis.PromoteProjectAutoAnalysisIfReady(ctx, q, enq, tenant.ID, project.ID)
	if len(enq.calls) != 0 {
		t.Fatalf("enqueue while FR not ready: want 0, got %d", len(enq.calls))
	}

	// FR completes → all jobs ready → enqueue once.
	markJobComplete(t, pool, jobFR.ID)
	analysis.PromoteProjectAutoAnalysisIfReady(ctx, q, enq, tenant.ID, project.ID)
	if len(enq.calls) != 1 {
		t.Fatalf("enqueue after all jobs ready: want 1, got %d", len(enq.calls))
	}
	if n := countAutoAnalyses(t, q, project.ID, tenant.ID); n != 1 {
		t.Errorf("auto analyses: want 1, got %d", n)
	}
}

// TestAutoAnalysis_RunPersistsJobIDs: an is_auto row is created with empty
// job_ids; after the worker runs it, the row's job_ids reflect the project's
// jobs (so the UI "jobs" count isn't stuck at 0 — regression guard).
func TestAutoAnalysis_RunPersistsJobIDs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "auto-ana-jobids")
	project := testutil.SeedProject(t, q, tenant.ID, "auto-jobids", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	jobDE := testutil.SeedJob(t, q, file.ID, "de")
	jobFR := testutil.SeedJob(t, q, file.ID, "fr")

	analysis.EnsurePendingAutoAnalysis(ctx, q, tenant.ID, project.ID, project.SourceLanguage)
	pending, err := q.GetPendingAutoAnalysis(ctx, project.ID)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if len(pending.JobIds) != 0 {
		t.Fatalf("pending job_ids: want empty at creation, got %v", pending.JobIds)
	}

	if err := newService(pool, q).Run(ctx, pending.ID, tenant.ID); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, err := q.GetAnalysis(ctx, db.GetAnalysisParams{ID: pending.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("get analysis: %v", err)
	}
	if got.Status != db.AnalysisStatusCompleted {
		t.Fatalf("status: want completed, got %s", got.Status)
	}
	if len(got.JobIds) != 2 {
		t.Fatalf("resolved job_ids: want 2 (the project's jobs), got %v", got.JobIds)
	}
	assertContains(t, got.JobIds, jobDE.ID)
	assertContains(t, got.JobIds, jobFR.ID)
}

func assertContains(t *testing.T, ids []int64, want int64) {
	t.Helper()
	for _, id := range ids {
		if id == want {
			return
		}
	}
	t.Errorf("job_ids %v missing %d", ids, want)
}

// TestAutoAnalysis_PromotesWithFailedJob: a permanently-failed job (MT gave up)
// must NOT block the coalesced analysis — the readiness gate treats 'failed' as
// past-pretranslate, so once every job is terminal (some complete, some failed)
// promotion enqueues. This is what lets the worker's failure-path promotion
// rescue the analysis instead of stranding it in 'pending'.
func TestAutoAnalysis_PromotesWithFailedJob(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "auto-ana-failed")
	project := testutil.SeedProject(t, q, tenant.ID, "auto-failed", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	jobDE := testutil.SeedJob(t, q, file.ID, "de")
	jobFR := testutil.SeedJob(t, q, file.ID, "fr")
	if _, err := pool.Exec(ctx, `UPDATE settings_profiles SET mt_on_upload=true WHERE id=$1`, project.SettingsProfileID); err != nil {
		t.Fatalf("enable mt_on_upload: %v", err)
	}

	analysis.EnsurePendingAutoAnalysis(ctx, q, tenant.ID, project.ID, project.SourceLanguage)

	// DE finishes MT; FR's MT permanently failed.
	markJobComplete(t, pool, jobDE.ID)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed', error_message='mt boom' WHERE id=$1`, jobFR.ID); err != nil {
		t.Fatalf("fail FR job: %v", err)
	}

	enq := &fakeAutoEnqueuer{}
	analysis.PromoteProjectAutoAnalysisIfReady(ctx, q, enq, tenant.ID, project.ID)

	if len(enq.calls) != 1 {
		t.Fatalf("a failed job must not block promotion once all jobs are terminal: want 1 enqueue, got %d", len(enq.calls))
	}
}

// markJobComplete flips a job to the post-pretranslate state the gate accepts
// (status='in_progress', current_step ending in "complete" — see
// pipeline.IsPipelineRunning).
func markJobComplete(t *testing.T, pool *pgxpool.Pool, jobID int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE jobs SET status='in_progress', current_step='Pipeline complete' WHERE id=$1`, jobID); err != nil {
		t.Fatalf("mark job %d complete: %v", jobID, err)
	}
}
