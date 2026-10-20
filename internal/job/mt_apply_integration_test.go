package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Apply MT must fail loudly when the project's settings profile has no MT
// engine assigned for the job's language pair. Pre-fix, the handler called
// TranslateFile, which silently skipped the MT phase (correct pipeline
// behavior — the worker-side upload hook relies on it), and the handler
// then reported success. The user clicked "Apply MT", saw "MT applied", and
// nothing happened.
//
// Anti-tautology RED proof: delete the pre-flight check in HandleMTApply
// (internal/job/handler_pipeline.go) — the handler falls through to
// runPhases, TranslateFile returns nil because the MT phase is a no-op,
// and the response becomes 200 with "mt-apply complete". The 400-status
// assertion below flips to fail.
func TestIntegrationMTApply_NoEngine_Returns400(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "MTApplyNoEngineCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@mt.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "No MT Project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.json", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// No MT router on this project's profile — that's the scenario under
	// test.

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/mt-apply", job.ID),
		nil, user.ID, tenant.ID,
	)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when no MT engine is configured; got %d (body: %s)",
			rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(strings.ToLower(body), "mt") || !strings.Contains(strings.ToLower(body), "config") {
		t.Errorf("error message should mention MT + configuration so the user knows what to do; got: %s", body)
	}

	// The job MUST NOT transition to in_progress / completed for a request
	// that didn't actually run the pipeline — otherwise the FE shows a
	// misleading "completed" badge.
	post, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("post-condition GetJob: %v", err)
	}
	if string(post.Status) == "completed" || string(post.Status) == "in_progress" {
		t.Errorf("job status must not move past pre-flight rejection; got %q", post.Status)
	}
}

// A prior failed Apply MT leaves jobs.error_message set (status='failed').
// A subsequent SUCCESSFUL Apply MT must clear it — otherwise the FE keeps
// showing a stale error on a job that just succeeded (reported 2026-08-04).
//
// Anti-tautology RED proof: revert the `error_message = NULL` clause in the
// UpdateJobStatus query — the successful apply leaves the stale message in
// place and the nil assertion below fails.
func TestIntegrationMTApply_ClearsStaleError(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "MTApplyClearErrCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@mtclear.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "MT ClearErr Project", "en", []string{"de"})
	engine := testutil.SeedMTEngine(t, q, tenant.ID, "Mock", "mock")
	testutil.SeedMTRouterForProfile(t, q, tenant.ID, project.SettingsProfileID, &engine.ID, nil)
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	testutil.SeedSegment(t, q, file.ID, "Hello world", 0)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// Simulate a prior failed run: status='failed' + error_message set.
	staleMsg := "Machine translation failed: the openai account is out of quota — check your plan and billing."
	if err := q.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: job.ID, ErrorMessage: &staleMsg}); err != nil {
		t.Fatalf("seed stale job error: %v", err)
	}

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/mt-apply", job.ID),
		nil, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	post, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("post-condition GetJob: %v", err)
	}
	if post.ErrorMessage != nil {
		t.Errorf("successful Apply MT must clear the stale error_message; got %q", *post.ErrorMessage)
	}
}

// Apply TM has the same misleading-success failure mode as Apply MT when
// the project has no TM assigned for the job's language pair: a silent
// no-op at the pipeline level would surface as a green "TM lookup
// complete" toast. Same pre-flight contract: reject loudly with a useful
// message so the user knows to add a TM in settings.
//
// Anti-tautology RED proof: same shape as the MT test — delete the
// pre-flight check in HandleTMApply and the 400 assertion flips.
func TestIntegrationTMApply_NoAssignment_Returns400(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TMApplyNoAssignCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@tm.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "No TM Project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.json", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// No CreateSettingsTMAssignment for this project's profile.

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/tm-apply", job.ID),
		nil, user.ID, tenant.ID,
	)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when no TM is assigned; got %d (body: %s)",
			rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(strings.ToLower(body), "tm") || !strings.Contains(strings.ToLower(body), "config") {
		t.Errorf("error message should mention TM + configuration so the user knows what to do; got: %s", body)
	}

	post, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("post-condition GetJob: %v", err)
	}
	if string(post.Status) == "completed" || string(post.Status) == "in_progress" {
		t.Errorf("job status must not move past pre-flight rejection; got %q", post.Status)
	}
}

// Anti-tautology RED proof: drop the phaseSkipReason call from
// runPhases — skip_reason disappears and this fails.
func TestIntegrationMTApply_NoSegments_ReturnsSkipReason(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "MTApplyNoSegCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@mtskip.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "MT NoSeg Project", "en", []string{"de"})
	// Router present (avoid no_assignment 400) + zero segments
	// (force PhaseSkipNoSegments).
	engine := testutil.SeedMTEngine(t, q, tenant.ID, "Mock", "mock")
	testutil.SeedMTRouterForProfile(t, q, tenant.ID, project.SettingsProfileID, &engine.ID, nil)
	file := testutil.SeedFile(t, q, project.ID, "f.json", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, file.ID, "de")

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/mt-apply", job.ID),
		nil, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
	assertSkipReasonNoSegments(t, rec)
}

func TestIntegrationTMApply_NoSegments_ReturnsSkipReason(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TMApplyNoSegCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@tmskip.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "TM NoSeg Project", "en", []string{"de"})
	tm := testutil.SeedTM(t, q, tenant.ID, "Empty TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          1,
	}); err != nil {
		t.Fatalf("seed TM assignment: %v", err)
	}
	file := testutil.SeedFile(t, q, project.ID, "f.json", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, file.ID, "de")

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/tm-apply", job.ID),
		nil, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
	assertSkipReasonNoSegments(t, rec)
}

func assertSkipReasonNoSegments(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		Message    string `json:"message"`
		SkipReason string `json:"skip_reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body: %v (raw: %s)", err, rec.Body.String())
	}
	if body.SkipReason != "no_segments" {
		t.Fatalf("want skip_reason=%q, got %q (full body: %s)",
			"no_segments", body.SkipReason, rec.Body.String())
	}
}
