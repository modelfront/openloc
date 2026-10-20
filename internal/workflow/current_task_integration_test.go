package workflow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// seedFailedTwoStepJob is a job on its first of two human steps whose
// automation failed: jobs.status='failed' with an error message.
func seedFailedTwoStepJob(t *testing.T, q db.Querier, prefix, errMsg string) (userID, tenantID, projectID, jobID int64, ids []int64) {
	t.Helper()
	ctx := context.Background()
	userID, tenantID = testutil.RegisterUser(t, q, prefix)
	proj := testutil.SeedProject(t, q, tenantID, prefix+" project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, proj.ID, prefix+".txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	ids = testutil.SeedWorkflow(t, q, proj.SettingsProfileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translate", Assignee: &userID, Status: "in_progress"},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &userID},
	)
	if err := q.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: job.ID, ErrorMessage: &errMsg}); err != nil {
		t.Fatalf("mark job failed: %v", err)
	}
	return userID, tenantID, proj.ID, job.ID, ids
}

// projectJobState reads the job the way the task matrix does: the project's
// jobs list, whose pipeline_status + workflow_step decide which cell is red.
func projectJobState(t *testing.T, router http.Handler, projectID, jobID, userID, tenantID int64) (pipelineStatus string, stepID int64) {
	t.Helper()
	rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/jobs", projectID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data []struct {
			ID             int64  `json:"id"`
			PipelineStatus string `json:"pipeline_status"`
			WorkflowStep   *struct {
				ID int64 `json:"id"`
			} `json:"workflow_step"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	for _, j := range resp.Data {
		if j.ID == jobID {
			if j.WorkflowStep == nil {
				t.Fatalf("job %d has no workflow_step", jobID)
			}
			return j.PipelineStatus, j.WorkflowStep.ID
		}
	}
	t.Fatalf("job %d missing from the project jobs list", jobID)
	return "", 0
}

// stepEventDetails returns the details of the newest event of the given type.
func stepEventDetails(t *testing.T, q db.Querier, projectID, tenantID int64, eventType string) map[string]any {
	t.Helper()
	events, err := q.ListProjectEvents(context.Background(), db.ListProjectEventsParams{
		ProjectID: projectID, TenantID: tenantID, Limit: 20, EventPrefix: eventType,
	})
	if err != nil {
		t.Fatalf("ListProjectEvents: %v", err)
	}
	for _, ev := range events {
		if ev.EventType == eventType {
			var d map[string]any
			if err := json.Unmarshal(ev.Details, &d); err != nil {
				t.Fatalf("decode %s details: %v", eventType, err)
			}
			return d
		}
	}
	t.Fatalf("no %s event recorded", eventType)
	return nil
}

// Completing an errored task accepts its failure: the job leaves 'failed', so
// the error does not reappear on the task that starts next, and the history
// row says an error was dismissed.
func TestIntegrationAdvanceDismissesJobFailure(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	const errMsg = "MT provider returned 500"
	userID, tenantID, projectID, jobID, ids := seedFailedTwoStepJob(t, q, "dismiss-advance", errMsg)

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", jobID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	job, err := q.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if job.Status == "failed" || job.ErrorMessage != nil {
		t.Errorf("completing the errored task must clear the failure; status=%q error=%v", job.Status, job.ErrorMessage)
	}

	status, stepID := projectJobState(t, router, projectID, jobID, userID, tenantID)
	if stepID != ids[1] {
		t.Fatalf("current task = %d, want the Review assignment %d", stepID, ids[1])
	}
	if status == "failed" {
		t.Errorf("the next task must not inherit the error; pipeline_status=%q", status)
	}

	details := stepEventDetails(t, q, projectID, tenantID, "workflow.step_advanced")
	if details["error_dismissed"] != errMsg {
		t.Errorf("step_advanced.error_dismissed = %v, want %q", details["error_dismissed"], errMsg)
	}
}

// Rejecting an errored task back to the previous step clears the failure too;
// otherwise the error moves onto the reopened earlier task.
func TestIntegrationRejectDismissesJobFailure(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := testutil.RegisterUser(t, q, "dismiss-reject")
	proj := testutil.SeedProject(t, q, tenantID, "dismiss-reject project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, proj.ID, "reject.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	ids := testutil.SeedWorkflow(t, q, proj.SettingsProfileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translate", Assignee: &userID, Status: "completed"},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &userID, Status: "in_progress"},
	)
	errMsg := "AI step failed"
	if err := q.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: job.ID, ErrorMessage: &errMsg}); err != nil {
		t.Fatalf("mark job failed: %v", err)
	}

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/reject", job.ID),
		map[string]any{"reason": "redo"}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	status, stepID := projectJobState(t, router, proj.ID, job.ID, userID, tenantID)
	if stepID != ids[0] {
		t.Fatalf("current task = %d, want the reopened Translate assignment %d", stepID, ids[0])
	}
	if status == "failed" {
		t.Errorf("the reopened task must not inherit the error; pipeline_status=%q", status)
	}
}

// A finished workflow with a step appended later (still pending) keeps its
// last completed task as the current one, as the editor and the edit guard do.
func TestIntegrationProjectJobsCurrentTaskPrefersLastCompleted(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	userID, tenantID := testutil.RegisterUser(t, q, "current-task")
	proj := testutil.SeedProject(t, q, tenantID, "current-task project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, proj.ID, "current.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	ids := testutil.SeedWorkflow(t, q, proj.SettingsProfileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translate", Assignee: &userID, Status: "completed"},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &userID, Status: "completed"},
		testutil.WorkflowStepSpec{Name: "Proofread", Assignee: &userID},
	)

	if _, stepID := projectJobState(t, router, proj.ID, job.ID, userID, tenantID); stepID != ids[1] {
		t.Errorf("current task = %d, want the last completed (Review, %d), not the pending Proofread %d", stepID, ids[1], ids[2])
	}
}
