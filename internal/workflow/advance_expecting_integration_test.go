package workflow_test

import (
	"context"
	"errors"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
	"openloc/internal/workflow"
)

// A Complete step names the step it saw. When the job has moved on — the
// expected step is no longer active, or another advance completed it between
// the read and the lock — nothing is completed, so a request sent twice never
// moves a job two steps.
func TestIntegrationAdvanceExpectingRefusesAStepThatMovedOn(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, tenantID := testutil.RegisterUser(t, q, "advance-expecting")
	owner := workflow.Actor{UserID: ownerID, Role: "owner"}
	project := testutil.SeedProject(t, q, tenantID, "Expecting", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "exp.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")
	steps := testutil.SeedWorkflow(t, q, project.SettingsProfileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translate", StepType: "human", Status: "in_progress"},
		testutil.WorkflowStepSpec{Name: "Review", StepType: "human"},
	)
	svc, _ := assignments(q, pipeline.NewPgxTxRunner(pool))
	status := func(id int64) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, `SELECT status FROM job_step_assignments WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	// Expecting a step that is not the active one.
	if _, err := svc.AdvanceExpecting(ctx, tenantID, job.ID, steps[1], owner, ""); !errors.Is(err, workflow.ErrStepChanged) {
		t.Fatalf("expecting the next step: err %v, want ErrStepChanged", err)
	}
	// Another advance moved the job's current step between the read and the
	// lock: the job row no longer points at the step this one read.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET current_step_id = $2 WHERE id = $1`, job.ID, steps[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdvanceExpecting(ctx, tenantID, job.ID, steps[0], owner, ""); !errors.Is(err, workflow.ErrStepChanged) {
		t.Fatalf("job moved on under the lock: err %v, want ErrStepChanged", err)
	}
	if got := status(steps[0]); got != "in_progress" {
		t.Errorf("translate step = %s, want still in progress", got)
	}
	if got := status(steps[1]); got != "pending" {
		t.Errorf("review step = %s, want still pending", got)
	}
}
