package analysis_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Journey test: full PM flow for a saved analysis. Exercises HTTP POST →
// worker compute (called inline since the asynq layer is wired separately) →
// HTTP GET detail. Asserts:
//   - Status transitions pending → completed
//   - Per-job breakdown rows reflect seeded TM percentages
//   - Tenant isolation: tenant B cannot read tenant A's completed analysis
//   - Snapshot survival: deleting a job after completion preserves the row

func TestSavedAnalysis_FullJourney(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ana-journey")
	user := testutil.SeedUser(t, q, tenant.ID, "journey@test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "journey-proj", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// 3 segments: one TM 100% auto_approved, one TM 80% (low fuzzy), one untranslated (new band).
	seg1 := testutil.SeedSegment(t, q, file.ID, "one two three four five", 0)
	seg2 := testutil.SeedSegment(t, q, file.ID, "six seven eight nine ten", 1)
	seg3 := testutil.SeedSegment(t, q, file.ID, "eleven twelve thirteen fourteen fifteen", 2)
	for _, id := range []int64{seg1.ID, seg2.ID, seg3.ID} {
		if _, err := pool.Exec(ctx, `UPDATE segments SET word_count=5 WHERE id=$1`, id); err != nil {
			t.Fatalf("set word_count: %v", err)
		}
	}
	for _, x := range []struct {
		segID  int64
		status string
		pct    int16
	}{
		{seg1.ID, "auto_approved", 100},
		{seg2.ID, "needs_review", 80},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO translations (segment_id, target_language, status, origin, tm_match_percent)
			VALUES ($1, 'de', $2, 'tm', $3)`, x.segID, x.status, x.pct); err != nil {
			t.Fatalf("seed translation %d: %v", x.segID, err)
		}
	}
	// Mark job past the gate.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='completed' WHERE id=$1`, job.ID); err != nil {
		t.Fatalf("set job completed: %v", err)
	}

	// ─── Step 1: POST /api/projects/{id}/analyses ───
	enq := &fakeAPIEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(pool, enq)

	createRec := testutil.DoRequest(t, router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/analyses", project.ID),
		map[string]any{"name": "journey-analysis", "job_ids": []int64{job.ID}},
		user.ID, tenant.ID)
	testutil.AssertStatus(t, createRec, http.StatusCreated)

	var createResp struct {
		Data db.Analysis `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	analysisID := createResp.Data.ID
	if createResp.Data.Status != db.AnalysisStatusPending {
		t.Errorf("post-POST status: want pending, got %s", createResp.Data.Status)
	}
	if len(enq.analysisCalls) != 1 || enq.analysisCalls[0].analysisID != analysisID {
		t.Errorf("enqueue call: want analysisID=%d once, got %v", analysisID, enq.analysisCalls)
	}

	// ─── Step 2: drive the worker inline (the asynq layer is tested elsewhere) ───
	svc := newService(pool, q)
	if err := svc.Run(ctx, analysisID, tenant.ID); err != nil {
		t.Fatalf("worker Run: %v", err)
	}

	// ─── Step 3: GET /api/analyses/{id} ───
	getRec := testutil.DoRequest(t, router, http.MethodGet,
		fmt.Sprintf("/api/analyses/%d", analysisID), nil, user.ID, tenant.ID)
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
	if getResp.Data.Analysis.Status != db.AnalysisStatusCompleted {
		t.Errorf("final status: want completed, got %s", getResp.Data.Analysis.Status)
	}
	if len(getResp.Data.Breakdowns) != 1 {
		t.Fatalf("breakdowns: want 1, got %d", len(getResp.Data.Breakdowns))
	}
	row := getResp.Data.Breakdowns[0]
	// tm_100: seg1 (auto_approved, 5 words)
	if row.Tm100Segments != 1 || row.Tm100Words != 5 || row.Tm100SegmentsAutoApproved != 1 {
		t.Errorf("tm_100: want 1 seg / 5 words / 1 aa, got %d / %d / %d", row.Tm100Segments, row.Tm100Words, row.Tm100SegmentsAutoApproved)
	}
	// tm_65_84: seg2 (needs_review, 5 words)
	if row.Tm6584Segments != 1 || row.Tm6584Words != 5 || row.Tm6584SegmentsAutoApproved != 0 {
		t.Errorf("tm_65_84: want 1 seg / 5 words / 0 aa, got %d / %d / %d", row.Tm6584Segments, row.Tm6584Words, row.Tm6584SegmentsAutoApproved)
	}
	// new: seg3 (no translation row, 5 words)
	if row.NewSegments != 1 || row.NewWords != 5 || row.NewSegmentsAutoApproved != 0 {
		t.Errorf("new: want 1 seg / 5 words / 0 aa, got %d / %d / %d", row.NewSegments, row.NewWords, row.NewSegmentsAutoApproved)
	}

	// ─── Step 4: tenant isolation — tenant B can't read tenant A's analysis ───
	tenantB := testutil.SeedTenant(t, q, "ana-journey-other")
	userB := testutil.SeedUser(t, q, tenantB.ID, "other@test", "pass")
	getRecB := testutil.DoRequest(t, router, http.MethodGet,
		fmt.Sprintf("/api/analyses/%d", analysisID), nil, userB.ID, tenantB.ID)
	testutil.AssertStatus(t, getRecB, http.StatusNotFound)

	// ─── Step 5: snapshot survives job deletion ───
	if _, err := pool.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, job.ID); err != nil {
		t.Fatalf("delete job: %v", err)
	}
	postDeleteRec := testutil.DoRequest(t, router, http.MethodGet,
		fmt.Sprintf("/api/analyses/%d", analysisID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, postDeleteRec, http.StatusOK)

	var postDeleteResp struct {
		Data struct {
			Breakdowns []db.JobAnalysis `json:"breakdowns"`
		} `json:"data"`
	}
	if err := json.Unmarshal(postDeleteRec.Body.Bytes(), &postDeleteResp); err != nil {
		t.Fatalf("decode post-delete: %v", err)
	}
	if len(postDeleteResp.Data.Breakdowns) != 1 {
		t.Errorf("snapshot survival: breakdown count want 1, got %d", len(postDeleteResp.Data.Breakdowns))
	}
	if postDeleteResp.Data.Breakdowns[0].JobID != nil {
		t.Errorf("snapshot survival: job_id should be NULL post-delete, got %d", *postDeleteResp.Data.Breakdowns[0].JobID)
	}
	if postDeleteResp.Data.Breakdowns[0].JobNameSnapshot != "doc.txt" {
		t.Errorf("snapshot survival: job_name_snapshot should persist, got %q", postDeleteResp.Data.Breakdowns[0].JobNameSnapshot)
	}
}
