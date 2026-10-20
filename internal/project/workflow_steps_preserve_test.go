package project_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Pins that a step kept across a workflow edit keeps its id and its in-flight
// assignment, so the assignee still sees the task. The edit must update in
// place: job_step_assignments.workflow_step_id is ON DELETE CASCADE, so
// deleting a step wipes every job's assignment and NULLs its current_step_id.
//
// Anti-tautology: revert HandleSetWorkflowSteps to DELETE-all + recreate and
// this flips RED — the linguist's task count drops to 0 and current_step_id
// goes NULL.
func TestIntegrationWorkflowSteps_EditPreservesJobAssignments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PreserveCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@preserve.test", "pass")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@preserve.test", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Preserve", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// The job is in-flight on its first step, assigned to the linguist.
	testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &linguist.ID)

	ctx := context.Background()
	// Pre-condition: the linguist sees the task.
	before, err := q.ListTasksFiltered(ctx, db.ListTasksFilteredParams{TenantID: tenant.ID, AssignedTo: &linguist.ID, Lim: 1000})
	if err != nil {
		t.Fatalf("ListTasksFiltered before: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("precondition: expected linguist to have 1 task, got %d", len(before))
	}

	// Admin edits the workflow: keeps the first (translate) step and appends a
	// review step.
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		map[string]any{"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenant.ID, "translate", map[string]any{"role_required": "linguist"}),
			testutil.LibraryStep(t, q, tenant.ID, "review", map[string]any{"role_required": "linguist"}),
		}},
		admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// The job must still be on an active step...
	gotJob, err := q.GetJobTenant(ctx, db.GetJobTenantParams{ID: job.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("GetJobTenant: %v", err)
	}
	if gotJob.CurrentStepID == nil {
		t.Fatal("job was orphaned: current_step_id is NULL after workflow edit")
	}

	// ...and the linguist must still see it (assignment + assignee preserved).
	after, err := q.ListTasksFiltered(ctx, db.ListTasksFilteredParams{TenantID: tenant.ID, AssignedTo: &linguist.ID, Lim: 1000})
	if err != nil {
		t.Fatalf("ListTasksFiltered after: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("assignment lost: expected linguist to still have 1 task after edit, got %d", len(after))
	}
	if after[0].ID != job.ID {
		t.Fatalf("wrong task surfaced: got job %d, want %d", after[0].ID, job.ID)
	}
}
