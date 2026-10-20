package project_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Pins that a new step assignee reaches in-flight jobs: assigning a project's
// workflow step to a linguist surfaces that project's existing jobs on the
// linguist's Tasks page. With no default_assignee, upload assigns the first
// step to the UPLOADER, so without propagation the job still points at the
// admin who uploaded it.
//
// Anti-tautology: drop the propagation from HandleSetWorkflowSteps and this
// flips RED — linguist task count stays 0 and the assignment still points at
// the uploader.
func TestIntegrationWorkflowSteps_SetAssigneePropagatesToInFlightJobs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PropagateCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@propagate.test", "pass")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@propagate.test", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Propagate", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// The job is in-flight on its first step, assigned to the ADMIN — this is
	// the upload-time fallback that fires when the step has no default_assignee.
	assignmentID := testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &admin.ID)

	ctx := context.Background()
	// Pre-condition: the linguist has nothing; the admin holds the task.
	before, err := q.ListTasksFiltered(ctx, db.ListTasksFilteredParams{TenantID: tenant.ID, AssignedTo: &linguist.ID, Lim: 1000})
	if err != nil {
		t.Fatalf("ListTasksFiltered(linguist) before: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("precondition: expected linguist to have 0 tasks, got %d", len(before))
	}
	adminBefore, err := q.ListTasksFiltered(ctx, db.ListTasksFilteredParams{TenantID: tenant.ID, AssignedTo: &admin.ID, Lim: 1000})
	if err != nil {
		t.Fatalf("ListTasksFiltered(admin) before: %v", err)
	}
	if len(adminBefore) != 1 {
		t.Fatalf("precondition: expected admin to hold 1 task, got %d", len(adminBefore))
	}

	// The PM assigns the step to the linguist — the only assignment surface the
	// project settings UI offers (WorkflowStepsEditor writes default_assignee).
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		map[string]any{"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenant.ID, "translate", map[string]any{"role_required": "linguist", "default_assignee": linguist.ID}),
		}},
		admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// The in-flight assignment must now point at the linguist...
	assignments, err := q.ListJobStepAssignments(ctx, job.ID)
	if err != nil {
		t.Fatalf("ListJobStepAssignments: %v", err)
	}
	var found bool
	for _, a := range assignments {
		if a.ID != assignmentID {
			continue
		}
		found = true
		if a.AssignedTo == nil {
			t.Fatalf("assignment %d is unassigned after the PM set an assignee", assignmentID)
		}
		if *a.AssignedTo != linguist.ID {
			t.Fatalf("assignment %d still points at user %d, want linguist %d", assignmentID, *a.AssignedTo, linguist.ID)
		}
	}
	if !found {
		t.Fatalf("assignment %d disappeared", assignmentID)
	}

	// ...so the linguist sees the task, and the admin no longer holds it.
	after, err := q.ListTasksFiltered(ctx, db.ListTasksFilteredParams{TenantID: tenant.ID, AssignedTo: &linguist.ID, Lim: 1000})
	if err != nil {
		t.Fatalf("ListTasksFiltered(linguist) after: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("linguist cannot see the assignment: expected 1 task, got %d", len(after))
	}
	if after[0].ID != job.ID {
		t.Fatalf("wrong task surfaced: got job %d, want %d", after[0].ID, job.ID)
	}
	adminAfter, err := q.ListTasksFiltered(ctx, db.ListTasksFilteredParams{TenantID: tenant.ID, AssignedTo: &admin.ID, Lim: 1000})
	if err != nil {
		t.Fatalf("ListTasksFiltered(admin) after: %v", err)
	}
	if len(adminAfter) != 0 {
		t.Fatalf("task still sitting on the admin's Tasks page: got %d tasks, want 0", len(adminAfter))
	}
}

// The blast radius of the propagation above is deliberately narrow: it fires
// only when an edit CHANGES the step's assignee to a real user. These pin the
// three "don't touch it" cases, because the naive implementation (re-sync every
// save) would silently reassign hand-assigned jobs on an unrelated edit.
func TestIntegrationWorkflowSteps_AssigneePropagationLeavesOtherEditsAlone(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "NarrowCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@narrow.test", "pass")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@narrow.test", "pass", "linguist")
	bob := testutil.SeedUserWithRole(t, q, tenant.ID, "bob@narrow.test", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Narrow", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	assignmentID := testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &admin.ID)

	ctx := context.Background()
	stepsURL := fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID)
	assigneeOf := func(t *testing.T) *int64 {
		t.Helper()
		assignments, err := q.ListJobStepAssignments(ctx, job.ID)
		if err != nil {
			t.Fatalf("ListJobStepAssignments: %v", err)
		}
		for _, a := range assignments {
			if a.ID == assignmentID {
				return a.AssignedTo
			}
		}
		t.Fatalf("assignment %d disappeared", assignmentID)
		return nil
	}

	// Set the assignee once so the blueprint holds a real user to diff against.
	rec := testutil.DoRequest(t, router, "PUT", stepsURL,
		map[string]any{"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenant.ID, "translate", map[string]any{"role_required": "linguist", "default_assignee": linguist.ID}),
		}}, admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if got := assigneeOf(t); got == nil || *got != linguist.ID {
		t.Fatalf("precondition: expected assignment on linguist %d, got %v", linguist.ID, got)
	}

	// A PM hand-assigns THIS job to bob (the bulk-assign surface). This is the
	// deliberate per-job override an unrelated workflow edit must not stomp.
	if err := q.AssignStepToUser(ctx, db.AssignStepToUserParams{ID: assignmentID, AssignedTo: &bob.ID}); err != nil {
		t.Fatalf("AssignStepToUser: %v", err)
	}

	t.Run("swapping a reached step for another is refused and reassigns nothing", func(t *testing.T) {
		// The job sits on step 1, so its identity is pinned;
		// the refusal must leave the per-job override untouched.
		rec := testutil.DoRequest(t, router, "PUT", stepsURL,
			map[string]any{"steps": []map[string]any{
				testutil.LibraryStep(t, q, tenant.ID, "translation", map[string]any{"role_required": "linguist", "default_assignee": linguist.ID}),
			}}, admin.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusConflict)
		if got := assigneeOf(t); got == nil || *got != bob.ID {
			t.Fatalf("per-job override lost on a refused edit: assignee is %v, want bob %d", got, bob.ID)
		}
	})

	t.Run("appending a step does not reassign", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "PUT", stepsURL,
			map[string]any{"steps": []map[string]any{
				testutil.LibraryStep(t, q, tenant.ID, "translate", map[string]any{"role_required": "linguist", "default_assignee": linguist.ID}),
				testutil.LibraryStep(t, q, tenant.ID, "review", map[string]any{"role_required": "linguist"}),
			}}, admin.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if got := assigneeOf(t); got == nil || *got != bob.ID {
			t.Fatalf("per-job override lost on an append: assignee is %v, want bob %d", got, bob.ID)
		}
	})

	t.Run("clearing to Anyone leaves in-flight work assigned", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "PUT", stepsURL,
			map[string]any{"steps": []map[string]any{
				testutil.LibraryStep(t, q, tenant.ID, "translate", map[string]any{"role_required": "linguist"}),
				testutil.LibraryStep(t, q, tenant.ID, "review", map[string]any{"role_required": "linguist"}),
			}}, admin.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		// Clearing means "no default for future jobs", not "yank this task off
		// the translator's plate mid-job" — which would make it vanish from
		// every Tasks page (ListTasksFiltered requires assigned_to).
		if got := assigneeOf(t); got == nil || *got != bob.ID {
			t.Fatalf("clearing the default unassigned in-flight work: assignee is %v, want bob %d", got, bob.ID)
		}
	})
}

// A completed step records who actually did the work. Reassigning it would
// falsify history, so propagation is limited to pending/in_progress.
func TestIntegrationWorkflowSteps_AssigneePropagationSkipsCompletedSteps(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "HistoryCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@history.test", "pass")
	didTheWork := testutil.SeedUserWithRole(t, q, tenant.ID, "did@history.test", "pass", "linguist")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@history.test", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "History", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	ctx := context.Background()
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID,
		Name:              "translate", StepOrder: 0, RoleRequired: "linguist",
	})
	if err != nil {
		t.Fatalf("CreateSettingsWorkflowStep: %v", err)
	}
	done, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: step.ID, AssignedTo: &didTheWork.ID, Status: "completed",
	})
	if err != nil {
		t.Fatalf("CreateJobStepAssignment: %v", err)
	}

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		map[string]any{"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenant.ID, "translate", map[string]any{"role_required": "linguist", "default_assignee": linguist.ID}),
		}}, admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	assignments, err := q.ListJobStepAssignments(ctx, job.ID)
	if err != nil {
		t.Fatalf("ListJobStepAssignments: %v", err)
	}
	for _, a := range assignments {
		if a.ID != done.ID {
			continue
		}
		if a.AssignedTo == nil || *a.AssignedTo != didTheWork.ID {
			t.Fatalf("completed step's assignee was rewritten: got %v, want %d (the user who did the work)",
				a.AssignedTo, didTheWork.ID)
		}
		return
	}
	t.Fatalf("completed assignment %d disappeared", done.ID)
}

// The assignee picker only offers members of the caller's org; the backend must
// enforce that too. A step's assignee lands on real job assignments, so an
// unchecked id would write a foreign user onto this tenant's jobs.
func TestIntegrationWorkflowSteps_RejectsNonMemberAssignee(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TenantA")
	other := testutil.SeedTenant(t, q, "TenantB")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@tenanta.test", "pass")
	outsider := testutil.SeedUser(t, q, other.ID, "outsider@tenantb.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Guarded", "en", []string{"de"})

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		map[string]any{"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenant.ID, "translate", map[string]any{"role_required": "linguist", "default_assignee": outsider.ID}),
		}}, admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// And the step must not have been written with the foreign assignee.
	steps, err := q.ListSettingsWorkflowSteps(context.Background(), project.SettingsProfileID)
	if err != nil {
		t.Fatalf("ListSettingsWorkflowSteps: %v", err)
	}
	for _, s := range steps {
		if s.DefaultAssignee != nil && *s.DefaultAssignee == outsider.ID {
			t.Fatal("foreign user was written onto the step as default_assignee")
		}
	}
}
