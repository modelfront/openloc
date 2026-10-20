package main

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
	"openloc/internal/seedid"
	"openloc/internal/testutil"
)

// The seeded project looks like one made in the app: it follows a library
// workflow, and its job has a task per step with the first one current.
// Reseeding keeps the project and rebuilds the job the same way.
func TestIntegrationSeedFollowsALibraryWorkflow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// What `make seed` leaves behind: the tenant, its owner and the library.
	tenant := testutil.SeedTenant(t, q, seedid.OrgName)
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, seedid.OwnerEmail, "pw", "owner")
	lib, err := demodata.EnsureWorkflowLibrary(ctx, q, tenant.ID, nil)
	if err != nil {
		t.Fatalf("EnsureWorkflowLibrary: %v", err)
	}

	var projectID int64
	for run := 1; run <= 2; run++ {
		s, err := seed(ctx, pool)
		if err != nil {
			t.Fatalf("run %d: seed: %v", run, err)
		}
		if run == 2 && s.projectID != projectID {
			t.Errorf("reseeding moved the project: %d → %d", projectID, s.projectID)
		}
		projectID = s.projectID

		project, err := q.GetProject(ctx, db.GetProjectParams{ID: s.projectID, TenantID: tenant.ID})
		if err != nil {
			t.Fatal(err)
		}
		profile, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
		if err != nil {
			t.Fatal(err)
		}
		if profile.WorkflowID == nil || *profile.WorkflowID != lib.Standard.ID {
			t.Errorf("run %d: project follows workflow %v, want %q (%d)", run, profile.WorkflowID, demodata.WorkflowStandard, lib.Standard.ID)
		}
		steps, err := q.ListSettingsWorkflowSteps(ctx, project.SettingsProfileID)
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) == 0 {
			t.Fatalf("run %d: project has no workflow steps", run)
		}

		tasks, err := q.ListJobStepAssignments(ctx, s.jobID)
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != len(steps) {
			t.Fatalf("run %d: job has %d tasks for %d steps", run, len(tasks), len(steps))
		}
		for i, task := range tasks {
			if task.WorkflowStepID != steps[i].ID {
				t.Errorf("run %d: task %d is on step %d, want %d", run, i, task.WorkflowStepID, steps[i].ID)
			}
			want := "pending"
			if i == 0 {
				want = "in_progress"
			}
			if task.Status != want {
				t.Errorf("run %d: task %d (%s) is %q, want %q", run, i, task.StepName, task.Status, want)
			}
		}
		if tasks[0].AssignedTo == nil || *tasks[0].AssignedTo != owner.ID {
			t.Errorf("run %d: first task is assigned to %v, want the owner %d", run, tasks[0].AssignedTo, owner.ID)
		}
		job, err := q.GetJob(ctx, s.jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.CurrentStepID == nil || *job.CurrentStepID != tasks[0].ID {
			t.Errorf("run %d: job's current task is %v, want the first task %d", run, job.CurrentStepID, tasks[0].ID)
		}
	}
}
