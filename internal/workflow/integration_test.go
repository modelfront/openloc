package workflow_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationSetAndListWorkflowSteps tests setting and listing workflow steps on a template.
func TestIntegrationSetAndListWorkflowSteps(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	// Register user
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "wf-steps")

	// Create template
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Steps Template",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	// Set workflow steps (translate + review)
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", templateID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "Translation"),
			testutil.LibraryStep(t, q, tenantID, "Review"),
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var setResp struct {
		Data []struct {
			ID           int64  `json:"id"`
			Name         string `json:"name"`
			StepOrder    int32  `json:"step_order"`
			RoleRequired string `json:"role_required"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &setResp)
	if len(setResp.Data) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(setResp.Data))
	}
	if setResp.Data[0].Name != "Translation" {
		t.Errorf("expected step 0 name 'Translation', got %q", setResp.Data[0].Name)
	}
	if setResp.Data[0].StepOrder != 0 {
		t.Errorf("expected step 0 order 0, got %d", setResp.Data[0].StepOrder)
	}
	if setResp.Data[1].Name != "Review" {
		t.Errorf("expected step 1 name 'Review', got %q", setResp.Data[1].Name)
	}
	if setResp.Data[1].StepOrder != 1 {
		t.Errorf("expected step 1 order 1, got %d", setResp.Data[1].StepOrder)
	}

	// List workflow steps via GET
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d/workflow-steps", templateID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var listResp struct {
		Data []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			StepOrder int32  `json:"step_order"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 2 {
		t.Fatalf("expected 2 steps from GET, got %d", len(listResp.Data))
	}
	if listResp.Data[0].Name != "Translation" || listResp.Data[1].Name != "Review" {
		t.Fatalf("step names mismatch: got %q, %q", listResp.Data[0].Name, listResp.Data[1].Name)
	}
}

// TestIntegrationListJobSteps tests creating job step assignments via DB and listing them via API.
func TestIntegrationListJobSteps(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Register user
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "job-steps")

	// Create template + workflow steps via API
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Job Steps Template",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", templateID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "member"}),
			testutil.LibraryStep(t, q, tenantID, "review", map[string]any{"role_required": "member"}),
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var stepsResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &stepsResp)
	translateStepID := stepsResp.Data[0].ID
	reviewStepID := stepsResp.Data[1].ID

	// Create project + file + job directly in DB
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Job Steps Project",
		"source_language":  "en",
		"target_languages": []string{"fr"},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID

	file := testutil.SeedFile(t, q, projectID, "steps.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")

	// Create step assignments
	assign1, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: translateStepID, AssignedTo: &userID, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("create translate assignment: %v", err)
	}
	_, err = q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: reviewStepID, AssignedTo: &userID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create review assignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &assign1.ID}); err != nil {
		t.Fatalf("set current step: %v", err)
	}

	// List job steps via API
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/workflow/steps", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var jobStepsResp struct {
		Data []struct {
			ID       int64  `json:"id"`
			StepName string `json:"step_name"`
			Status   string `json:"status"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &jobStepsResp)
	if len(jobStepsResp.Data) != 2 {
		t.Fatalf("expected 2 step assignments, got %d", len(jobStepsResp.Data))
	}
	if jobStepsResp.Data[0].Status != "in_progress" {
		t.Errorf("expected translate step in_progress, got %s", jobStepsResp.Data[0].Status)
	}
	if jobStepsResp.Data[1].Status != "pending" {
		t.Errorf("expected review step pending, got %s", jobStepsResp.Data[1].Status)
	}
}

// TestIntegrationAdvanceStep tests advancing from translate to review step.
func TestIntegrationAdvanceStep(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Register translator
	translatorID, tenantID := testutil.RegisterUser(t, db.New(pool), "advance-translator")

	// Create reviewer in same tenant
	reviewer := testutil.SeedUserWithRole(t, q, tenantID, "advance-reviewer@test.com", "pw", "member")
	reviewerID := reviewer.ID

	// Create template + workflow steps
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Advance Template",
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", templateID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "member"}),
			testutil.LibraryStep(t, q, tenantID, "review", map[string]any{"role_required": "member"}),
		},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var stepsResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &stepsResp)
	translateStepID := stepsResp.Data[0].ID
	reviewStepID := stepsResp.Data[1].ID

	// Create project + file + job
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Advance Project",
		"source_language":  "en",
		"target_languages": []string{"de"},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)

	file := testutil.SeedFile(t, q, projResp.Data.ID, "advance.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// Create step assignments
	translateAssign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: translateStepID, AssignedTo: &translatorID, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("create translate assignment: %v", err)
	}
	_, err = q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: reviewStepID, AssignedTo: &reviewerID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create review assignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &translateAssign.ID}); err != nil {
		t.Fatalf("set current step: %v", err)
	}

	// Advance as translator
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", job.ID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var advanceResp struct {
		Data struct {
			Status     string `json:"status"`
			NextStepID int64  `json:"next_step_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &advanceResp)
	if advanceResp.Data.Status != "advanced" {
		t.Fatalf("expected status 'advanced', got %s", advanceResp.Data.Status)
	}

	// Verify via list: translate=completed, review=in_progress
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/workflow/steps", job.ID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var jobStepsResp struct {
		Data []struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &jobStepsResp)
	if len(jobStepsResp.Data) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(jobStepsResp.Data))
	}
	if jobStepsResp.Data[0].Status != "completed" {
		t.Errorf("expected translate step completed, got %s", jobStepsResp.Data[0].Status)
	}
	if jobStepsResp.Data[1].Status != "in_progress" {
		t.Errorf("expected review step in_progress, got %s", jobStepsResp.Data[1].Status)
	}
}

// TestIntegrationHardLockForbidden tests that a non-assigned user cannot advance a step.
func TestIntegrationHardLockForbidden(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Register translator (owner)
	translatorID, tenantID := testutil.RegisterUser(t, db.New(pool), "lock-translator")

	// Create second user (reviewer) who is NOT assigned to the active step
	reviewer := testutil.SeedUserWithRole(t, q, tenantID, "lock-reviewer@test.com", "pw", "member")
	reviewerID := reviewer.ID

	// Create template + steps
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Lock Template",
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)

	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", tmplResp.Data.ID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "member"}),
			testutil.LibraryStep(t, q, tenantID, "review", map[string]any{"role_required": "member"}),
		},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var stepsResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &stepsResp)

	// Create project + file + job
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Lock Project",
		"source_language":  "en",
		"target_languages": []string{"es"},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)

	file := testutil.SeedFile(t, q, projResp.Data.ID, "lock.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	// Assign translate step to translator, review to reviewer
	translateAssign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: stepsResp.Data[0].ID, AssignedTo: &translatorID, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("create translate assignment: %v", err)
	}
	_, err = q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: stepsResp.Data[1].ID, AssignedTo: &reviewerID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create review assignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &translateAssign.ID}); err != nil {
		t.Fatalf("set current step: %v", err)
	}

	// The reviewer (a linguist, not the assignee) tries to advance the
	// translate step → 403. A manager could — see
	// TestIntegrationAssigneeOrManagerCanAdvance.
	rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", job.ID), nil, reviewerID, tenantID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
}

// TestIntegrationRejectStep tests rejecting a step and reopening the previous one.
func TestIntegrationRejectStep(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Register translator
	translatorID, tenantID := testutil.RegisterUser(t, db.New(pool), "reject-translator")

	reviewer := testutil.SeedUserWithRole(t, q, tenantID, "reject-reviewer@test.com", "pw", "member")
	reviewerID := reviewer.ID

	// Create template + steps
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Reject Template",
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", templateID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "member"}),
			testutil.LibraryStep(t, q, tenantID, "review", map[string]any{"role_required": "member"}),
		},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var stepsResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &stepsResp)
	translateStepID := stepsResp.Data[0].ID
	reviewStepID := stepsResp.Data[1].ID

	// Create project + file + job
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Reject Project",
		"source_language":  "en",
		"target_languages": []string{"fr"},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)

	file := testutil.SeedFile(t, q, projResp.Data.ID, "reject.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")

	// Create assignments: translate assigned to translator, review to reviewer
	translateAssign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: translateStepID, AssignedTo: &translatorID, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("create translate assignment: %v", err)
	}
	_, err = q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: reviewStepID, AssignedTo: &reviewerID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create review assignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &translateAssign.ID}); err != nil {
		t.Fatalf("set current step: %v", err)
	}

	// Step 1: Advance translate → review
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", job.ID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Step 2: Reviewer rejects → goes back to translate
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/reject", job.ID), nil, reviewerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var rejectResp struct {
		Data struct {
			Status         string `json:"status"`
			ReopenedStepID int64  `json:"reopened_step_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &rejectResp)
	if rejectResp.Data.Status != "rejected" {
		t.Fatalf("expected status 'rejected', got %s", rejectResp.Data.Status)
	}

	// Verify: translate=in_progress (reopened), review=rejected
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/workflow/steps", job.ID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var jobStepsResp struct {
		Data []struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &jobStepsResp)
	if len(jobStepsResp.Data) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(jobStepsResp.Data))
	}
	if jobStepsResp.Data[0].Status != "in_progress" {
		t.Errorf("expected translate reopened (in_progress), got %s", jobStepsResp.Data[0].Status)
	}
	if jobStepsResp.Data[1].Status != "rejected" {
		t.Errorf("expected review rejected, got %s", jobStepsResp.Data[1].Status)
	}
}

// TestIntegrationAssignStep tests assigning a step to a different user.
func TestIntegrationAssignStep(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Register user
	ownerID, tenantID := testutil.RegisterUser(t, db.New(pool), "assign-owner")

	// Create another user to reassign to
	newUser := testutil.SeedUserWithRole(t, q, tenantID, "assign-new@test.com", "pw", "member")
	newUserID := newUser.ID

	// Create template + steps
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Assign Template",
	}, ownerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)

	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", tmplResp.Data.ID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "member"}),
		},
	}, ownerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var stepsResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &stepsResp)

	// Create project + file + job
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Assign Project",
		"source_language":  "en",
		"target_languages": []string{"es"},
	}, ownerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)

	file := testutil.SeedFile(t, q, projResp.Data.ID, "assign.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	// Create step assignment assigned to owner
	assign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: stepsResp.Data[0].ID, AssignedTo: &ownerID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	// Reassign to new user
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/steps/%d/assign", job.ID, assign.ID), map[string]any{
		"user_id": newUserID,
	}, ownerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var assignResp struct {
		Data struct {
			Assigned bool `json:"assigned"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &assignResp)
	if !assignResp.Data.Assigned {
		t.Error("expected assigned=true")
	}

	// Verify via DB that the assignment changed
	assignments, err := q.ListJobStepAssignments(ctx, job.ID)
	if err != nil {
		t.Fatalf("list assignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(assignments))
	}
	if assignments[0].AssignedTo == nil || *assignments[0].AssignedTo != newUserID {
		t.Errorf("expected assignment to user %d, got %v", newUserID, assignments[0].AssignedTo)
	}
}

// TestIntegrationRejectWithReason tests that a rejection reason is stored and returned.
func TestIntegrationRejectWithReason(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Register translator
	translatorID, tenantID := testutil.RegisterUser(t, db.New(pool), "reason-translator")

	reviewer := testutil.SeedUserWithRole(t, q, tenantID, "reason-reviewer@test.com", "pw", "member")
	reviewerID := reviewer.ID

	// Create template + steps
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Reason Template",
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", templateID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "member"}),
			testutil.LibraryStep(t, q, tenantID, "review", map[string]any{"role_required": "member"}),
		},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var stepsResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &stepsResp)
	translateStepID := stepsResp.Data[0].ID
	reviewStepID := stepsResp.Data[1].ID

	// Create project + file + job
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Reason Project",
		"source_language":  "en",
		"target_languages": []string{"fr"},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)

	file := testutil.SeedFile(t, q, projResp.Data.ID, "reason.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")

	// Create assignments
	translateAssign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: translateStepID, AssignedTo: &translatorID, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("create translate assignment: %v", err)
	}
	_, err = q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: reviewStepID, AssignedTo: &reviewerID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create review assignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &translateAssign.ID}); err != nil {
		t.Fatalf("set current step: %v", err)
	}

	// Advance translate → review
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", job.ID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Reject with reason
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/reject", job.ID), map[string]string{
		"reason": "translations are too literal",
	}, reviewerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var rejectResp struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &rejectResp)
	if rejectResp.Data.Status != "rejected" {
		t.Fatalf("expected status 'rejected', got %s", rejectResp.Data.Status)
	}

	// Verify rejection_reason via list job steps
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/workflow/steps", job.ID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var jobStepsResp struct {
		Data []struct {
			StepName        string `json:"step_name"`
			Status          string `json:"status"`
			RejectionReason string `json:"rejection_reason"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &jobStepsResp)
	if len(jobStepsResp.Data) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(jobStepsResp.Data))
	}
	// Review step (index 1) should have the rejection reason
	if jobStepsResp.Data[1].RejectionReason != "translations are too literal" {
		t.Errorf("expected rejection_reason 'translations are too literal', got %q", jobStepsResp.Data[1].RejectionReason)
	}
	if jobStepsResp.Data[1].Status != "rejected" {
		t.Errorf("expected review step rejected, got %s", jobStepsResp.Data[1].Status)
	}
}

// TestIntegrationDefaultAssigneeOnAdvance tests that advancing to the next step uses the default_assignee from the template.
func TestIntegrationDefaultAssigneeOnAdvance(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Register translator
	translatorID, tenantID := testutil.RegisterUser(t, db.New(pool), "defassign-translator")

	// Create reviewer who will be the default assignee
	reviewer := testutil.SeedUserWithRole(t, q, tenantID, "defassign-reviewer@test.com", "pw", "member")
	reviewerID := reviewer.ID

	// Create template + workflow steps with default_assignee on review step
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "DefAssign Template",
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", templateID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "member"}),
			testutil.LibraryStep(t, q, tenantID, "review", map[string]any{"role_required": "member", "default_assignee": reviewerID}),
		},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var stepsResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &stepsResp)
	translateStepID := stepsResp.Data[0].ID
	reviewStepID := stepsResp.Data[1].ID

	// Create project + file + job
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "DefAssign Project",
		"source_language":  "en",
		"target_languages": []string{"de"},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)

	file := testutil.SeedFile(t, q, projResp.Data.ID, "defassign.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// Create step assignments — review step starts without assigned_to (will be filled by default_assignee)
	translateAssign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: translateStepID, AssignedTo: &translatorID, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("create translate assignment: %v", err)
	}
	_, err = q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: reviewStepID, AssignedTo: nil, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create review assignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &translateAssign.ID}); err != nil {
		t.Fatalf("set current step: %v", err)
	}

	// Advance translate → review
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", job.ID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Verify the review step's assigned_to was set from default_assignee
	assignments, err := q.ListJobStepAssignments(ctx, job.ID)
	if err != nil {
		t.Fatalf("list assignments: %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(assignments))
	}
	// Review step (index 1) should be assigned to reviewerID via default_assignee
	if assignments[1].AssignedTo == nil || *assignments[1].AssignedTo != reviewerID {
		t.Errorf("expected review step assigned_to=%d (default_assignee), got %v", reviewerID, assignments[1].AssignedTo)
	}
	if assignments[1].Status != "in_progress" {
		t.Errorf("expected review step in_progress, got %s", assignments[1].Status)
	}
}

// TestIntegrationAssigneeOrManagerCanAdvance pins the hard lock on advance /
// reject: the assignee, or any manager, may act; a
// linguist who is not the assignee may not — even on an unassigned step.
func TestIntegrationAssigneeOrManagerCanAdvance(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "Lock")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@lock.test", "pw", "project_manager")
	translator := testutil.SeedUserWithRole(t, q, tenant.ID, "tr@lock.test", "pw", "linguist")
	other := testutil.SeedUserWithRole(t, q, tenant.ID, "other@lock.test", "pw", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Lock", "en", []string{"es"})

	// newJob seeds a job whose active step has the given assignee, and gives
	// `other` a pending later step on it: the linguist job gate then lets
	// `other` through, so their 403 has to come from the step lock itself.
	// Returns the job and its active step assignment.
	newJob := func(name string, assignee *int64) (db.Job, int64) {
		file := testutil.SeedFile(t, q, project.ID, name, db.FileFormatTxt)
		job := testutil.SeedJob(t, q, file.ID, "es")
		activeID := testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, assignee)
		later, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: project.SettingsProfileID, Name: "review", StepOrder: 1, RoleRequired: "linguist",
		})
		if err != nil {
			t.Fatalf("create later step: %v", err)
		}
		if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID: job.ID, WorkflowStepID: later.ID, AssignedTo: &other.ID, Status: "pending",
		}); err != nil {
			t.Fatalf("assign other to later step: %v", err)
		}
		return job, activeID
	}
	advance := func(job db.Job, user db.User, role string) int {
		rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", job.ID), nil, user.ID, tenant.ID, role)
		return rec.Code
	}
	// otherRefusedByStepLock asserts `other` gets the step lock's 403 and the
	// job's active step does not move.
	otherRefusedByStepLock := func(job db.Job, activeID int64, what string) {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", job.ID), nil, other.ID, tenant.ID, "linguist")
		const lockMsg = "only the assigned user or a manager can complete this step"
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), lockMsg) {
			t.Errorf("%s: want 403 %q, got %d %s", what, lockMsg, rec.Code, rec.Body.String())
		}
		active, err := q.GetActiveStepForJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("%s: read active step: %v", what, err)
		}
		if active.ID != activeID {
			t.Errorf("%s: active step moved from assignment %d to %d", what, activeID, active.ID)
		}
	}

	// Unassigned step: a linguist may not push it; a project manager may.
	unassigned, unassignedStep := newJob("u.txt", nil)
	otherRefusedByStepLock(unassigned, unassignedStep, "linguist on an unassigned step")
	if code := advance(unassigned, pm, "project_manager"); code != http.StatusOK {
		t.Errorf("project manager on an unassigned step: want 200, got %d", code)
	}

	// Assigned step: the assignee and a manager may act, another linguist may not.
	assigned, assignedStep := newJob("a.txt", &translator.ID)
	otherRefusedByStepLock(assigned, assignedStep, "non-assignee linguist")
	if code := advance(assigned, pm, "project_manager"); code != http.StatusOK {
		t.Errorf("manager on someone else's step: want 200, got %d", code)
	}
	assigned2, _ := newJob("b.txt", &translator.ID)
	if code := advance(assigned2, translator, "linguist"); code != http.StatusOK {
		t.Errorf("assignee: want 200, got %d", code)
	}
}

// TestIntegration_WorkflowHandlers_CrossTenantReturns404 pins the tenant boundary
// across all four /api/jobs/{jobID}/workflow/* handlers: each one
// returns 404 to another tenant and changes nothing.
//
// The subtests that send tenant A's job id are refused by the
// RequireJobOwnership middleware on /api/jobs/{jobID} before any handler
// runs, so they pin that middleware. AssignStepViaOwnJob sends tenant B's own
// job id with tenant A's step id: the middleware lets it through, and only
// the assign service's check that the step belongs to the job refuses it.
func TestIntegration_WorkflowHandlers_CrossTenantReturns404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	// Tenant A — owns the workflow + job + step assignments. Two members so
	// the assign-step path has a valid in-tenant assignee target.
	userA, tenantA := testutil.RegisterUser(t, db.New(pool), "wfx-a")

	// Second tenant-A user, used as the assign target.
	memberA := testutil.SeedUserWithRole(t, q, tenantA, "wfx-a-member@test.com", "pw", "project_manager")

	// Template + steps for tenant A.
	rec := testutil.DoRequest(t, router, "POST", "/api/templates",
		map[string]any{"name": "WFX Template"}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/templates/%d/workflow-steps", tmplResp.Data.ID),
		map[string]any{
			"steps": []map[string]any{
				testutil.LibraryStep(t, q, tenantA, "translate", map[string]any{"role_required": "member"}),
				testutil.LibraryStep(t, q, tenantA, "review", map[string]any{"role_required": "member"}),
			},
		}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var stepsResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &stepsResp)
	translateStepID := stepsResp.Data[0].ID
	reviewStepID := stepsResp.Data[1].ID

	// Project + file + job for tenant A.
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "WFX Project",
		"source_language":  "en",
		"target_languages": []string{"de"},
	}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	fileA := testutil.SeedFile(t, q, projResp.Data.ID, "wfx.txt", db.FileFormatTxt)
	jobA := testutil.SeedJob(t, q, fileA.ID, "de")

	translateAssign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: jobA.ID, WorkflowStepID: translateStepID, AssignedTo: &userA, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("create translate assignment: %v", err)
	}
	if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: jobA.ID, WorkflowStepID: reviewStepID, AssignedTo: &userA, Status: "pending",
	}); err != nil {
		t.Fatalf("create review assignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobA.ID, CurrentStepID: &translateAssign.ID}); err != nil {
		t.Fatalf("set current step: %v", err)
	}

	// Tenant B — attacker. Registered separately.
	userB, tenantB := testutil.RegisterUser(t, db.New(pool), "wfx-b")

	t.Run("ListJobSteps", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/workflow/steps", jobA.ID), nil, userB, tenantB)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("AssignStep", func(t *testing.T) {
		// Snapshot before so we can prove no mutation.
		before, err := q.GetCurrentStepForJob(ctx, translateAssign.ID)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}

		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/workflow/steps/%d/assign", jobA.ID, translateAssign.ID),
			map[string]any{"user_id": memberA.ID}, // a real tenant-A user, just to prove the bug isn't masked by FK
			userB, tenantB,
		)
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		got, err := q.GetCurrentStepForJob(ctx, translateAssign.ID)
		if err != nil {
			t.Fatalf("readback: %v", err)
		}
		if got.AssignedTo == nil || *got.AssignedTo != *before.AssignedTo {
			t.Fatalf("cross-tenant assign leaked through: assigned_to changed from %v to %v",
				before.AssignedTo, got.AssignedTo)
		}
	})

	t.Run("AssignStepViaOwnJob", func(t *testing.T) {
		// Tenant B's own job and member pass the job-ownership middleware and
		// the assignee check; only tenant A's step id is foreign.
		projectB := testutil.SeedProject(t, q, tenantB, "WFX B Project", "en", []string{"de"})
		fileB := testutil.SeedFile(t, q, projectB.ID, "wfx-b.txt", db.FileFormatTxt)
		jobB := testutil.SeedJob(t, q, fileB.ID, "de")
		memberB := testutil.SeedUserWithRole(t, q, tenantB, "wfx-b-member@test.com", "pw", "project_manager")

		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/workflow/steps/%d/assign", jobB.ID, translateAssign.ID),
			map[string]any{"user_id": memberB.ID},
			userB, tenantB,
		)
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		got, err := q.GetCurrentStepForJob(ctx, translateAssign.ID)
		if err != nil {
			t.Fatalf("readback: %v", err)
		}
		if got.AssignedTo == nil {
			t.Fatalf("assign through tenant B's own job cleared tenant A's step assignee, want %d", userA)
		}
		if *got.AssignedTo != userA {
			t.Fatalf("assign through tenant B's own job reached tenant A's step: assigned_to = %d, want %d",
				*got.AssignedTo, userA)
		}
	})

	t.Run("AdvanceStep", func(t *testing.T) {
		before, err := q.GetCurrentStepForJob(ctx, translateAssign.ID)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}

		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/workflow/advance", jobA.ID), nil, userB, tenantB)
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		got, err := q.GetCurrentStepForJob(ctx, translateAssign.ID)
		if err != nil {
			t.Fatalf("readback: %v", err)
		}
		if got.Status != before.Status {
			t.Fatalf("cross-tenant advance leaked through: status changed from %s to %s",
				before.Status, got.Status)
		}
	})

	t.Run("RejectStep", func(t *testing.T) {
		before, err := q.GetCurrentStepForJob(ctx, translateAssign.ID)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}

		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/workflow/reject", jobA.ID), nil, userB, tenantB)
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		got, err := q.GetCurrentStepForJob(ctx, translateAssign.ID)
		if err != nil {
			t.Fatalf("readback: %v", err)
		}
		if got.Status != before.Status {
			t.Fatalf("cross-tenant reject leaked through: status changed from %s to %s",
				before.Status, got.Status)
		}
	})
}

// A rejection hands work back to a person: the ai step between two human
// steps is skipped, and with only an ai step behind the reviewer there is
// nothing to reopen.
func TestIntegrationRejectSkipsAISteps(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "RejectAI")
	translator := testutil.SeedUserWithRole(t, q, tenant.ID, "tr@ai.test", "pw", "linguist")
	reviewer := testutil.SeedUserWithRole(t, q, tenant.ID, "rv@ai.test", "pw", "linguist")
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "APE", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	aiRouterID := testutil.SeedRouterForEngine(t, q, tenant.ID, engine.ID)
	project := testutil.SeedProject(t, q, tenant.ID, "Reject AI", "en", []string{"fr"})

	seed := func(fileName string, specs []struct {
		name, stepType string
		router         *int64
	}, activeIdx int, assignees []*int64) (db.Job, []int64) {
		file := testutil.SeedFile(t, q, project.ID, fileName, db.FileFormatTxt)
		job := testutil.SeedJob(t, q, file.ID, "fr")
		var ids []int64
		for i, sp := range specs {
			ws, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
				SettingsProfileID: project.SettingsProfileID, Name: sp.name, StepOrder: int32(i),
				RoleRequired: "linguist", AiRouterID: sp.router, StepType: sp.stepType,
			})
			if err != nil {
				t.Fatal(err)
			}
			status := "pending"
			switch {
			case i < activeIdx:
				status = "completed"
			case i == activeIdx:
				status = "in_progress"
			}
			a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
				JobID: job.ID, WorkflowStepID: ws.ID, Status: status, AssignedTo: assignees[i],
			})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, a.ID)
		}
		if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &ids[activeIdx]}); err != nil {
			t.Fatal(err)
		}
		return job, ids
	}
	type spec = struct {
		name, stepType string
		router         *int64
	}

	t.Run("human → ai → human: reject reopens the first human step", func(t *testing.T) {
		job, ids := seed("skip.txt", []spec{
			{"Translation", "human", nil}, {"Auto APE", "ai", &aiRouterID}, {"Review", "human", nil},
		}, 2, []*int64{&translator.ID, nil, &reviewer.ID})
		// Each profile step needs its own row set; clear the shared profile
		// afterwards so the next subtest starts clean.
		defer func() {
			if err := q.DeleteSettingsWorkflowSteps(ctx, project.SettingsProfileID); err != nil {
				t.Fatal(err)
			}
		}()
		rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/reject", job.ID),
			map[string]any{"reason": "typos"}, reviewer.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusOK)
		got, err := q.GetJob(ctx, job.ID)
		if err != nil || got.CurrentStepID == nil || *got.CurrentStepID != ids[0] {
			t.Fatalf("expected job back on Translation (%d), got %v (err=%v)", ids[0], got.CurrentStepID, err)
		}
		rows, _ := q.ListJobStepAssignments(ctx, job.ID)
		// The AI step in between runs again on the way forward.
		if rows[0].Status != "in_progress" || rows[1].Status != "pending" || rows[2].Status != "rejected" {
			t.Errorf("expected in_progress / pending / rejected, got %q / %q / %q", rows[0].Status, rows[1].Status, rows[2].Status)
		}
	})

	t.Run("ai → human: nothing human to reopen → 400", func(t *testing.T) {
		job, _ := seed("none.txt", []spec{
			{"Auto APE", "ai", &aiRouterID}, {"Review", "human", nil},
		}, 1, []*int64{nil, &reviewer.ID})
		rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/reject", job.ID),
			nil, reviewer.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	})
}
