package workflow_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
	"openloc/internal/workflow"
)

// The person a manager put on a step that has not started keeps it when the
// job reaches the step, and gets the assignment notification; the blueprint's
// default assignee fills only a step nobody is on.
func TestIntegrationAdvanceKeepsTheStepsAssignee(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, tenantID := testutil.RegisterUser(t, q, "advance-keeps")
	def := testutil.SeedUserWithRole(t, q, tenantID, "default@advance-keeps.test", "pw", "linguist")
	chosen := testutil.SeedUserWithRole(t, q, tenantID, "chosen@advance-keeps.test", "pw", "linguist")
	owner := workflow.Actor{UserID: ownerID, Role: "owner"}

	for _, tc := range []struct {
		name     string
		assignee *int64
		want     int64
	}{
		{"pre-assigned step keeps its person", &chosen.ID, chosen.ID},
		{"empty step takes the default", nil, def.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := testutil.SeedProject(t, q, tenantID, tc.name, "en", []string{"es"})
			file := testutil.SeedFile(t, q, project.ID, "keep.txt", db.FileFormatTxt)
			job := testutil.SeedJob(t, q, file.ID, "es")
			steps := testutil.SeedWorkflow(t, q, project.SettingsProfileID, job.ID,
				testutil.WorkflowStepSpec{Name: "Translate", StepType: "human", Status: "in_progress"},
				testutil.WorkflowStepSpec{Name: "Review", StepType: "human", DefaultAssignee: &def.ID, Assignee: tc.assignee},
			)

			svc, sent := assignments(q, pipeline.NewPgxTxRunner(pool))
			if _, err := svc.Advance(ctx, tenantID, job.ID, owner, ""); err != nil {
				t.Fatalf("advance: %v", err)
			}
			rows, err := q.ListJobStepAssignments(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if r.ID == steps[1] && (r.AssignedTo == nil || *r.AssignedTo != tc.want) {
					t.Errorf("review step assigned to %v, want %d", r.AssignedTo, tc.want)
				}
			}
			if got := sent(); len(got) != 1 || got[0].RecipientID != tc.want {
				t.Errorf("assignment notifications = %+v, want one to %d", got, tc.want)
			}
		})
	}
}
