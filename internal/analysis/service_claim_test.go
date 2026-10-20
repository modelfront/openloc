package analysis_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/analysis"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// One analysis can be picked up by more than one worker: an upload promotes
// the coalesced auto analysis once per file and once per finished
// pretranslate job, and each promotion enqueues a task. Only one worker may
// write the breakdown; the others must exit without touching the row.

// seedTwoJobAnalysis creates a pending analysis over two jobs, each with one
// segment, and returns the analysis id plus the tenant it belongs to.
func seedTwoJobAnalysis(t *testing.T, q db.Querier, slug string) (analysisID, tenantID int64) {
	t.Helper()
	tenant := testutil.SeedTenant(t, q, slug)
	project := testutil.SeedProject(t, q, tenant.ID, slug+"-p", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	jobDE := testutil.SeedJob(t, q, file.ID, "de")
	jobFR := testutil.SeedJob(t, q, file.ID, "fr")
	testutil.SeedSegment(t, q, file.ID, "one two three", 0)

	a, err := q.InsertAnalysis(context.Background(), db.InsertAnalysisParams{
		TenantID:         tenant.ID,
		ProjectID:        project.ID,
		Name:             slug,
		SourceLanguage:   "en",
		JobIds:           []int64{jobDE.ID, jobFR.ID},
		SettingsSnapshot: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("insert analysis: %v", err)
	}
	return a.ID, tenant.ID
}

// A worker that picks up an analysis another worker already holds must exit
// cleanly — no duplicate breakdown row, no flip to failed.
func TestAnalysisWorker_SkipsAnalysisClaimedByAnotherWorker(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	analysisID, tenantID := seedTwoJobAnalysis(t, q, "ana-claimed")
	a, err := q.GetAnalysis(ctx, db.GetAnalysisParams{ID: analysisID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("get analysis: %v", err)
	}

	// The other worker's state: it has claimed the row and written the first
	// job's breakdown.
	if _, err := pool.Exec(ctx, `UPDATE analyses SET status = 'running' WHERE id = $1`, analysisID); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	firstJob := a.JobIds[0]
	if err := q.InsertJobAnalysis(ctx, db.InsertJobAnalysisParams{
		AnalysisID:      analysisID,
		JobID:           &firstJob,
		TargetLanguage:  "de",
		JobNameSnapshot: "doc.txt",
	}); err != nil {
		t.Fatalf("insert other worker's breakdown: %v", err)
	}

	if err := newService(pool, q).Run(ctx, analysisID, tenantID); err != nil {
		t.Fatalf("Run on a claimed analysis: want nil, got %v", err)
	}

	got, err := q.GetAnalysis(ctx, db.GetAnalysisParams{ID: analysisID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != db.AnalysisStatusRunning {
		t.Errorf("status: want running (still the other worker's), got %s (error_message=%q)", got.Status, testutil.Show(got.ErrorMessage))
	}
	rows, err := q.ListJobAnalysisByAnalysis(ctx, analysisID)
	if err != nil {
		t.Fatalf("list breakdown: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("breakdown rows: want 1 (the other worker's), got %d", len(rows))
	}
}

// Many workers racing on one pending analysis: every Run succeeds, the
// analysis completes, and each job has exactly one breakdown row.
func TestAnalysisWorker_ConcurrentRunsWriteOnce(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	analysisID, tenantID := seedTwoJobAnalysis(t, q, "ana-concurrent")
	svc := newService(pool, q)

	const workers = 8
	errs := make([]error, workers)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range workers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			errs[i] = svc.Run(ctx, analysisID, tenantID)
		}()
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: %v", i, err)
		}
	}
	got, err := q.GetAnalysis(ctx, db.GetAnalysisParams{ID: analysisID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != db.AnalysisStatusCompleted {
		t.Errorf("status: want completed, got %s (error_message=%q)", got.Status, testutil.Show(got.ErrorMessage))
	}
	rows, err := q.ListJobAnalysisByAnalysis(ctx, analysisID)
	if err != nil {
		t.Fatalf("list breakdown: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("breakdown rows: want 2 (one per job), got %d", len(rows))
	}
}

// A run that fails part-way leaves no breakdown rows behind, and the stored
// error_message is the user-facing text, not the internal error.
func TestAnalysisWorker_FailedRunRollsBackAndHidesInternalError(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantA := testutil.SeedTenant(t, q, "ana-rollback-a")
	tenantB := testutil.SeedTenant(t, q, "ana-rollback-b")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "ana-rb-pa", "en", []string{"de"})
	projectB := testutil.SeedProject(t, q, tenantB.ID, "ana-rb-pb", "en", []string{"de"})
	fileA := testutil.SeedFile(t, q, projectA.ID, "a.txt", db.FileFormatTxt)
	fileB := testutil.SeedFile(t, q, projectB.ID, "b.txt", db.FileFormatTxt)
	jobA := testutil.SeedJob(t, q, fileA.ID, "de")
	jobB := testutil.SeedJob(t, q, fileB.ID, "de")

	// The legit job comes first, so its breakdown row is written before the
	// cross-tenant job fails the run.
	a, err := q.InsertAnalysis(ctx, db.InsertAnalysisParams{
		TenantID:         tenantA.ID,
		ProjectID:        projectA.ID,
		Name:             "rollback",
		SourceLanguage:   "en",
		JobIds:           []int64{jobA.ID, jobB.ID},
		SettingsSnapshot: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("insert analysis: %v", err)
	}

	runErr := newService(pool, q).Run(ctx, a.ID, tenantA.ID)
	if runErr == nil {
		t.Fatal("expected an error from the cross-tenant job, got nil")
	}

	rows, err := q.ListJobAnalysisByAnalysis(ctx, a.ID)
	if err != nil {
		t.Fatalf("list breakdown: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("breakdown rows after a failed run: want 0 (rolled back), got %d", len(rows))
	}

	got, err := q.GetAnalysis(ctx, db.GetAnalysisParams{ID: a.ID, TenantID: tenantA.ID})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != db.AnalysisStatusFailed {
		t.Fatalf("status: want failed, got %s", got.Status)
	}
	msg := testutil.Show(got.ErrorMessage)
	if msg == runErr.Error() || strings.Contains(msg, "tenant") || strings.Contains(msg, "job ") {
		t.Errorf("error_message leaks the internal error: %q", msg)
	}
	if msg == "" || msg == "<nil>" {
		t.Error("error_message must be set on a failed run")
	}
}

// newService builds the service the way production wires it: the analysis
// run happens inside a transaction on pool.
func newService(pool *pgxpool.Pool, q db.Querier) *analysis.Service {
	return analysis.NewService(q, pipeline.NewPgxTxRunner(pool))
}
