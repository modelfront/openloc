package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Job completion after a workflow: the FE advances every workflow step, then
// POSTs /jobs/{id}/complete (the call CATEditor chains after
// "workflow_completed"). Completion writes the human-approved rows to the
// write TMs and flips the job to completed.

// seedCompletionJob seeds a project + file + one human-approved translation +
// a read_write TM, returning what the tests need.
func seedCompletionJob(t *testing.T, q db.Querier, pool *pgxpool.Pool, tenantID int64, name string) (project db.Project, jobID int64, tmID int64) {
	t.Helper()
	ctx := context.Background()
	project = testutil.SeedProject(t, q, tenantID, name, "en", []string{"de"})
	tm := testutil.SeedTM(t, q, tenantID, name+" TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("seed TM assignment: %v", err)
	}
	f := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, f.ID, "Hello world", 0)
	job := testutil.SeedJob(t, q, f.ID, "de")
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusApproved)
	if _, err := pool.Exec(ctx,
		`UPDATE translations SET active_text = $1, status = 'approved' WHERE id = $2`, "Hallo Welt", tr.ID,
	); err != nil {
		t.Fatalf("set approved active_text: %v", err)
	}
	return project, job.ID, tm.ID
}

// seedPlainWorkflow creates plain human steps in order (step 0 in_progress +
// current, rest pending), all assigned to assignee.
func seedPlainWorkflow(t *testing.T, q db.Querier, spID, jobID, assignee int64, names ...string) {
	t.Helper()
	ctx := context.Background()
	for i, name := range names {
		step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: spID, Name: name, StepOrder: int32(i), RoleRequired: "linguist",
		})
		if err != nil {
			t.Fatalf("seedPlainWorkflow CreateSettingsWorkflowStep %q: %v", name, err)
		}
		status := "pending"
		if i == 0 {
			status = "in_progress"
		}
		a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID: jobID, WorkflowStepID: step.ID, AssignedTo: &assignee, Status: status,
		})
		if err != nil {
			t.Fatalf("seedPlainWorkflow assign %q: %v", name, err)
		}
		if i == 0 {
			if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &a.ID}); err != nil {
				t.Fatalf("seedPlainWorkflow SetJobCurrentStep: %v", err)
			}
		}
	}
}

// advanceAll advances the job's workflow n times, asserting the last advance
// reports workflow_completed. Each step takes away the previous step's
// approvals, so
// the last step re-confirms every segment with text before it completes.
func advanceAll(t *testing.T, router http.Handler, pool *pgxpool.Pool, jobID, userID, tenantID int64, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if i == n-1 {
			confirmJobInStep(t, pool, jobID)
		}
		rec := testutil.DoRequestWithRole(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/workflow/advance", jobID), nil, userID, tenantID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		if i == n-1 {
			var body struct {
				Data struct {
					Status string `json:"status"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode advance response: %v (raw: %s)", err, rec.Body.String())
			}
			if body.Data.Status != "workflow_completed" {
				t.Fatalf("want final advance status=workflow_completed, got %q", body.Data.Status)
			}
		}
	}
}

// confirmJobInStep confirms every segment of the job that has text, as a
// reviewer's Approve all in the current step: text an AI verdict approved and
// no person touched goes back to auto-approved, the rest is approved by a person.
func confirmJobInStep(t *testing.T, pool *pgxpool.Pool, jobID int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE translations t SET status = CASE
			WHEN t.edited_by IS NULL AND t.predict_outcome IN ('approved', 'ape_approved') THEN 'auto_approved'
			ELSE 'approved' END::translation_status, confirmed_via = 'user'
		FROM segments s, jobs j
		WHERE j.id = $1 AND s.file_id = j.file_id AND t.segment_id = s.id
		  AND t.target_language = j.target_language AND t.active_text IS NOT NULL
		  AND t.status = 'needs_review'`, jobID); err != nil {
		t.Fatalf("confirm job in step: %v", err)
	}
}

// Completing a job after its workflow saves the approved row to the TM and
// flips the job to completed.
func TestIntegrationComplete_AfterWorkflow_SavesTMAndCompletes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "CompleteTMCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@completetm.test", "pass", "owner")

	project, jobID, tmID := seedCompletionJob(t, q, pool, tenant.ID, "Complete TM Project")
	seedPlainWorkflow(t, q, project.SettingsProfileID, jobID, owner.ID, "translation", "review")
	advanceAll(t, router, pool, jobID, owner.ID, tenant.ID, 2)

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/complete", jobID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var tmCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM translation_memory WHERE tm_id = $1`, tmID).Scan(&tmCount); err != nil {
		t.Fatalf("count TM entries: %v", err)
	}
	if tmCount == 0 {
		t.Error("job completion must save approved segments to TM")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("read job status: %v", err)
	}
	if status != "completed" {
		t.Errorf("job status = %q, want completed", status)
	}
}

// approved_count — in the /complete response and in the job.completed event
// the project History screen renders as "N approved, M saved to TM" — counts
// the segments a HUMAN approved.
func TestIntegrationComplete_ReportsHumanApprovedCount(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ApprovedCountCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@approvedcount.test", "pass", "owner")

	project, jobID, _ := seedCompletionJob(t, q, pool, tenant.ID, "Approved Count Project")
	// A second human-approved segment, so the count can't be mistaken for a
	// flag.
	job, err := q.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	seg := testutil.SeedSegment(t, q, job.FileID, "Second sentence", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusApproved)
	if _, err := pool.Exec(ctx,
		`UPDATE translations SET active_text = $1, status = 'approved' WHERE id = $2`, "Zweiter Satz", tr.ID,
	); err != nil {
		t.Fatalf("set approved active_text: %v", err)
	}
	// An auto-approved segment is neither counted nor saved to TM.
	autoSeg := testutil.SeedSegment(t, q, job.FileID, "Third sentence", 2)
	autoTr := testutil.SeedTranslation(t, q, autoSeg.ID, "de", db.TranslationStatusAutoApproved)
	if _, err := pool.Exec(ctx,
		`UPDATE translations SET active_text = $1, status = 'auto_approved', predict_outcome = 'approved' WHERE id = $2`, "Dritter Satz", autoTr.ID,
	); err != nil {
		t.Fatalf("set auto_approved active_text: %v", err)
	}

	seedPlainWorkflow(t, q, project.SettingsProfileID, jobID, owner.ID, "translation", "review")
	advanceAll(t, router, pool, jobID, owner.ID, tenant.ID, 2)

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/complete", jobID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		ApprovedCount int `json:"approved_count"`
		TMSavedCount  int `json:"tm_saved_count"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.ApprovedCount != 2 {
		t.Errorf("response approved_count = %d, want 2 (human-approved segments)", resp.ApprovedCount)
	}
	if resp.TMSavedCount != 2 {
		t.Errorf("response tm_saved_count = %d, want 2", resp.TMSavedCount)
	}

	events, err := q.ListProjectEvents(ctx, db.ListProjectEventsParams{
		ProjectID: project.ID, TenantID: tenant.ID, Limit: 50,
	})
	if err != nil {
		t.Fatalf("list project events: %v", err)
	}
	var found bool
	for _, e := range events {
		if e.EventType != "job.completed" {
			continue
		}
		found = true
		var details struct {
			ApprovedCount int `json:"approved_count"`
			TMSavedCount  int `json:"tm_saved_count"`
		}
		if err := json.Unmarshal(e.Details, &details); err != nil {
			t.Fatalf("decode job.completed details: %v (raw: %s)", err, e.Details)
		}
		if details.ApprovedCount != 2 || details.TMSavedCount != 2 {
			t.Errorf("job.completed details = %+v, want 2 approved, 2 saved", details)
		}
	}
	if !found {
		t.Fatal("no job.completed event emitted")
	}
}

// A segment confirmed in an earlier step but not in the last one stays out of
// TM: each step takes away the previous step's approvals, and job completion writes
// only what is confirmed when the workflow ends.
func TestIntegrationComplete_EarlierStepConfirmNotSavedToTM(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "EarlierStepTMCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@earlierstep.test", "pass", "owner")

	// Approved in step 1 (seeded), then translation → review with no
	// confirm in review.
	project, jobID, tmID := seedCompletionJob(t, q, pool, tenant.ID, "Earlier Step Project")
	seedPlainWorkflow(t, q, project.SettingsProfileID, jobID, owner.ID, "translation", "review")
	for i := 0; i < 2; i++ {
		rec := testutil.DoRequestWithRole(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/workflow/advance", jobID), nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
	}

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/complete", jobID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var tmCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM translation_memory WHERE tm_id = $1`, tmID).Scan(&tmCount); err != nil {
		t.Fatalf("count TM entries: %v", err)
	}
	if tmCount != 0 {
		t.Errorf("a segment the last step did not confirm reached TM: %d entries", tmCount)
	}
}
