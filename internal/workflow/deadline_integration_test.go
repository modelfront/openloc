package workflow_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The deadline lives on the step assignment and is set via
// PATCH /api/jobs/{jobID}/workflow/steps/{stepID}/deadline. This pins:
//   - set a deadline (ISO 8601) → persisted on the assignment,
//   - clear it (null) → back to NULL,
//   - cross-tenant job → 404 (the GetJobTenant guard),
//   - a step assignment that belongs to a DIFFERENT job → 404 (the
//     GetCurrentStepForJob ownership check).
func TestIntegrationSetStepDeadline(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DeadlineEditCo")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@deadline-edit.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "Deadline Edit", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	assignmentID := testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &user.ID)

	base := fmt.Sprintf("/api/jobs/%d/workflow/steps/%d/deadline", job.ID, assignmentID)

	// Set a deadline.
	rec := testutil.DoRequest(t, router, "PATCH", base,
		map[string]any{"deadline": "2027-01-15T17:00:00Z"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	step, err := q.GetCurrentStepForJob(ctx, assignmentID)
	if err != nil {
		t.Fatalf("GetCurrentStepForJob: %v", err)
	}
	if !step.Deadline.Valid {
		t.Fatalf("expected deadline set on the assignment, got NULL")
	}
	if got := step.Deadline.Time.UTC().Format("2006-01-02T15:04:05Z"); got != "2027-01-15T17:00:00Z" {
		t.Errorf("stored deadline = %q, want 2027-01-15T17:00:00Z", got)
	}

	// Clear it.
	rec = testutil.DoRequest(t, router, "PATCH", base,
		map[string]any{"deadline": nil}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	step, err = q.GetCurrentStepForJob(ctx, assignmentID)
	if err != nil {
		t.Fatalf("GetCurrentStepForJob (after clear): %v", err)
	}
	if step.Deadline.Valid {
		t.Errorf("expected deadline cleared, got %v", step.Deadline.Time)
	}

	// Cross-tenant caller must not reach the job.
	other := testutil.SeedTenant(t, q, "OtherCo")
	otherUser := testutil.SeedUser(t, q, other.ID, "x@other.test", "pw")
	rec = testutil.DoRequest(t, router, "PATCH", base,
		map[string]any{"deadline": "2027-01-15T17:00:00Z"}, otherUser.ID, other.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant deadline set: got %d, want 404", rec.Code)
	}

	// A step assignment that belongs to a different job must be rejected.
	job2 := testutil.SeedJob(t, q, file.ID, "fr")
	wrong := fmt.Sprintf("/api/jobs/%d/workflow/steps/%d/deadline", job2.ID, assignmentID)
	rec = testutil.DoRequest(t, router, "PATCH", wrong,
		map[string]any{"deadline": "2027-01-15T17:00:00Z"}, user.ID, tenant.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("mismatched job/step: got %d, want 404", rec.Code)
	}
}

// An AI step runs by itself — nobody waits on it — so it takes no deadline,
// here as in the workflow editor (workflow_steps_ai_no_deadline). Setting one
// is a 400 that leaves the assignment untouched; clearing stays allowed.
func TestIntegrationSetStepDeadline_AIStepRefused(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AIDeadlineCo")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@ai-deadline.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "AI Deadline", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	aID := testutil.SeedAIJobStep(t, q, tenant.ID, project.SettingsProfileID, job.ID)

	base := fmt.Sprintf("/api/jobs/%d/workflow/steps/%d/deadline", job.ID, aID)
	rec := testutil.DoRequest(t, router, "PATCH", base,
		map[string]any{"deadline": "2027-01-15T17:00:00Z"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	got, err := q.GetCurrentStepForJob(ctx, aID)
	if err != nil {
		t.Fatalf("GetCurrentStepForJob: %v", err)
	}
	if got.Deadline.Valid {
		t.Errorf("AI step got a deadline %v; want none", got.Deadline.Time)
	}

	rec = testutil.DoRequest(t, router, "PATCH", base,
		map[string]any{"deadline": nil}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}
