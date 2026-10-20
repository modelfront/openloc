package workflow_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type projectTask struct {
	ID              int64   `json:"id"`
	JobID           int64   `json:"job_id"`
	WorkflowStepID  int64   `json:"workflow_step_id"`
	Status          string  `json:"status"`
	Deadline        *string `json:"deadline"`
	AssignedTo      *int64  `json:"assigned_to"`
	AssigneeName    *string `json:"assignee_name"`
	RejectionReason string  `json:"rejection_reason"`
	HandoffNote     *string `json:"handoff_note"`
	StepName        string  `json:"step_name"`
	StepOrder       int32   `json:"step_order"`
	StepType        string  `json:"step_type"`
}

// seedStep adds a workflow step to a settings profile.
func seedStep(t *testing.T, q db.Querier, profileID int64, name string, order int32) db.WorkflowStep {
	t.Helper()
	s, err := q.CreateSettingsWorkflowStep(context.Background(), db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: profileID, Name: name, StepOrder: order, RoleRequired: "linguist",
	})
	if err != nil {
		t.Fatalf("CreateSettingsWorkflowStep %s: %v", name, err)
	}
	return s
}

// seedAssignment adds one (job, step) task.
func seedAssignment(t *testing.T, q db.Querier, jobID, stepID int64, status string, assignee *int64, deadline pgtype.Timestamptz) int64 {
	t.Helper()
	a, err := q.CreateJobStepAssignment(context.Background(), db.CreateJobStepAssignmentParams{
		JobID: jobID, WorkflowStepID: stepID, AssignedTo: assignee, Status: status, Deadline: deadline,
	})
	if err != nil {
		t.Fatalf("CreateJobStepAssignment: %v", err)
	}
	return a.ID
}

// GET /api/projects/{projectID}/tasks returns one row per (job, step) for
// every job in the project, with the assignee, deadline and status of each —
// including steps that have not started yet, which the jobs list never shows.
func TestIntegrationListProjectTasks(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "MatrixCo")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@matrix.test", "pw", "project_manager")
	translator := testutil.SeedUserWithRole(t, q, tenant.ID, "tr@matrix.test", "pw", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Matrix", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "guide.txt", db.FileFormatTxt)
	de := testutil.SeedJob(t, q, file.ID, "de")
	fr := testutil.SeedJob(t, q, file.ID, "fr")

	translate := seedStep(t, q, project.SettingsProfileID, "translate", 0)
	review := seedStep(t, q, project.SettingsProfileID, "review", 1)

	deadline := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	noDeadline := pgtype.Timestamptz{}
	deTranslate := seedAssignment(t, q, de.ID, translate.ID, "completed", &translator.ID,
		pgtype.Timestamptz{Time: deadline, Valid: true})
	deReview := seedAssignment(t, q, de.ID, review.ID, "in_progress", nil, noDeadline)
	frTranslate := seedAssignment(t, q, fr.ID, translate.ID, "in_progress", &translator.ID, noDeadline)
	frReview := seedAssignment(t, q, fr.ID, review.ID, "pending", nil, noDeadline)
	// The de job reached Review through a hand-off with a comment.
	comment := "Check the new glossary terms."
	if err := q.StartNextStep(context.Background(), db.StartNextStepParams{ID: deReview, HandoffNote: &comment}); err != nil {
		t.Fatalf("StartNextStep: %v", err)
	}

	// Another tenant's project with its own task — must never show up.
	other := testutil.SeedTenant(t, q, "OtherMatrixCo")
	otherPM := testutil.SeedUserWithRole(t, q, other.ID, "pm@other-matrix.test", "pw", "project_manager")
	otherProject := testutil.SeedProject(t, q, other.ID, "Theirs", "en", []string{"de"})
	otherFile := testutil.SeedFile(t, q, otherProject.ID, "theirs.txt", db.FileFormatTxt)
	otherJob := testutil.SeedJob(t, q, otherFile.ID, "de")
	otherStep := seedStep(t, q, otherProject.SettingsProfileID, "translate", 0)
	seedAssignment(t, q, otherJob.ID, otherStep.ID, "in_progress", nil, noDeadline)

	path := fmt.Sprintf("/api/projects/%d/tasks", project.ID)

	t.Run("manager gets every step of every job", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "GET", path, nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []projectTask `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)

		// Ordered by step, then job: both translate tasks before both review tasks.
		wantIDs := []int64{deTranslate, frTranslate, deReview, frReview}
		if len(resp.Data) != len(wantIDs) {
			t.Fatalf("got %d tasks, want %d: %+v", len(resp.Data), len(wantIDs), resp.Data)
		}
		for i, id := range wantIDs {
			if resp.Data[i].ID != id {
				t.Errorf("task[%d].id = %d, want %d", i, resp.Data[i].ID, id)
			}
		}

		byID := map[int64]projectTask{}
		for _, task := range resp.Data {
			byID[task.ID] = task
		}

		done := byID[deTranslate]
		if done.JobID != de.ID || done.WorkflowStepID != translate.ID || done.StepName != "translate" || done.StepOrder != 0 {
			t.Errorf("de/translate identity wrong: %+v", done)
		}
		if done.Status != "completed" {
			t.Errorf("de/translate status = %q, want completed", done.Status)
		}
		if done.AssigneeName == nil || *done.AssigneeName == "" || done.AssignedTo == nil || *done.AssignedTo != translator.ID {
			t.Errorf("de/translate assignee = %v / %v, want translator %d", done.AssignedTo, done.AssigneeName, translator.ID)
		}
		if done.Deadline == nil {
			t.Fatalf("de/translate deadline missing")
		}
		if got, err := time.Parse(time.RFC3339, *done.Deadline); err != nil || !got.Equal(deadline) {
			t.Errorf("de/translate deadline = %q, want %s", *done.Deadline, deadline.Format(time.RFC3339))
		}

		// The hand-off note sits on the step it was written for.
		if got := byID[deReview].HandoffNote; got == nil || *got != comment {
			t.Errorf("de/review handoff_note = %v, want %q", got, comment)
		}
		if got := byID[deTranslate].HandoffNote; got != nil {
			t.Errorf("de/translate handoff_note = %q, want null", *got)
		}

		// A step nobody has started yet is still a task in the matrix.
		pending := byID[frReview]
		if pending.Status != "pending" || pending.StepName != "review" || pending.JobID != fr.ID {
			t.Errorf("fr/review = %+v, want a pending review task on the fr job", pending)
		}
		if pending.AssignedTo != nil || pending.Deadline != nil {
			t.Errorf("fr/review should be unassigned with no deadline: %+v", pending)
		}
	})

	t.Run("another tenant gets 404, not an empty list", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "GET", path, nil, otherPM.ID, other.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("a linguist is refused", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "GET", path, nil, translator.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
	})
}
