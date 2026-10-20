package flowtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hibiken/asynq"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/settings"
	"openloc/internal/systemworkflow"
	"openloc/internal/testutil"
	"openloc/internal/worker"
	"openloc/internal/workflow"
)

// donePipeline is a pre-translation run that succeeds without writing text.
type donePipeline struct{}

func (donePipeline) TranslateFile(context.Context, int64, int64, string, string, pipeline.ProgressFunc, pipeline.TranslatePhases) (pipeline.TranslateOutcome, error) {
	return pipeline.TranslateOutcome{}, nil
}
func (donePipeline) RunAIStep(context.Context, int64, int64, pipeline.ProgressFunc) (pipeline.AIStepResult, error) {
	return pipeline.AIStepResult{}, nil
}

type idResp struct {
	Data struct {
		ID int64 `json:"id"`
	} `json:"data"`
}

// TestIntegrationPretranslateOnlyWorkflow walks the built-in Pre-translation
// only workflow through the API: it stays out of the library, a project picks
// it with MT forced on, an upload pre-translates and completes itself, and a
// manager's edit reopens the task.
func TestIntegrationPretranslateOnlyWorkflow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	ownerID, tenantID := testutil.RegisterUser(t, q, "preonly")
	for range 2 { // provisioning is idempotent
		if err := systemworkflow.EnsurePretranslate(ctx, q, tenantID); err != nil {
			t.Fatalf("ensure: %v", err)
		}
	}
	pm := testutil.SeedUserWithRole(t, q, tenantID, "pm@preonly.test", "pw", "project_manager")
	ling := testutil.SeedUserWithRole(t, q, tenantID, "ling@preonly.test", "pw", "linguist")

	asPM := func(method, path string, body any) *httptest.ResponseRecorder {
		return testutil.DoRequestWithRole(t, router, method, path, body, pm.ID, tenantID, "project_manager")
	}

	// The built-in workflow, as the Raw MT mode finds it.
	rec := asPM("GET", "/api/workflows/pretranslate-only", nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var sys struct {
		Data struct {
			ID        int64   `json:"id"`
			Name      string  `json:"name"`
			SystemKey *string `json:"system_key"`
			Steps     []struct {
				StepDefinitionID int64  `json:"step_definition_id"`
				StepType         string `json:"step_type"`
			} `json:"steps"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &sys)
	if len(sys.Data.Steps) != 1 || sys.Data.Steps[0].StepType != "pretranslate" {
		t.Fatalf("built-in workflow should be one pretranslate step, got %+v", sys.Data.Steps)
	}
	if sys.Data.SystemKey == nil || *sys.Data.SystemKey != systemworkflow.WorkflowKey {
		t.Fatalf("built-in workflow should carry its system key, got %v", sys.Data.SystemKey)
	}
	sysID, sysStepID := sys.Data.ID, sys.Data.Steps[0].StepDefinitionID

	t.Run("only managers can read it", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "GET", "/api/workflows/pretranslate-only", nil, ling.ID, tenantID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
	})

	t.Run("it is hidden from and locked in the library", func(t *testing.T) {
		// A library step and workflow of the tenant's own, which the lists
		// must keep showing.
		rec := asPM("POST", "/api/step-definitions", map[string]any{"name": "Proofread", "step_type": "human"})
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var ownStep idResp
		testutil.DecodeJSON(t, rec, &ownStep)
		rec = asPM("POST", "/api/workflows", map[string]any{
			"name": "Proofread only", "steps": []map[string]any{{"step_definition_id": ownStep.Data.ID}},
		})
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var ownWorkflow idResp
		testutil.DecodeJSON(t, rec, &ownWorkflow)

		listIDs := func(path string) map[int64]bool {
			t.Helper()
			var list struct {
				Data []struct {
					ID int64 `json:"id"`
				} `json:"data"`
			}
			rec := asPM("GET", path, nil)
			testutil.AssertStatus(t, rec, http.StatusOK)
			testutil.DecodeJSON(t, rec, &list)
			ids := map[int64]bool{}
			for _, d := range list.Data {
				ids[d.ID] = true
			}
			return ids
		}
		if wfs := listIDs("/api/workflows"); !wfs[ownWorkflow.Data.ID] || wfs[sysID] {
			t.Errorf("workflow list should hold the tenant's own workflow and not the built-in one, got %v", wfs)
		}
		if defs := listIDs("/api/step-definitions"); !defs[ownStep.Data.ID] || defs[sysStepID] {
			t.Errorf("step list should hold the tenant's own step and not the built-in one, got %v", defs)
		}

		testutil.AssertStatus(t, asPM("GET", fmt.Sprintf("/api/workflows/%d", sysID), nil), http.StatusNotFound)
		testutil.AssertStatus(t, asPM("PUT", fmt.Sprintf("/api/workflows/%d", sysID), map[string]any{
			"name": "Renamed", "steps": []map[string]any{{"step_definition_id": sysStepID}},
		}), http.StatusNotFound)
		testutil.AssertStatus(t, asPM("DELETE", fmt.Sprintf("/api/workflows/%d", sysID), nil), http.StatusNotFound)
		testutil.AssertStatus(t, asPM("PUT", fmt.Sprintf("/api/step-definitions/%d", sysStepID), map[string]any{
			"name": "Renamed", "step_type": "human",
		}), http.StatusNotFound)
		testutil.AssertStatus(t, asPM("DELETE", fmt.Sprintf("/api/step-definitions/%d", sysStepID), nil), http.StatusNotFound)
		// Nor can a library workflow be composed from the built-in step.
		testutil.AssertStatus(t, asPM("POST", "/api/workflows", map[string]any{
			"name": "Mine", "steps": []map[string]any{{"step_definition_id": sysStepID}},
		}), http.StatusBadRequest)

		wf, err := systemworkflow.Pretranslate(ctx, q, tenantID)
		if err != nil || wf.Name != systemworkflow.WorkflowName {
			t.Fatalf("the built-in workflow should be untouched, got %+v (err=%v)", wf, err)
		}
	})

	engine := testutil.SeedMTEngine(t, q, tenantID, "Mock", "mock")
	routerID := testutil.SeedMTRouter(t, q, tenantID, "Mock router", &engine.ID, nil)

	// Pre-translation fills files in with MT, so a live project needs a
	// router to translate with.
	t.Run("a live project without an MT router is refused", func(t *testing.T) {
		before, err := q.ListProjects(ctx, tenantID)
		if err != nil {
			t.Fatal(err)
		}
		rec := asPM("POST", "/api/projects", map[string]any{
			"name": "No router", "source_language": "en", "target_languages": []string{"de"},
			"workflow_id": sysID,
		})
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		after, err := q.ListProjects(ctx, tenantID)
		if err != nil {
			t.Fatal(err)
		}
		if len(after) != len(before) {
			t.Errorf("a refused create must leave no project behind, got %d before / %d after", len(before), len(after))
		}
	})

	t.Run("publishing a draft without an MT router is refused", func(t *testing.T) {
		rec := asPM("POST", "/api/projects", map[string]any{
			"status": "draft", "name": "Draft no router", "source_language": "en", "target_languages": []string{"de"},
			"workflow_id": sysID,
		})
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var draft idResp
		testutil.DecodeJSON(t, rec, &draft)
		testutil.AssertStatus(t, asPM("POST", fmt.Sprintf("/api/projects/%d/publish", draft.Data.ID), nil), http.StatusBadRequest)
		p, err := q.GetProject(ctx, db.GetProjectParams{ID: draft.Data.ID, TenantID: tenantID})
		if err != nil || p.Status != db.ProjectStatusDraft {
			t.Fatalf("a refused publish must leave the draft, got %q (err=%v)", p.Status, err)
		}
		// With a router it publishes.
		testutil.AssertStatus(t, asPM("PATCH", fmt.Sprintf("/api/projects/%d/settings", draft.Data.ID),
			map[string]any{"mt_router_id": routerID}), http.StatusOK)
		testutil.AssertStatus(t, asPM("POST", fmt.Sprintf("/api/projects/%d/publish", draft.Data.ID), nil), http.StatusOK)
	})

	// A library workflow to switch between, and the steps a profile has.
	review, err := q.GetStepDefinitionByName(ctx, db.GetStepDefinitionByNameParams{TenantID: tenantID, Name: "Review"})
	if err != nil {
		t.Fatal(err)
	}
	rec = asPM("POST", "/api/workflows", map[string]any{
		"name": "Review only", "steps": []map[string]any{{"step_definition_id": review.ID}},
	})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var libWorkflow idResp
	testutil.DecodeJSON(t, rec, &libWorkflow)
	isPretranslateOnly := func(profileID int64) bool {
		t.Helper()
		steps, err := q.ListSettingsWorkflowSteps(ctx, profileID)
		if err != nil {
			t.Fatal(err)
		}
		return settings.IsPretranslateOnly(steps)
	}
	refusedSwitch := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		if !strings.Contains(rec.Body.String(), "cannot switch to or from Pre-translation") {
			t.Errorf("want the switch refusal, got %s", rec.Body.String())
		}
	}

	// Pre-translation is chosen when a project is set up. The project has
	// a router, so the refusal is the switch itself, not a missing router.
	t.Run("a created project cannot switch to it", func(t *testing.T) {
		live := testutil.SeedProject(t, q, tenantID, "Live", "en", []string{"de"})
		testutil.AssertStatus(t, asPM("PATCH", fmt.Sprintf("/api/projects/%d/settings", live.ID),
			map[string]any{"mt_router_id": routerID}), http.StatusOK)
		refusedSwitch(t, asPM("POST", fmt.Sprintf("/api/projects/%d/workflow/apply", live.ID),
			map[string]any{"workflow_id": sysID}))
		refusedSwitch(t, asPM("PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", live.ID),
			map[string]any{"steps": []map[string]any{{"step_definition_id": sysStepID}}}))
		if isPretranslateOnly(live.SettingsProfileID) {
			t.Error("a refused switch must leave the project's steps")
		}
	})

	t.Run("a draft can switch both ways", func(t *testing.T) {
		rec := asPM("POST", "/api/projects", map[string]any{
			"status": "draft", "name": "Draft switch", "source_language": "en", "target_languages": []string{"de"},
		})
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var draft idResp
		testutil.DecodeJSON(t, rec, &draft)
		p, err := q.GetProject(ctx, db.GetProjectParams{ID: draft.Data.ID, TenantID: tenantID})
		if err != nil {
			t.Fatal(err)
		}
		testutil.AssertStatus(t, asPM("POST", fmt.Sprintf("/api/projects/%d/workflow/apply", draft.Data.ID),
			map[string]any{"workflow_id": sysID}), http.StatusOK)
		if !isPretranslateOnly(p.SettingsProfileID) {
			t.Fatal("the draft should be Pre-translation")
		}
		testutil.AssertStatus(t, asPM("POST", fmt.Sprintf("/api/projects/%d/workflow/apply", draft.Data.ID),
			map[string]any{"workflow_id": libWorkflow.Data.ID}), http.StatusOK)
		if isPretranslateOnly(p.SettingsProfileID) {
			t.Error("the draft should be back on the library workflow")
		}
	})

	// A live project on the built-in workflow, created with MT switched off.
	rec = asPM("POST", "/api/projects", map[string]any{
		"name": "Pre-translate only", "source_language": "en", "target_languages": []string{"de"},
		"workflow_id": sysID,
		"settings":    map[string]any{"mt_on_upload": false, "tm_on_upload": true, "mt_router_id": routerID},
	})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created idResp
	testutil.DecodeJSON(t, rec, &created)
	project, err := q.GetProject(ctx, db.GetProjectParams{ID: created.Data.ID, TenantID: tenantID})
	if err != nil {
		t.Fatal(err)
	}
	profileMT := func() bool {
		t.Helper()
		p, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
		if err != nil {
			t.Fatal(err)
		}
		return p.MtOnUpload
	}

	t.Run("creating a project with it switches MT on", func(t *testing.T) {
		steps, err := q.ListSettingsWorkflowSteps(ctx, project.SettingsProfileID)
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) != 1 || steps[0].StepType != "pretranslate" || steps[0].DefaultAssignee != nil {
			t.Fatalf("project should have the single unassigned Pre-translation step, got %+v", steps)
		}
		if !profileMT() {
			t.Error("a Pre-translation project must fill in MT on upload")
		}
	})

	t.Run("a created project cannot leave it", func(t *testing.T) {
		refusedSwitch(t, asPM("POST", fmt.Sprintf("/api/projects/%d/workflow/apply", project.ID),
			map[string]any{"workflow_id": libWorkflow.Data.ID}))
		refusedSwitch(t, asPM("PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
			map[string]any{"steps": []map[string]any{{"step_definition_id": review.ID}}}))
		if !isPretranslateOnly(project.SettingsProfileID) {
			t.Error("a refused switch must leave the single Pre-translation step")
		}
		// A write that keeps the workflow is still a normal edit.
		testutil.AssertStatus(t, asPM("PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
			map[string]any{"steps": []map[string]any{{"step_definition_id": sysStepID}}}), http.StatusOK)
	})

	t.Run("it cannot be combined with other steps", func(t *testing.T) {
		rec := asPM("PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID), map[string]any{
			"steps": []map[string]any{{"step_definition_id": sysStepID}, {"step_definition_id": review.ID}},
		})
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		steps, err := q.ListSettingsWorkflowSteps(ctx, project.SettingsProfileID)
		if err != nil || len(steps) != 1 {
			t.Fatalf("a refused edit must leave the single step, got %d (err=%v)", len(steps), err)
		}
	})

	t.Run("MT cannot be switched off", func(t *testing.T) {
		testutil.AssertStatus(t, asPM("PATCH", fmt.Sprintf("/api/projects/%d/settings", project.ID),
			map[string]any{"mt_on_upload": false}), http.StatusBadRequest)
		if !profileMT() {
			t.Error("a refused settings edit must leave MT on")
		}
		testutil.AssertStatus(t, asPM("PATCH", fmt.Sprintf("/api/projects/%d/settings", project.ID),
			map[string]any{"tm_on_upload": false}), http.StatusOK)
	})

	t.Run("its MT router cannot be cleared", func(t *testing.T) {
		testutil.AssertStatus(t, asPM("PATCH", fmt.Sprintf("/api/projects/%d/settings", project.ID),
			map[string]any{"mt_router_id": 0}), http.StatusBadRequest)
		p, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
		if err != nil || p.MtRouterID == nil || *p.MtRouterID != routerID {
			t.Fatalf("a refused edit must keep the router, got %v (err=%v)", p.MtRouterID, err)
		}
	})

	t.Run("it cannot be saved as a library workflow", func(t *testing.T) {
		testutil.AssertStatus(t, asPM("POST", fmt.Sprintf("/api/projects/%d/workflow/save-as", project.ID),
			map[string]any{"name": "Copy"}), http.StatusBadRequest)
	})

	t.Run("an upload pre-translates, completes itself, and a manager's edit reopens it", func(t *testing.T) {
		rec := testutil.DoFileUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
			"pre.txt", "text/plain", "Hello world.\n\nSecond line.\n", ownerID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var file idResp
		testutil.DecodeJSON(t, rec, &file)
		jobs, err := q.ListJobsByFile(ctx, file.Data.ID)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("expected one job, got %d (err=%v)", len(jobs), err)
		}
		job := jobs[0]
		rows, err := q.ListJobStepAssignments(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Status != "in_progress" || rows[0].AssignedTo != nil {
			t.Fatalf("the Pre-translation task should start unassigned, got %+v", rows)
		}

		// The pre-translation run the upload queued.
		h := worker.NewHandler(donePipeline{}, q).WithWorkflow(workflow.NewService(q, pipeline.NewPgxTxRunner(pool), nil))
		payload, err := json.Marshal(worker.PipelinePayload{
			TenantID: tenantID, JobID: job.ID, FileID: file.Data.ID, SourceLanguage: "en", TargetLanguage: "de",
			Phases: pipeline.TranslatePhases{UseTM: true, UseMT: true}, TriggeredBy: &ownerID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.HandlePipeline(ctx, asynq.NewTask(worker.TaskPipeline, payload)); err != nil {
			t.Fatalf("HandlePipeline: %v", err)
		}
		if rows, _ := q.ListJobStepAssignments(ctx, job.ID); rows[0].Status != "completed" {
			t.Fatalf("the task should complete itself, got %q", rows[0].Status)
		}
		if j, _ := q.GetJob(ctx, job.ID); j.Status != "completed" {
			t.Fatalf("the job should be completed, got %q", j.Status)
		}

		var trID int64
		if err := pool.QueryRow(ctx, `SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id
			WHERE s.file_id = $1 AND t.target_language = 'de' ORDER BY s.position LIMIT 1`, file.Data.ID).Scan(&trID); err != nil {
			t.Fatalf("find translation: %v", err)
		}
		draftPath := fmt.Sprintf("/api/files/%d/translations/%d/draft", file.Data.ID, trID)
		draft := map[string]any{"human_text": "Hallo Welt.", "editing_time_ms": 0}

		// A linguist cannot reopen it.
		rec = testutil.DoRequestWithRole(t, router, "PUT", draftPath, draft, ling.ID, tenantID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)

		// A manager's edit reopens it, through the regular flow.
		rec = asPM("PUT", draftPath, draft)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var body struct {
			TaskReopened bool `json:"task_reopened"`
		}
		testutil.DecodeJSON(t, rec, &body)
		if !body.TaskReopened {
			t.Error("the edit should report the task reopened")
		}
		rows, err = q.ListJobStepAssignments(ctx, job.ID)
		if err != nil || rows[0].Status != "in_progress" {
			t.Fatalf("the task should be back in progress, got %+v (err=%v)", rows, err)
		}
		j, err := q.GetJob(ctx, job.ID)
		if err != nil || j.Status != "in_progress" {
			t.Fatalf("the job should be back in progress, got %q (err=%v)", j.Status, err)
		}
		events, err := q.ListProjectEvents(ctx, db.ListProjectEventsParams{
			ProjectID: project.ID, TenantID: tenantID, Limit: 100, EventPrefix: "workflow.step_reopened",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("expected one workflow.step_reopened event, got %d", len(events))
		}
		var details map[string]any
		if err := json.Unmarshal(events[0].Details, &details); err != nil {
			t.Fatal(err)
		}
		if details["step"] != systemworkflow.StepName || details["step_type"] != "pretranslate" {
			t.Errorf("unexpected reopen event details: %v", details)
		}
	})

	t.Run("a template takes it with MT forced on", func(t *testing.T) {
		rec := asPM("POST", "/api/templates", map[string]any{"name": "Pre-translate template"})
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var tmpl idResp
		testutil.DecodeJSON(t, rec, &tmpl)
		testutil.AssertStatus(t, asPM("PATCH", fmt.Sprintf("/api/templates/%d", tmpl.Data.ID),
			map[string]any{"mt_on_upload": false}), http.StatusOK)
		testutil.AssertStatus(t, asPM("POST", fmt.Sprintf("/api/templates/%d/workflow/apply", tmpl.Data.ID),
			map[string]any{"workflow_id": sysID}), http.StatusOK)
		got, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: tmpl.Data.ID, TenantID: tenantID})
		if err != nil {
			t.Fatal(err)
		}
		p, err := q.GetSettingsProfile(ctx, got.SettingsProfileID)
		if err != nil || !p.MtOnUpload {
			t.Fatalf("applying it must switch MT on, got %v (err=%v)", p.MtOnUpload, err)
		}
		testutil.AssertStatus(t, asPM("PATCH", fmt.Sprintf("/api/templates/%d", tmpl.Data.ID),
			map[string]any{"mt_on_upload": false}), http.StatusBadRequest)
	})
}
