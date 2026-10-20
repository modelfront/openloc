package timing_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/timing"
)

// timingsResponse mirrors the handler's wire shape (the Go type is in package
// timing; decoding into a local struct keeps the test honest about what a
// client actually receives).
type timingsResponse struct {
	TotalComputeMS   int64 `json:"total_compute_ms"`
	TotalQueueWaitMS int64 `json:"total_queue_wait_ms"`
	TotalRuns        int64 `json:"total_runs"`
	JobCount         int   `json:"job_count"`
	TotalWords       int64 `json:"total_words"`
	Stages           []struct {
		Stage   string            `json:"stage"`
		Group   string            `json:"group"`
		N       int64             `json:"n"`
		SelfMS  int64             `json:"self_ms"`
		TotalMS int64             `json:"total_ms"`
		MaxMS   int64             `json:"max_ms"`
		AvgMS   int64             `json:"avg_ms"`
		Err     int64             `json:"err"`
		Meta    map[string]string `json:"meta"`
		Share   float64           `json:"share"`
	} `json:"stages"`
	Jobs []struct {
		JobID          int64  `json:"job_id"`
		FileName       string `json:"file_name"`
		TargetLanguage string `json:"target_language"`
		ComputeMS      int64  `json:"compute_ms"`
		QueueWaitMS    int64  `json:"queue_wait_ms"`
		Runs           int32  `json:"runs"`
		Stages         []struct {
			Stage  string `json:"stage"`
			N      int64  `json:"n"`
			SelfMS int64  `json:"self_ms"`
		} `json:"stages"`
	} `json:"jobs"`
}

func getTimings(t *testing.T, router http.Handler, projectID int64, userID, tenantID int64) (*httptest.ResponseRecorder, timingsResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+strconv.FormatInt(projectID, 10)+"/timings", nil)
	req.Header.Set("Authorization", testutil.AuthHeader(userID, tenantID))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var wrapper struct {
		Data timingsResponse `json:"data"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &wrapper); err != nil {
			t.Fatalf("decode response %s: %v", rec.Body.String(), err)
		}
	}
	return rec, wrapper.Data
}

// TestIntegrationProjectTimings_AggregatesAcrossJobs is the panel's contract:
// the project rollup must sum its jobs, the stage list must be ordered slowest
// first, and per-job breakdowns must survive alongside the rollup.
func TestIntegrationProjectTimings_AggregatesAcrossJobs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := t.Context()

	userID, tenantID := testutil.RegisterUser(t, q, "timing-panel")
	project := testutil.SeedProject(t, q, tenantID, "Panel", "en", []string{"fr", "de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	frJob := testutil.SeedJob(t, q, file.ID, "fr")
	deJob := testutil.SeedJob(t, q, file.ID, "de")

	// fr: MT-dominated. de: TM-dominated but cheaper overall. Each job also
	// records a task span. Record() is leaf-only (self == total), so these
	// stand in for a real run's task SELF time — the overhead left after the
	// nested stages are subtracted — not the task's wall clock.
	fr := timing.New()
	fr.Record(timing.StageMTEngineCall, 4*time.Second, nil, timing.Meta("engine", "deepl"))
	fr.Record(timing.StageTMExactLookup, 200*time.Millisecond, nil)
	fr.Record(timing.StageTaskPipeline, 300*time.Millisecond, nil)
	fr.SetQueueWait(time.Second)
	fr.Flush(ctx, q, tenantID, frJob.ID)

	de := timing.New()
	de.Record(timing.StageMTEngineCall, time.Second, nil, timing.Meta("engine", "deepl"))
	de.Record(timing.StageTMExactLookup, 300*time.Millisecond, nil)
	de.Record(timing.StageTaskPipeline, 100*time.Millisecond, nil)
	de.SetQueueWait(500 * time.Millisecond)
	de.Flush(ctx, q, tenantID, deJob.ID)

	rec, resp := getTimings(t, router, project.ID, userID, tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET timings = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	if resp.JobCount != 2 {
		t.Errorf("job_count = %d, want 2", resp.JobCount)
	}
	if resp.TotalQueueWaitMS != 1500 {
		t.Errorf("total_queue_wait_ms = %d, want 1500", resp.TotalQueueWaitMS)
	}
	if resp.TotalRuns != 2 {
		t.Errorf("total_runs = %d, want 2", resp.TotalRuns)
	}

	if len(resp.Stages) < 2 {
		t.Fatalf("want at least 2 rolled-up stages, got %d", len(resp.Stages))
	}
	// Slowest first — the panel renders in array order.
	if resp.Stages[0].Stage != timing.StageMTEngineCall {
		t.Errorf("stages[0] = %q, want %q (must be ordered slowest first)",
			resp.Stages[0].Stage, timing.StageMTEngineCall)
	}
	// Look up by slug rather than index: ordering is asserted separately, and
	// a positional grab silently tests the wrong row when order changes.
	stageBySlug := map[string]int{}
	for i, s := range resp.Stages {
		stageBySlug[s.Stage] = i
	}
	mtIdx, ok := stageBySlug[timing.StageMTEngineCall]
	if !ok {
		t.Fatalf("mt.engine_call missing from rollup; got %v", resp.Stages)
	}
	mt := resp.Stages[mtIdx]
	if mt.SelfMS != 5000 {
		t.Errorf("rolled-up mt.engine_call self_ms = %d, want 5000 (4000 + 1000)", mt.SelfMS)
	}
	if mt.N != 2 {
		t.Errorf("rolled-up mt.engine_call n = %d, want 2", mt.N)
	}
	if mt.MaxMS != 4000 {
		t.Errorf("rolled-up mt.engine_call max_ms = %d, want 4000", mt.MaxMS)
	}
	if mt.AvgMS != 2500 {
		t.Errorf("rolled-up mt.engine_call avg_ms = %d, want 2500", mt.AvgMS)
	}
	if mt.Group != "mt" {
		t.Errorf("group = %q, want %q", mt.Group, "mt")
	}
	// Both jobs ran deepl, so the rollup can name it rather than "mixed".
	if mt.Meta["engine"] != "deepl" {
		t.Errorf("rolled-up engine = %q, want %q", mt.Meta["engine"], "deepl")
	}
	// Shares are weighted by SELF time and must sum to 1 across the whole
	// list — task spans included. That is the guarantee: a task span sits in
	// the same list as the stages it contains without inflating the total,
	// because self time already excludes them.
	var shareSum float64
	var selfSum int64
	for _, s := range resp.Stages {
		shareSum += s.Share
		selfSum += s.SelfMS
	}
	if shareSum < 0.999 || shareSum > 1.001 {
		t.Errorf("shares sum to %f, want 1.0 — a stage is being counted twice or dropped", shareSum)
	}
	// 4000 + 1000 (MT) + 200 + 300 (TM) + 300 + 100 (task overhead) = 5900.
	// No millisecond appears more than once, even though a task span is in the
	// list alongside the stages it would contain in a real run.
	if selfSum != 5900 {
		t.Errorf("self times sum to %d, want 5900", selfSum)
	}
	if mt.Share < 0.84 || mt.Share > 0.85 {
		t.Errorf("mt share = %f, want ~0.847 (5000/5900)", mt.Share)
	}

	if len(resp.Jobs) != 2 {
		t.Fatalf("want 2 job entries, got %d", len(resp.Jobs))
	}
	// Jobs are ordered by compute desc; both have ~0 compute here (synthetic
	// Record calls), so assert on content rather than order.
	byLang := map[string]int64{}
	for _, j := range resp.Jobs {
		if j.FileName != "doc.txt" {
			t.Errorf("job %d file_name = %q, want doc.txt", j.JobID, j.FileName)
		}
		for _, s := range j.Stages {
			if s.Stage == timing.StageMTEngineCall {
				byLang[j.TargetLanguage] = s.SelfMS
			}
		}
	}
	if byLang["fr"] != 4000 || byLang["de"] != 1000 {
		t.Errorf("per-job mt.engine_call self_ms = %v, want fr=4000 de=1000", byLang)
	}
}

// TestIntegrationProjectTimings_TenantIsolation — a project in another tenant
// must 404, not leak its timings. The handler loads the project tenant-scoped
// before touching job_timings for exactly this reason.
func TestIntegrationProjectTimings_TenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := t.Context()

	_, ownerTenant := testutil.RegisterUser(t, q, "timing-owner")
	project := testutil.SeedProject(t, q, ownerTenant, "Private", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "secret.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")

	rec := timing.New()
	rec.Record(timing.StageMTEngineCall, time.Second, nil, timing.Meta("engine", "deepl"))
	rec.Flush(ctx, q, ownerTenant, job.ID)

	attackerID, attackerTenant := testutil.RegisterUser(t, q, "timing-attacker")
	res, body := getTimings(t, router, project.ID, attackerID, attackerTenant)
	if res.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant GET timings = %d, want 404 (body: %s)", res.Code, res.Body.String())
	}
	if len(body.Jobs) != 0 {
		t.Errorf("cross-tenant response leaked %d job entries", len(body.Jobs))
	}
}

// TestIntegrationProjectTimings_EmptyProject — a project whose jobs never ran
// must render an empty panel, not 500 and not null arrays (the client maps over
// both without guarding).
func TestIntegrationProjectTimings_EmptyProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	userID, tenantID := testutil.RegisterUser(t, q, "timing-empty")
	project := testutil.SeedProject(t, q, tenantID, "Empty", "en", []string{"fr"})

	res, body := getTimings(t, router, project.ID, userID, tenantID)
	if res.Code != http.StatusOK {
		t.Fatalf("GET timings on empty project = %d, want 200", res.Code)
	}
	if body.JobCount != 0 || body.TotalComputeMS != 0 {
		t.Errorf("empty project reported job_count=%d compute=%d", body.JobCount, body.TotalComputeMS)
	}
	// Arrays, not null — `[]StageEntry{}` initialisation in buildProjectTimings.
	raw := res.Body.String()
	if !strings.Contains(raw, `"stages":[]`) || !strings.Contains(raw, `"jobs":[]`) {
		t.Errorf("want empty arrays not null in %s", raw)
	}
}
