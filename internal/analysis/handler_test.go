package analysis_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/analysis"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// fakeAPIEnqueuer satisfies api.TaskEnqueuer. EnqueuePipeline is a no-op
// (analyses don't enqueue pipeline tasks); EnqueueAnalysis records calls
// so the test can assert the POST handler dispatched the worker with the
// right payload.
type fakeAPIEnqueuer struct{ analysisCalls []analysisCall }

type analysisCall struct{ analysisID, tenantID int64 }

func (f *fakeAPIEnqueuer) EnqueuePipeline(fileID int64, sourceLang, targetLang string, jobID, tenantID int64, phases pipeline.TranslatePhases, triggeredBy *int64) error {
	return nil
}
func (f *fakeAPIEnqueuer) EnqueueAnalysis(analysisID, tenantID int64) error {
	f.analysisCalls = append(f.analysisCalls, analysisCall{analysisID, tenantID})
	return nil
}

// TestHandleCreateAnalysis_HappyPath — gate passes, row inserted in
// pending, worker dispatched with the right (analysisID, tenantID).
func TestHandleCreateAnalysis_HappyPath(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ana-http-happy")
	user := testutil.SeedUser(t, q, tenant.ID, "happy@test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "ana-http-project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// Mark the job past the gate. The pipeline normally does this; the
	// test exercises the gate, not the pipeline.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='completed' WHERE id=$1`, job.ID); err != nil {
		t.Fatalf("set job completed: %v", err)
	}

	enq := &fakeAPIEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(pool, enq)

	body := map[string]any{
		"name":    "test analysis",
		"job_ids": []int64{job.ID},
	}
	rec := testutil.DoRequest(t, router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/analyses", project.ID), body, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var envelope struct {
		Data db.Analysis `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	resp := envelope.Data
	if resp.Status != db.AnalysisStatusPending {
		t.Errorf("status: want pending, got %s", resp.Status)
	}
	if resp.Name != "test analysis" {
		t.Errorf("name: want %q, got %q", "test analysis", resp.Name)
	}
	if len(resp.JobIds) != 1 || resp.JobIds[0] != job.ID {
		t.Errorf("job_ids: want [%d], got %v", job.ID, resp.JobIds)
	}

	if len(enq.analysisCalls) != 1 {
		t.Fatalf("enqueue calls: want 1, got %d", len(enq.analysisCalls))
	}
	if enq.analysisCalls[0].analysisID != resp.ID || enq.analysisCalls[0].tenantID != tenant.ID {
		t.Errorf("enqueue payload: want (%d,%d), got %v", resp.ID, tenant.ID, enq.analysisCalls[0])
	}
}

// TestHandleCreateAnalysis_GateBlocksPrePipelineJobs — pretranslate-completion
// gate (planning doc §"Pretranslate-completion gate"). A selected job
// stuck in 'created' status blocks the whole request when the project has
// pretranslate phases enabled.
func TestHandleCreateAnalysis_GateBlocksPrePipelineJobs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "ana-http-gate")
	user := testutil.SeedUser(t, q, tenant.ID, "gate@test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "ana-gate-project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "g.txt", db.FileFormatTxt)
	jobReady := testutil.SeedJob(t, q, file.ID, "de")
	jobNotReady := testutil.SeedJob(t, q, file.ID, "fr")

	if _, err := pool.Exec(context.Background(),
		`UPDATE jobs SET status='completed' WHERE id=$1`, jobReady.ID); err != nil {
		t.Fatalf("setup ready job: %v", err)
	}
	// Enable at least one pretranslate phase so the gate actually runs.
	if _, err := pool.Exec(context.Background(),
		`UPDATE settings_profiles SET mt_on_upload=true WHERE id=$1`, project.SettingsProfileID); err != nil {
		t.Fatalf("enable mt_on_upload: %v", err)
	}

	enq := &fakeAPIEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(pool, enq)

	body := map[string]any{"job_ids": []int64{jobReady.ID, jobNotReady.ID}}
	rec := testutil.DoRequest(t, router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/analyses", project.ID), body, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	if len(enq.analysisCalls) != 0 {
		t.Errorf("gate should block enqueue; got %d enqueue calls", len(enq.analysisCalls))
	}
}

// TestHandleCreateAnalysis_GateSkippedWhenNoPhasesEnabled — when no
// pretranslate phases are enabled on the project, the gate is trivially
// satisfied even for jobs in 'created' status.
func TestHandleCreateAnalysis_GateSkippedWhenNoPhasesEnabled(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "ana-no-phases")
	user := testutil.SeedUser(t, q, tenant.ID, "noph@test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "no-phases", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "p.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// Both upload phases off. Job stays in 'created' — gate must accept.
	if _, err := pool.Exec(context.Background(),
		`UPDATE settings_profiles SET tm_on_upload=false, mt_on_upload=false WHERE id=$1`,
		project.SettingsProfileID); err != nil {
		t.Fatalf("disable phases: %v", err)
	}

	enq := &fakeAPIEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(pool, enq)

	body := map[string]any{"job_ids": []int64{job.ID}}
	rec := testutil.DoRequest(t, router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/analyses", project.ID), body, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
}

// TestHandleCreateAnalysis_RejectsCrossProjectJob — a job_id that exists
// but belongs to a different project must be rejected.
func TestHandleCreateAnalysis_RejectsCrossProjectJob(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ana-cross-proj")
	user := testutil.SeedUser(t, q, tenant.ID, "cross@test", "pass")
	projectA := testutil.SeedProject(t, q, tenant.ID, "pA", "en", []string{"de"})
	projectB := testutil.SeedProject(t, q, tenant.ID, "pB", "en", []string{"de"})
	fileB := testutil.SeedFile(t, q, projectB.ID, "b.txt", db.FileFormatTxt)
	jobB := testutil.SeedJob(t, q, fileB.ID, "de")

	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='completed' WHERE id=$1`, jobB.ID); err != nil {
		t.Fatal(err)
	}

	enq := &fakeAPIEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(pool, enq)

	body := map[string]any{"job_ids": []int64{jobB.ID}}
	rec := testutil.DoRequest(t, router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/analyses", projectA.ID), body, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

// TestHandleCreateAnalysis_TenantIsolation — POSTing against another
// tenant's project must surface as 404 (the GetProject lookup with the
// caller's tenant_id misses), NOT 200/400.
func TestHandleCreateAnalysis_TenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenantA := testutil.SeedTenant(t, q, "ana-tA")
	tenantB := testutil.SeedTenant(t, q, "ana-tB")
	userA := testutil.SeedUser(t, q, tenantA.ID, "ua@test", "pass")
	projectB := testutil.SeedProject(t, q, tenantB.ID, "pB", "en", []string{"de"})
	fileB := testutil.SeedFile(t, q, projectB.ID, "b.txt", db.FileFormatTxt)
	jobB := testutil.SeedJob(t, q, fileB.ID, "de")

	router := testutil.NewTestRouter(pool)

	body := map[string]any{"job_ids": []int64{jobB.ID}}
	rec := testutil.DoRequest(t, router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/analyses", projectB.ID), body, userA.ID, tenantA.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

// TestHandleListAndGet — round-trip an analysis via List + Get.
func TestHandleListAndGet(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ana-list")
	user := testutil.SeedUser(t, q, tenant.ID, "list@test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "list-proj", "en", []string{"de"})

	a, err := q.InsertAnalysis(ctx, db.InsertAnalysisParams{
		TenantID:         tenant.ID,
		ProjectID:        project.ID,
		Name:             "list-test",
		SourceLanguage:   "en",
		JobIds:           []int64{},
		SettingsSnapshot: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	router := testutil.NewTestRouter(pool)

	// List
	listRec := testutil.DoRequest(t, router, http.MethodGet,
		fmt.Sprintf("/api/projects/%d/analyses", project.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, listRec, http.StatusOK)

	var listResp struct {
		Data []db.Analysis `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Data) != 1 || listResp.Data[0].ID != a.ID {
		t.Errorf("list: want [%d], got %v", a.ID, listResp.Data)
	}

	// Get
	getRec := testutil.DoRequest(t, router, http.MethodGet,
		fmt.Sprintf("/api/analyses/%d", a.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, getRec, http.StatusOK)

	var getResp struct {
		Data struct {
			Analysis   db.Analysis      `json:"analysis"`
			Breakdowns []db.JobAnalysis `json:"breakdowns"`
		} `json:"data"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if getResp.Data.Analysis.ID != a.ID {
		t.Errorf("get analysis id: want %d, got %d", a.ID, getResp.Data.Analysis.ID)
	}
	if len(getResp.Data.Breakdowns) != 0 {
		t.Errorf("breakdowns: want empty (no worker run), got %d", len(getResp.Data.Breakdowns))
	}
}

// failingAPIEnqueuer fails every analysis dispatch with err.
type failingAPIEnqueuer struct {
	fakeAPIEnqueuer
	err error
}

func (f *failingAPIEnqueuer) EnqueueAnalysis(int64, int64) error { return f.err }

// When the worker dispatch fails, the row is marked failed with the
// user-facing FailedMessage; the dispatch error stays out of error_message,
// which the Analyses tab renders.
func TestHandleCreateAnalysis_EnqueueFailureHidesInternalError(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ana-http-enqfail")
	user := testutil.SeedUser(t, q, tenant.ID, "enqfail@test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "ana-http-enqfail", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='completed' WHERE id=$1`, job.ID); err != nil {
		t.Fatalf("set job completed: %v", err)
	}

	enq := &failingAPIEnqueuer{err: errors.New("dial tcp 10.0.0.7:6379: connect: connection refused")}
	router := testutil.NewTestRouterWithEnqueuer(pool, enq)
	rec := testutil.DoRequest(t, router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/analyses", project.ID),
		map[string]any{"name": "enqueue fails", "job_ids": []int64{job.ID}}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusInternalServerError)
	if strings.Contains(rec.Body.String(), "6379") {
		t.Errorf("response body leaks the dispatch error: %s", rec.Body.String())
	}

	rows, err := q.ListAnalysesByProject(ctx, db.ListAnalysesByProjectParams{ProjectID: project.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("list analyses: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("analyses: want 1, got %d", len(rows))
	}
	if rows[0].Status != db.AnalysisStatusFailed {
		t.Fatalf("status: want failed, got %s", rows[0].Status)
	}
	if got := testutil.Show(rows[0].ErrorMessage); got != analysis.FailedMessage {
		t.Errorf("error_message: want %q, got %q", analysis.FailedMessage, got)
	}
}
