package job_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Clear is allowed up to and including the workflow's first human or hybrid
// step: automated steps before it (Pre-translation, AI) don't count. Every
// clear (segment, job, project) and the job's `last_clear_step_order` (job
// detail and job list) follow the same rule. On a finished workflow the job
// and project clears restart it from its first step, while the segment clear
// judges the last completed step.

type clearWindowCase struct {
	name  string
	steps []testutil.WorkflowStepSpec
	// segmentOK: the segment clear is allowed. bulkOK: the job and project
	// clears are allowed.
	segmentOK, bulkOK bool
	// lastClear is the job's expected `last_clear_step_order`.
	lastClear int
}

func clearWindowCases(assignee *int64) []clearWindowCase {
	step := func(name, typ, status string) testutil.WorkflowStepSpec {
		sp := testutil.WorkflowStepSpec{Name: name, StepType: typ, Status: status}
		if typ == "human" || typ == "hybrid" {
			sp.Assignee = assignee
		}
		return sp
	}
	return []clearWindowCase{
		{
			name: "first human step after pre-translation and AI",
			steps: []testutil.WorkflowStepSpec{
				step("Pre-translation", "pretranslate", "completed"),
				step("AI check", "ai", "completed"),
				step("Translation", "human", "in_progress"),
				step("Review", "human", "pending"),
			},
			segmentOK: true, bulkOK: true, lastClear: 2,
		},
		{
			name: "first hybrid step after pre-translation",
			steps: []testutil.WorkflowStepSpec{
				step("Pre-translation", "pretranslate", "completed"),
				step("Translation", "hybrid", "in_progress"),
			},
			segmentOK: true, bulkOK: true, lastClear: 1,
		},
		{
			name: "review after the first human step",
			steps: []testutil.WorkflowStepSpec{
				step("Pre-translation", "pretranslate", "completed"),
				step("Translation", "human", "completed"),
				step("Review", "human", "in_progress"),
			},
			segmentOK: false, bulkOK: false, lastClear: 1,
		},
		{
			name: "finished workflow whose last step is the first human step",
			steps: []testutil.WorkflowStepSpec{
				step("Pre-translation", "pretranslate", "completed"),
				step("Translation", "human", "completed"),
			},
			segmentOK: true, bulkOK: true, lastClear: 1,
		},
		{
			name: "finished workflow past the first human step",
			steps: []testutil.WorkflowStepSpec{
				step("Pre-translation", "pretranslate", "completed"),
				step("Translation", "human", "completed"),
				step("Review", "human", "completed"),
			},
			segmentOK: false, bulkOK: true, lastClear: 1,
		},
		{
			name: "hybrid step after a first human step",
			steps: []testutil.WorkflowStepSpec{
				step("Translation", "human", "completed"),
				step("Post-edit", "hybrid", "in_progress"),
			},
			segmentOK: false, bulkOK: false, lastClear: 0,
		},
	}
}

// clearWindowJob seeds one project, file, job and translated segment on the
// given workflow.
func clearWindowJob(t *testing.T, q db.Querier, tenantID int64, steps []testutil.WorkflowStepSpec) (projectID, fileID, jobID, translationID int64) {
	t.Helper()
	project := testutil.SeedProject(t, q, tenantID, "Clear Window", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedWorkflow(t, q, project.SettingsProfileID, job.ID, steps...)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 0)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	return project.ID, file.ID, job.ID, tr.ID
}

func TestIntegrationClearWindow_FirstHumanOrHybridStep(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ownerID, tenantID := testutil.RegisterUser(t, q, "clear-window")
	ling := testutil.SeedUserWithRole(t, q, tenantID, "clear-window-ling@test.com", "pw", "linguist")

	for _, tc := range clearWindowCases(&ling.ID) {
		t.Run(tc.name, func(t *testing.T) {
			status := func(ok bool) int {
				if ok {
					return http.StatusOK
				}
				return http.StatusForbidden
			}

			// The job detail and the project's job list report where the window ends.
			projectID, _, jobID, _ := clearWindowJob(t, q, tenantID, tc.steps)
			rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d", jobID), nil, ownerID, tenantID)
			testutil.AssertStatus(t, rec, http.StatusOK)
			var got struct {
				Data struct {
					LastClearStepOrder *int `json:"last_clear_step_order"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if v := got.Data.LastClearStepOrder; v == nil || *v != tc.lastClear {
				t.Errorf("job last_clear_step_order = %s, want %d", intOrNil(v), tc.lastClear)
			}
			rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/jobs", projectID), nil, ownerID, tenantID)
			testutil.AssertStatus(t, rec, http.StatusOK)
			var list struct {
				Data []struct {
					ID                 int64 `json:"id"`
					LastClearStepOrder *int  `json:"last_clear_step_order"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Data) != 1 || list.Data[0].ID != jobID {
				t.Fatalf("job list = %+v, want the one job %d", list.Data, jobID)
			}
			if v := list.Data[0].LastClearStepOrder; v == nil || *v != tc.lastClear {
				t.Errorf("job list last_clear_step_order = %s, want %d", intOrNil(v), tc.lastClear)
			}

			// Segment clear.
			_, fileID, _, trID := clearWindowJob(t, q, tenantID, tc.steps)
			setActiveText(t, pool, trID, "Hallo")
			rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/files/%d/translations/%d/clear", fileID, trID),
				nil, ownerID, tenantID)
			testutil.AssertStatus(t, rec, status(tc.segmentOK))

			// Job clear.
			_, _, jobID, trID = clearWindowJob(t, q, tenantID, tc.steps)
			setActiveText(t, pool, trID, "Hallo")
			rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/clear-translations", jobID),
				map[string]any{"scope": "all"}, ownerID, tenantID)
			testutil.AssertStatus(t, rec, status(tc.bulkOK))

			// Project clear.
			projectID, _, _, trID = clearWindowJob(t, q, tenantID, tc.steps)
			setActiveText(t, pool, trID, "Hallo")
			rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/clear-translations", projectID),
				map[string]any{"scope": "all"}, ownerID, tenantID)
			testutil.AssertStatus(t, rec, status(tc.bulkOK))
		})
	}
}

func intOrNil(v *int) string {
	if v == nil {
		return "missing"
	}
	return fmt.Sprint(*v)
}
