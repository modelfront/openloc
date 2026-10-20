package job_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Assigning a job reaches its current step; when that step is an AI step,
// which runs by itself, there is nobody to hand it to: a 400, and the step
// stays unassigned.
func TestIntegrationAssignJob_CurrentAIStepRefused(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "AIJobAssignCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@ai-job-assign.test", "pass")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@ai-job-assign.test", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Proj", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	aID := testutil.SeedAIJobStep(t, q, tenant.ID, project.SettingsProfileID, job.ID)

	rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/assign", job.ID),
		map[string]int64{"user_id": linguist.ID}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	got, err := q.GetCurrentStepForJob(context.Background(), aID)
	if err != nil {
		t.Fatalf("GetCurrentStepForJob: %v", err)
	}
	if got.AssignedTo != nil {
		t.Errorf("AI step assigned to user %d; want nobody", *got.AssignedTo)
	}
}
