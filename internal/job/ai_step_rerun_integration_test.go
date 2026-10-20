package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/testutil"
	"openloc/pkg/extsdk"
)

// POST /jobs/{id}/ai-step re-runs the active QE+APE step. With the mock
// extension installed but zero pre_translated segments, the executor reports
// PhaseSkipNoSegments and the handler returns 200 + skip_reason.
//
// Anti-tautology RED proof: drop the `if res.SkipReason == PhaseSkipNoSegments`
// branch in writeAIStepResponse — skip_reason disappears and this fails.
func TestIntegrationAIStep_NoSegments_ReturnsSkipReason(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	store := file.NewMemoryStore()
	router := testutil.NewTestRouterWithExtensions(pool, store, nil)
	tenant := testutil.SeedTenant(t, q, "AIStepNoSegCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@aistepskip.test", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "AIStep NoSeg Project", "en", []string{"de"})
	f := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, f.ID, "de")
	testutil.InstallMockExtension(t, q, store, tenant.ID)
	token := testutil.ConnectExtension(t, q, tenant.ID, testutil.MockExtensionID, "")
	seedActiveAIStep(t, q, tenant.ID, project.SettingsProfileID, job.ID, &owner.ID, &token)

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/ai-step", job.ID),
		nil, owner.ID, tenant.ID, "owner",
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
	assertSkipReasonNoSegments(t, rec)
}

// A server without the extension runtime cannot run an extension step. The
// re-run rejects with a 400 that says so, rather than a misleading 200.
//
// Anti-tautology RED proof: drop the PhaseSkipNoExtensions case from
// aiStepSkipMessage — the handler answers 200 and the 400 assertion flips.
func TestIntegrationAIStep_NoExtensionRuntime_Returns400(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool) // no storage ⇒ no extension runtime
	tenant := testutil.SeedTenant(t, q, "AIStepNoExtCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@aistepnoext.test", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "No Ext Project", "en", []string{"de"})
	f := testutil.SeedFile(t, q, project.ID, "f.json", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, f.ID, "de")
	token := testutil.ConnectExtension(t, q, tenant.ID, testutil.MockExtensionID, "")
	seedActiveAIStep(t, q, tenant.ID, project.SettingsProfileID, job.ID, &owner.ID, &token)

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/ai-step", job.ID),
		nil, owner.ID, tenant.ID, "owner",
	)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when extensions are not available; got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "custom providers are not available") {
		t.Errorf("message should say custom providers are not available; got: %s", rec.Body.String())
	}
	post, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("post-condition GetJob: %v", err)
	}
	if string(post.Status) == "completed" {
		t.Errorf("ai-step guard must not complete the job; got %q", post.Status)
	}
}

// Removing an extension deletes its keys, so the engine's re-run fails with a
// 400 saying it has no key, and touches no segment.
func TestIntegrationAIStep_RemovedExtension_FailsWithReason(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	store := file.NewMemoryStore()
	router := testutil.NewTestRouterWithExtensions(pool, store, nil)
	tenant := testutil.SeedTenant(t, q, "AIStepRemovedExtCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@removedext.test", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "Removed Ext Project", "en", []string{"de"})
	f := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, f.ID, "Hello world", 0)
	job := testutil.SeedJob(t, q, f.ID, "de")
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	if _, err := pool.Exec(ctx, `UPDATE translations SET active_text = 'Hallo Welt', origin = 'mt' WHERE id = $1`, tr.ID); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}
	testutil.InstallMockExtension(t, q, store, tenant.ID)
	token := testutil.ConnectExtension(t, q, tenant.ID, testutil.MockExtensionID, "")
	seedActiveAIStep(t, q, tenant.ID, project.SettingsProfileID, job.ID, &owner.ID, &token)

	rec := testutil.DoRequestWithRole(t, router, "DELETE", "/api/ai/extensions/"+testutil.MockExtensionID, nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	rec = testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/ai-step", job.ID), nil, owner.ID, tenant.ID, "owner")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "has no API key") {
		t.Fatalf("an engine on a removed extension's key must fail the re-run with its reason; got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM translations WHERE id = $1`, tr.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "needs_review" {
		t.Errorf("segment status = %s, want it untouched (needs_review)", status)
	}
}

// The re-run reaches the extension end to end: the mock extension (no
// endpoint) approves a clean target and post-edits a "FIXME" one, and both
// verdicts land on the segments.
func TestIntegrationAIStep_ExtensionVerdictsLand(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	store := file.NewMemoryStore()
	router := testutil.NewTestRouterWithExtensions(pool, store, nil)
	tenant := testutil.SeedTenant(t, q, "AIStepVerdictCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@verdict.test", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "Verdict Project", "en", []string{"de"})
	f := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, f.ID, "de")
	ids := map[string]int64{}
	for i, mt := range []string{"Hallo Welt", "FIXME Welt", "BAD Welt"} {
		seg := testutil.SeedSegment(t, q, f.ID, fmt.Sprintf("Source %d", i), int32(i))
		tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
		if _, err := pool.Exec(ctx, `UPDATE translations SET active_text = $1, origin = 'mt' WHERE id = $2`, mt, tr.ID); err != nil {
			t.Fatalf("seed MT text: %v", err)
		}
		ids[mt] = tr.ID
	}
	testutil.InstallMockExtension(t, q, store, tenant.ID)
	token := testutil.ConnectExtension(t, q, tenant.ID, testutil.MockExtensionID, "")
	seedActiveAIStep(t, q, tenant.ID, project.SettingsProfileID, job.ID, &owner.ID, &token)

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/ai-step", job.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	read := func(id int64) (status, text string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT status, active_text FROM translations WHERE id = $1`, id).Scan(&status, &text); err != nil {
			t.Fatal(err)
		}
		return status, text
	}
	if s, txt := read(ids["Hallo Welt"]); s != "auto_approved" || txt != "Hallo Welt" {
		t.Errorf("clean target: %s %q, want auto_approved, unchanged", s, txt)
	}
	if s, txt := read(ids["FIXME Welt"]); s != "auto_approved" || txt != "fixed Welt" {
		t.Errorf("FIXME target: %s %q, want auto_approved post-edit %q", s, txt, "fixed Welt")
	}
	if s, txt := read(ids["BAD Welt"]); s != "needs_review" || txt != "BAD Welt" {
		t.Errorf("BAD target: %s %q, want needs_review, unchanged", s, txt)
	}
}

// The manual "Re-run AI step" (POST /jobs/{id}/ai-step) must claim
// the QE+APE editor lock. Without the status label neither the frontend
// editor lock nor the backend 409 save-guard engages, and a linguist's save
// racing the predict writes can restore stale text (the original MT) and drop
// the row to Draft while the sticky predict_outcome keeps claiming "APE
// Approved". HandleAIStep must write
// in_progress + "Running QE+APE" BEFORE the synchronous run so a concurrent
// draft save is rejected with 409, exactly like the worker path.
//
// The extension's vendor stub simulates the race deterministically: when its
// /predict arrives (i.e. mid-run), it fires a draft save through the router
// and records the status code.
//
// Anti-tautology RED proof: remove the pre-run setJobStatus from HandleAIStep
// — the mid-flight save returns 200 and the 409 assertion below fails.
func TestIntegrationAIStep_ManualRerun_LocksEditor(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "AIStepLockCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@aisteplock.test", "pass", "owner")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@aisteplock.test", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "AIStep Lock Project", "en", []string{"de"})
	f := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, f.ID, "Hello world", 0)
	job := testutil.SeedJob(t, q, f.ID, "de")

	// One predict-eligible row: needs_review + automation origin + MT text.
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	if _, err := pool.Exec(ctx,
		`UPDATE translations SET active_text = 'Hallo Welt', origin = 'mt' WHERE id = $1`, tr.ID,
	); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}

	// Vendor stub behind the mock extension: on /predict, fire the concurrent
	// draft save and record its status, then approve every row.
	var router http.Handler
	var mu sync.Mutex
	midFlightStatus := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := testutil.DoRequestWithRole(t, router, "PUT",
			fmt.Sprintf("/api/files/%d/translations/%d/draft", f.ID, tr.ID),
			map[string]any{"human_text": "stale linguist edit"},
			linguist.ID, tenant.ID, "linguist",
		)
		mu.Lock()
		midFlightStatus = rec.Code
		mu.Unlock()

		var rows []extsdk.PredictRow
		_ = json.NewDecoder(r.Body).Decode(&rows)
		out := extsdk.PredictResponse{Rows: make([]extsdk.PredictResult, len(rows))}
		for i, row := range rows {
			out.Rows[i] = extsdk.PredictResult{ID: row.ID, Approved: true}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	store := file.NewMemoryStore()
	router = testutil.NewTestRouterWithExtensions(pool, store, srv.Client())
	testutil.InstallMockExtension(t, q, store, tenant.ID)
	token := testutil.ConnectExtension(t, q, tenant.ID, testutil.MockExtensionID, srv.URL)
	seedActiveAIStep(t, q, tenant.ID, project.SettingsProfileID, job.ID, &linguist.ID, &token)

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/ai-step", job.ID),
		nil, owner.ID, tenant.ID, "owner",
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	mu.Lock()
	got := midFlightStatus
	mu.Unlock()
	if got != http.StatusConflict {
		t.Fatalf("a draft save during a manual AI-step re-run must be 409 (editor locked), got %d", got)
	}

	// The predict result survived: auto-approved with the MT text intact and
	// no stale linguist edit.
	var status, text string
	if err := pool.QueryRow(ctx,
		`SELECT status, active_text FROM translations WHERE id = $1`, tr.ID,
	).Scan(&status, &text); err != nil {
		t.Fatalf("read translation: %v", err)
	}
	if status != "auto_approved" || text != "Hallo Welt" {
		t.Fatalf("predict result was clobbered: status=%s text=%q", status, text)
	}
}

// seedActiveAIStep creates a QE+APE AI engine with an extension row (no
// system prompt ⇒ the extension behind the row's key runs; a prompt-carrying
// row would be an LLM row), attaches it to a new workflow step on the
// settings profile, and makes an in_progress assignment the job's current
// step. Bypasses ValidateStepAIFields — these tests seed directly
// to exercise RunAIStep, not the save path.
//
// tokenID is the extension key the row spends (testutil.ConnectExtension).
// nil is the engine whose key was DELETED, which RunAIStep refuses to run.
func seedActiveAIStep(t *testing.T, q db.Querier, tenantID, settingsProfileID, jobID int64, assignee *int64, tokenID *int64) {
	t.Helper()
	engineID := seedQEAPEEngine(t, q, tenantID, jobID, tokenID)
	seedActiveStepWithRouter(t, q, settingsProfileID, jobID, assignee,
		testutil.SeedRouterForEngine(t, q, tenantID, engineID))
}

// seedQEAPEEngine creates the extension QE+APE engine seedActiveAIStep routes
// to: one row covering the tests' en→de pair, no system prompt. Returns its id
// so a test can put it behind a router of its own shape. tokenID as in
// seedActiveAIStep.
func seedQEAPEEngine(t *testing.T, q db.Querier, tenantID, jobID int64, tokenID *int64) int64 {
	t.Helper()
	ctx := context.Background()
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{
		TenantID: tenantID, Name: fmt.Sprintf("QE engine %d", jobID), Purpose: "qe_ape",
	})
	if err != nil {
		t.Fatalf("seedQEAPEEngine CreateAIEngine: %v", err)
	}
	if _, err := q.CreateAIEngineRow(ctx, db.CreateAIEngineRowParams{
		EngineID: engine.ID, SourceLang: "en", TargetLangs: []string{"de"}, TokenID: tokenID,
		Model: testutil.MockExtensionID,
	}); err != nil {
		t.Fatalf("seedQEAPEEngine CreateAIEngineRow: %v", err)
	}
	return engine.ID
}

// seedActiveStepWithRouter makes aiRouterID's step the job's current,
// in_progress one.
func seedActiveStepWithRouter(t *testing.T, q db.Querier, settingsProfileID, jobID int64, assignee *int64, aiRouterID int64) {
	t.Helper()
	ctx := context.Background()
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: settingsProfileID, Name: "QE review", StepOrder: 0,
		RoleRequired: "linguist", AiRouterID: &aiRouterID,
	})
	if err != nil {
		t.Fatalf("seedActiveStepWithRouter CreateSettingsWorkflowStep: %v", err)
	}
	a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: jobID, WorkflowStepID: step.ID, AssignedTo: assignee, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("seedActiveStepWithRouter CreateJobStepAssignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &a.ID}); err != nil {
		t.Fatalf("seedActiveStepWithRouter SetJobCurrentStep: %v", err)
	}
}
