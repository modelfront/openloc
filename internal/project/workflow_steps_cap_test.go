package project_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/workflow"
)

func makeWorkflowSteps(t *testing.T, q db.Querier, tenantID int64, n int) []map[string]any {
	steps := make([]map[string]any, n)
	for i := range steps {
		steps[i] = testutil.LibraryStep(t, q, tenantID, fmt.Sprintf("step %d", i+1))
	}
	return steps
}

// Pins that the 9-step cap holds server-side, not just in the FE editor.
// MaxWorkflowSteps+1 → 400; MaxWorkflowSteps → 200.
//
// Anti-tautology: remove the `len(steps) > stepdef.MaxWorkflowSteps` check
// in settings.resolveSteps and the 10-step case flips to 200.
func TestIntegrationWorkflowSteps_MaxCapEnforced(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "StepCapCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@stepcap.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Step Cap", "en", []string{"de"})

	// Over the cap → rejected.
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		map[string]any{"steps": makeWorkflowSteps(t, q, tenant.ID, workflow.MaxWorkflowSteps+1)},
		user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Exactly at the cap → accepted.
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		map[string]any{"steps": makeWorkflowSteps(t, q, tenant.ID, workflow.MaxWorkflowSteps)},
		user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	steps, err := q.ListSettingsWorkflowSteps(context.Background(), project.SettingsProfileID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != workflow.MaxWorkflowSteps {
		t.Errorf("expected %d steps persisted, got %d", workflow.MaxWorkflowSteps, len(steps))
	}
}
