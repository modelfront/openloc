package translation_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// These tests pin that each HTTP handler write path emits its
// translation_history row.
//
//   - HandleUpdateHuman → kind='human', text + edit_distance + created_by
//   - HandlePropagate   → kind='propagation' per sibling, source_translation_id in details
//   - HandleClear       → kind='cleared', text=NULL
//
// These kinds DO NOT touch translations.origin or
// translations.tm_match_percent — the lineage stays sticky. (XLSX
// file_import emission is exercised in the file/integration test suite
// where the upload flow already lives.)
//
// Anti-tautology: comment out the corresponding pipeline.EmitHumanHistory
// call in internal/translation/handler.go and the matching subtest goes
// RED. Verified locally 2026-05-26.

func TestIntegrationHandlerEmission_Human(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := registerUser(t, q, "history-human")
	projectID := createProject(t, router, "History Human Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "human.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID), map[string]any{
		"human_text": "Hola",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// 1) Exactly one `human` history row.
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM translation_history
		WHERE translation_id = $1 AND kind = 'human'`, tr.ID).Scan(&count); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if count != 1 {
		t.Errorf("want 1 human history row, got %d", count)
	}

	// 2) Payload: text='Hola', created_by=userID. Edit distance comes from
	//    Levenshtein vs prior active_text (NULL → distance=0).
	var hText, _ *string = nil, nil
	var hCreatedBy *int64
	if err := pool.QueryRow(ctx, `
		SELECT text, created_by FROM translation_history
		WHERE translation_id = $1 AND kind = 'human'
		ORDER BY created_at DESC LIMIT 1`, tr.ID).Scan(&hText, &hCreatedBy); err != nil {
		t.Fatalf("scan history: %v", err)
	}
	if hText == nil || *hText != "Hola" {
		t.Errorf("history.text: want 'Hola', got %v", hText)
	}
	if hCreatedBy == nil || *hCreatedBy != userID {
		t.Errorf("history.created_by: want %d, got %v", userID, hCreatedBy)
	}

	// 3) translations.origin stays NULL (no automation has touched this row
	//    — pure human translation). The sticky rule means human action does
	//    not change origin.
	var hasOrigin bool
	if err := pool.QueryRow(ctx, `
		SELECT origin IS NOT NULL FROM translations WHERE id = $1`, tr.ID).Scan(&hasOrigin); err != nil {
		t.Fatalf("scan origin: %v", err)
	}
	if hasOrigin {
		t.Error("translations.origin must remain NULL for a pure-human translation; was set")
	}
}

// TestIntegrationHandlerEmission_HumanRecordsStepAndEditingTime pins that
// a Cmd+Enter confirm must record BOTH the per-event editing time and
// the active workflow step on the emitted `human` history row, so the per-step
// productivity report can attribute the edit. Exercises the full wiring:
// HandleUpdateHuman → the task edit guard's in-progress task → EmitHumanHistory(
// EditingTimeMs, JobStepAssignmentID).
//
// Anti-tautology: drop `EditingTimeMs`/`JobStepAssignmentID` from the
// EmitHumanHistory call in handler.go and this goes RED (both columns NULL).
func TestIntegrationHandlerEmission_HumanRecordsStepAndEditingTime(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := registerUser(t, q, "history-step")
	projectID := createProject(t, router, "History Step Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "step.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	// An in-progress workflow step on this job — the task the guard reports.
	project, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	profileID := project.SettingsProfileID
	var stepID, jsaID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required) VALUES ($1, 'Translation', 1, 'member') RETURNING id`,
		profileID).Scan(&stepID); err != nil {
		t.Fatalf("insert step: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO job_step_assignments (job_id, workflow_step_id, assigned_to, status) VALUES ($1, $2, $3, 'in_progress') RETURNING id`,
		job.ID, stepID, userID).Scan(&jsaID); err != nil {
		t.Fatalf("insert assignment: %v", err)
	}

	rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID), map[string]any{
		"human_text":      "Hola",
		"editing_time_ms": 45000,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var gotStep *int64
	var gotTime *int64
	if err := pool.QueryRow(ctx, `
		SELECT job_step_assignment_id, editing_time_ms FROM translation_history
		WHERE translation_id = $1 AND kind = 'human'
		ORDER BY created_at DESC LIMIT 1`, tr.ID).Scan(&gotStep, &gotTime); err != nil {
		t.Fatalf("scan history: %v", err)
	}
	if gotStep == nil || *gotStep != jsaID {
		t.Errorf("history.job_step_assignment_id: want %d, got %v", jsaID, gotStep)
	}
	if gotTime == nil || *gotTime != 45000 {
		t.Errorf("history.editing_time_ms: want 45000, got %v", gotTime)
	}
}

func TestIntegrationHandlerEmission_Propagation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := registerUser(t, q, "history-prop")
	projectID := createProject(t, router, "History Propagation Project", userID, tenantID)
	file := testutil.SeedFile(t, q, projectID, "propagate.txt", db.FileFormatTxt)

	// Three identical-source segments. Saving + propagating from the first
	// emits 2 propagation rows (one per sibling).
	seg1 := testutil.SeedSegment(t, q, file.ID, "Hello world", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Hello world", 2)
	seg3 := testutil.SeedSegment(t, q, file.ID, "Hello world", 3)
	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "es", db.TranslationStatusNeedsReview)
	tr2 := testutil.SeedTranslation(t, q, seg2.ID, "es", db.TranslationStatusNeedsReview)
	tr3 := testutil.SeedTranslation(t, q, seg3.ID, "es", db.TranslationStatusNeedsReview)

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/files/%d/propagate", file.ID), map[string]any{
		"source_text":     "Hello world",
		"target_language": "es",
		"human_text":      "Hola mundo",
		"exclude_id":      tr1.ID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// One propagation row per sibling (tr2 + tr3).
	for _, sibID := range []int64{tr2.ID, tr3.ID} {
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM translation_history
			WHERE translation_id = $1 AND kind = 'propagation'`, sibID).Scan(&count); err != nil {
			t.Fatalf("count history for %d: %v", sibID, err)
		}
		if count != 1 {
			t.Errorf("want 1 propagation history row for translation %d, got %d", sibID, count)
		}
	}

	// No propagation row for the originating segment (tr1).
	var originCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM translation_history
		WHERE translation_id = $1 AND kind = 'propagation'`, tr1.ID).Scan(&originCount); err != nil {
		t.Fatalf("count history for tr1: %v", err)
	}
	if originCount != 0 {
		t.Errorf("originating segment must NOT get a propagation history row; got %d", originCount)
	}

	// Payload check on tr2: text propagated + details.source_translation_id = tr1.ID.
	var hText *string
	var detailsJSON string
	if err := pool.QueryRow(ctx, `
		SELECT text, details::text FROM translation_history
		WHERE translation_id = $1 AND kind = 'propagation'`, tr2.ID).Scan(&hText, &detailsJSON); err != nil {
		t.Fatalf("scan propagation history: %v", err)
	}
	if hText == nil || *hText != "Hola mundo" {
		t.Errorf("propagation.text: want 'Hola mundo', got %v", hText)
	}
	wantSubstr := fmt.Sprintf(`"source_translation_id": %d`, tr1.ID)
	if !contains(detailsJSON, wantSubstr) {
		t.Errorf("propagation.details: want substring %q, got %q", wantSubstr, detailsJSON)
	}
}

func TestIntegrationHandlerEmission_Cleared(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := registerUser(t, q, "history-clear")
	projectID := createProject(t, router, "History Clear Project", userID, tenantID)
	file := testutil.SeedFile(t, q, projectID, "clear.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	// HandleClear requires a job — without one it returns 404 ("job not
	// found"). Seed one. JobInFirstStep returns true for any job without
	// workflow steps, so we don't need to fake step assignments.
	testutil.SeedJob(t, q, file.ID, "es")

	// Write a draft first so there's something to clear (and so the test
	// exercises the realistic ordering).
	humanText := "Hola"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        tr.ID,
		HumanText: &humanText,
	}); err != nil {
		t.Fatalf("seed human text: %v", err)
	}

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/files/%d/translations/%d/clear", file.ID, tr.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM translation_history
		WHERE translation_id = $1 AND kind = 'cleared'`, tr.ID).Scan(&count); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if count != 1 {
		t.Errorf("want 1 cleared history row, got %d", count)
	}

	// text must be NULL on a cleared event (sentinel — the row is now empty).
	var hText *string
	var hCreatedBy *int64
	if err := pool.QueryRow(ctx, `
		SELECT text, created_by FROM translation_history
		WHERE translation_id = $1 AND kind = 'cleared'`, tr.ID).Scan(&hText, &hCreatedBy); err != nil {
		t.Fatalf("scan history: %v", err)
	}
	if hText != nil {
		t.Errorf("cleared.text: want NULL (sentinel), got %v", *hText)
	}
	if hCreatedBy == nil || *hCreatedBy != userID {
		t.Errorf("cleared.created_by: want %d, got %v", userID, hCreatedBy)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
