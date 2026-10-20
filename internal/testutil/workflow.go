package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
)

// WorkflowStepSpec is one step for SeedWorkflow. Status is the job's
// assignment status for the step; empty means "pending". StepType empty lets
// the database pick from the router: human without one, hybrid with one.
// DefaultAssignee is the blueprint's assignee, who takes the step when a
// hand-off starts it. A zero Deadline means the assignment has none.
type WorkflowStepSpec struct {
	Name            string
	StepType        string
	RouterID        *int64
	Assignee        *int64
	DefaultAssignee *int64
	Status          string
	Deadline        time.Time
}

// SeedWorkflow writes the steps onto the settings profile in order and gives
// the job one assignment per step with the requested status. Completed rows
// get completed_at. The in_progress row, when there is one, becomes the job's
// current step; otherwise the job has none, as after a finished workflow.
// Returns the assignment IDs in step order.
func SeedWorkflow(t testing.TB, q db.Querier, settingsProfileID, jobID int64, steps ...WorkflowStepSpec) []int64 {
	t.Helper()
	ids := make([]int64, 0, len(steps))
	var current *int64
	for i, sp := range steps {
		ws, err := q.CreateSettingsWorkflowStep(context.Background(), db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: settingsProfileID, Name: sp.Name, StepOrder: int32(i),
			RoleRequired: "linguist", AiRouterID: sp.RouterID, StepType: sp.StepType,
			DefaultAssignee: sp.DefaultAssignee,
		})
		if err != nil {
			t.Fatalf("SeedWorkflow create step %q: %v", sp.Name, err)
		}
		id := createStepAssignment(t, q, jobID, ws.ID, sp)
		if sp.Status == "in_progress" {
			current = &id
		}
		ids = append(ids, id)
	}
	setCurrentStep(t, q, jobID, current)
	return ids
}

// AssignStep gives the job an assignment to an existing workflow step, so
// several jobs can share one blueprint step. Only Assignee, Status and
// Deadline of sp apply. An in_progress assignment becomes the job's current
// step. Returns the assignment ID.
func AssignStep(t testing.TB, q db.Querier, jobID, workflowStepID int64, sp WorkflowStepSpec) int64 {
	t.Helper()
	id := createStepAssignment(t, q, jobID, workflowStepID, sp)
	if sp.Status == "in_progress" {
		setCurrentStep(t, q, jobID, &id)
	}
	return id
}

func createStepAssignment(t testing.TB, q db.Querier, jobID, workflowStepID int64, sp WorkflowStepSpec) int64 {
	t.Helper()
	ctx := context.Background()
	status := sp.Status
	if status == "" {
		status = "pending"
	}
	a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: jobID, WorkflowStepID: workflowStepID, AssignedTo: sp.Assignee, Status: status,
		Deadline: pgtype.Timestamptz{Time: sp.Deadline, Valid: !sp.Deadline.IsZero()},
	})
	if err != nil {
		t.Fatalf("assign step %d to job %d: %v", workflowStepID, jobID, err)
	}
	if status == "completed" {
		if err := q.CompleteStepAssignment(ctx, a.ID); err != nil {
			t.Fatalf("complete step assignment %d: %v", a.ID, err)
		}
	}
	return a.ID
}

func setCurrentStep(t testing.TB, q db.Querier, jobID int64, assignmentID *int64) {
	t.Helper()
	if err := q.SetJobCurrentStep(context.Background(), db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: assignmentID}); err != nil {
		t.Fatalf("set current step of job %d: %v", jobID, err)
	}
}
