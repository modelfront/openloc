package project_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// TestIntegrationWorkflowStepAppend_QueuesBehindStepChange pins that a
// workflow edit that appends a step to a finished workflow, which starts the
// step on the job and clears its translations' step edits, takes the job
// lock before anything else, so it waits for a step change on the job.
func TestIntegrationWorkflowStepAppend_QueuesBehindStepChange(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	j := testutil.SeedRecountJob(t, pool, q, "StepAppendLockCo", 3)

	// A finished workflow whose rows were edited in the last task.
	steps, err := q.ListJobStepAssignments(ctx, j.JobID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("ListJobStepAssignments: %d rows, %v", len(steps), err)
	}
	if _, err := pool.Exec(ctx, `UPDATE job_step_assignments SET status = 'completed', completed_at = now() WHERE id = $1`, steps[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET current_step_id = NULL WHERE id = $1`, j.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE translations SET step_edited = true WHERE id = ANY($1)`, j.IDs); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"steps": []map[string]any{
		{"existing_step_id": steps[0].WorkflowStepID},
		testutil.LibraryStep(t, q, j.TenantID, "review", map[string]any{"role_required": "linguist"}),
	}}

	testutil.ExpectQueuedBehindStepChange(t, pool, j.JobID, j.IDs[len(j.IDs)-1], func() error {
		rec := testutil.DoRequestWithRole(t, router, http.MethodPut, fmt.Sprintf("/api/projects/%d/workflow-steps", j.ProjectID),
			body, j.AdminID, j.TenantID, "admin")
		if rec.Code != http.StatusOK {
			return fmt.Errorf("status %d: %s", rec.Code, rec.Body.String())
		}
		return nil
	})

	var edited int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM translations WHERE id = ANY($1) AND step_edited`, j.IDs).Scan(&edited); err != nil {
		t.Fatal(err)
	}
	if edited != 0 {
		t.Errorf("%d rows still step_edited, want the appended step started with none", edited)
	}
}
