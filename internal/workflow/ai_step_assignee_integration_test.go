package workflow_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// An AI step runs by itself, so nobody can be assigned to it: assigning is a
// 400 that leaves the step unassigned.
func TestIntegrationAssignStep_AIStepRefused(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AIAssignCo")
	pm := testutil.SeedUser(t, q, tenant.ID, "pm@ai-assign.test", "pw")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@ai-assign.test", "pw", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "AI Assign", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	aID := testutil.SeedAIJobStep(t, q, tenant.ID, project.SettingsProfileID, job.ID)

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/steps/%d/assign", job.ID, aID),
		map[string]int64{"user_id": linguist.ID}, pm.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	got, err := q.GetCurrentStepForJob(ctx, aID)
	if err != nil {
		t.Fatalf("GetCurrentStepForJob: %v", err)
	}
	if got.AssignedTo != nil {
		t.Errorf("AI step assigned to user %d; want nobody", *got.AssignedTo)
	}
}
