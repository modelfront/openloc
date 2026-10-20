package translation_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// seedRunningQEStep attaches a QE AI engine to a fresh in_progress
// workflow step, makes it the job's current step, and marks the job running
// with the given current_step label. Mirrors job.seedActiveAIStep.
func seedRunningQEStep(t *testing.T, q db.Querier, tenantID, settingsProfileID, jobID int64, currentStep string) {
	t.Helper()
	ctx := context.Background()
	prompt, err := q.CreateAIPrompt(ctx, db.CreateAIPromptParams{
		TenantID: tenantID, Name: fmt.Sprintf("QE prompt %d", jobID), PromptText: "verify", OutStatus: true,
	})
	if err != nil {
		t.Fatalf("seedRunningQEStep CreateAIPrompt: %v", err)
	}
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{
		TenantID: tenantID, Name: fmt.Sprintf("QE engine %d", jobID), Purpose: "qe_ape",
	})
	if err != nil {
		t.Fatalf("seedRunningQEStep CreateAIEngine: %v", err)
	}
	// Engine row: route en→de through the QE prompt so the engine is complete.
	if _, err := q.CreateAIEngineRow(ctx, db.CreateAIEngineRowParams{
		EngineID: engine.ID, SourceLang: "en", TargetLangs: []string{"de"}, SystemPromptID: &prompt.ID,
	}); err != nil {
		t.Fatalf("seedRunningQEStep CreateAIEngineRow: %v", err)
	}
	router := testutil.SeedRouterForEngine(t, q, tenantID, engine.ID)
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: settingsProfileID, Name: "QE review", StepOrder: 0,
		RoleRequired: "linguist", AiRouterID: &router,
	})
	if err != nil {
		t.Fatalf("seedRunningQEStep CreateSettingsWorkflowStep: %v", err)
	}
	a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: jobID, WorkflowStepID: step.ID, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("seedRunningQEStep CreateJobStepAssignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &a.ID}); err != nil {
		t.Fatalf("seedRunningQEStep SetJobCurrentStep: %v", err)
	}
	if err := q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: jobID, Status: "in_progress", CurrentStep: currentStep}); err != nil {
		t.Fatalf("seedRunningQEStep UpdateJobStatus: %v", err)
	}
}

// While a job's QE+APE step is running, human edits must be hard-blocked with a
// 409 — the server-side mirror of the editor lock. The AI step is
// rewriting/approving rows; a human confirm/draft would race those writes.
//
// Anti-tautology RED proof: remove the task edit guard from HandleUpdateHuman /
// HandleSaveDraft — both requests fall through to 200 and these assertions
// flip.
func TestIntegrationQEAPELock_BlocksHumanEditAndDraft(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "qeape-lock")
	projectID := createProject(t, router, "QEAPE Lock", userID, tenantID)
	proj, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("get project: %v", err)
	}

	file := testutil.SeedFile(t, q, projectID, "qeape.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "es")

	seedRunningQEStep(t, q, tenantID, proj.SettingsProfileID, job.ID, "Running QE+APE")

	// Confirm (PUT .../translations/{id}) → 409.
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": "Hola mundo", "editing_time_ms": 0},
		userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusConflict)
	// The message names the step, not one of its modes: the same lock covers a
	// QE-only and an APE-only step.
	if !strings.Contains(strings.ToLower(rec.Body.String()), "ai step") {
		t.Errorf("409 body should explain the AI-step lock; got %s", rec.Body.String())
	}

	// Draft (PUT .../draft) → 409.
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, tr.ID),
		map[string]any{"human_text": "borrador", "editing_time_ms": 0},
		userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// The row must be untouched — the lock is a hard block, not a soft warning.
	reloaded, err := q.GetTranslation(ctx, tr.ID)
	if err != nil {
		t.Fatalf("reload translation: %v", err)
	}
	if reloaded.ActiveText != nil {
		t.Errorf("locked translation must not have been written; active_text=%v", reloaded.ActiveText)
	}
}

// Control 1 (pins the is_ai_step requirement): the SAME running-pipeline state
// on a PLAIN (non-AI) step must NOT block edits — pre-translation/MT stays
// editable. Proves the 409 keys off the AI engine, not merely "a step is
// in_progress".
func TestIntegrationQEAPELock_MTRunningNotBlocked(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "qeape-mt")
	projectID := createProject(t, router, "QEAPE MT", userID, tenantID)
	proj, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("get project: %v", err)
	}

	file := testutil.SeedFile(t, q, projectID, "mt.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "es")

	// Plain first step, in_progress + running MT (no ai_router_id).
	testutil.SeedJobStep(t, q, proj.SettingsProfileID, job.ID, nil)
	if err := q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: job.ID, Status: "in_progress", CurrentStep: "Running MT"}); err != nil {
		t.Fatalf("mark running MT: %v", err)
	}

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": "Hola mundo", "editing_time_ms": 0},
		userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// Control 2 (pins the running requirement): a QE+APE step whose label reads
// "QE+APE complete" is an AI step but is NOT running — edits must flow. Proves
// the 409 keys off the running predicate, not merely "the active step is AI".
func TestIntegrationQEAPELock_CompletedQEStepNotBlocked(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "qeape-done")
	projectID := createProject(t, router, "QEAPE Done", userID, tenantID)
	proj, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("get project: %v", err)
	}

	file := testutil.SeedFile(t, q, projectID, "done.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "es")

	// AI step, but the pipeline has finished ("...complete" ⇒ not running).
	seedRunningQEStep(t, q, tenantID, proj.SettingsProfileID, job.ID, "QE+APE complete")

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": "Hola mundo", "editing_time_ms": 0},
		userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}
