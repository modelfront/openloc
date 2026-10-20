package job_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The legacy POST /jobs/{id}/predict is replaced by the manager-gated
// POST /jobs/{id}/ai-step, which re-runs QE+APE for the job's
// ACTIVE QE+APE workflow step. When the active step carries no QE+APE AI engine
// (a plain human step), the endpoint rejects with 400 rather than silently
// no-op'ing — and, crucially, must NOT transition the job to "completed"
// (completed is reserved for /complete).
//
// Anti-tautology RED proof: drop the PhaseSkipNoAIStep → BadRequest branch in
// HandleAIStep and the endpoint would 200 instead of 400.
func TestIntegrationAIStep_NoEngineOnStep_Rejects(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AIStepGuardCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@aistep.test", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "AIStep Guard", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	// A plain (non-AI) workflow step is the active step for this job.
	testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &owner.ID)

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/ai-step", job.ID),
		nil, owner.ID, tenant.ID, "owner",
	)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// A rejected ai-step must not flip the job to "completed".
	post, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("post-condition GetJob: %v", err)
	}
	if string(post.Status) == "completed" {
		t.Fatalf("ai-step guard must not complete the job")
	}
}
