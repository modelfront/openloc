// Library workflow ↔ project: default steps come from the
// library, a project can be created from / switched to a library workflow,
// and its steps can be promoted back into the library. Template twins.
package project_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type stepRow struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	StepType         string `json:"step_type"`
	StepDefinitionID *int64 `json:"step_definition_id"`
	DefaultAssignee  *int64 `json:"default_assignee"`
	AiRouterID       *int64 `json:"ai_router_id"`
}

type settingsResp struct {
	Data struct {
		Project struct {
			WorkflowID   *int64  `json:"workflow_id"`
			WorkflowName *string `json:"workflow_name"`
		} `json:"project"`
		WorkflowSteps []stepRow `json:"workflow_steps"`
	} `json:"data"`
}

func projectSettings(t *testing.T, router http.Handler, projectID, userID, tenantID int64) settingsResp {
	t.Helper()
	rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", projectID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var out settingsResp
	testutil.DecodeJSON(t, rec, &out)
	return out
}

func createProjectWithBody(t *testing.T, router http.Handler, body map[string]any, userID, tenantID int64) int64 {
	t.Helper()
	base := map[string]any{"name": "P", "source_language": "en", "target_languages": []string{"de"}}
	for k, v := range body {
		base[k] = v
	}
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", base, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var out struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &out)
	return out.Data.ID
}

func createLibraryWorkflow(t *testing.T, router http.Handler, name string, defs []int64, userID, tenantID int64) int64 {
	t.Helper()
	steps := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		steps = append(steps, map[string]any{"step_definition_id": d})
	}
	rec := testutil.DoRequest(t, router, "POST", "/api/workflows", map[string]any{"name": name, "steps": steps}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var out struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &out)
	return out.Data.ID
}

func names(rows []stepRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

func TestIntegrationProjectWorkflowLibrary(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "WfLib")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@wflib.test", "pass", "project_manager")
	other := testutil.SeedTenant(t, q, "Other")
	// A real workflow that belongs to the other tenant: the id exists, so any
	// "accepted" outcome below means the tenant scope leaked.
	foreign, err := q.CreateWorkflow(ctx, db.CreateWorkflowParams{TenantID: other.ID, Name: "Theirs"})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "APE", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	aiRouterID := testutil.SeedRouterForEngine(t, q, tenant.ID, engine.ID)
	autoAPE := testutil.SeedStepDefinition(t, q, tenant.ID, "Auto APE", "ai", "", &aiRouterID)
	proofread := testutil.SeedStepDefinition(t, q, tenant.ID, "Proofread", "human", "", nil)

	t.Run("a new project gets Translation → Review linked to the library defaults", func(t *testing.T) {
		pid := createProjectWithBody(t, router, nil, user.ID, tenant.ID)
		s := projectSettings(t, router, pid, user.ID, tenant.ID)
		if got := names(s.Data.WorkflowSteps); len(got) != 2 || got[0] != "Translation" || got[1] != "Review" {
			t.Fatalf("expected default Translation → Review, got %v", got)
		}
		for _, st := range s.Data.WorkflowSteps {
			if st.StepDefinitionID == nil || st.StepType != "human" {
				t.Errorf("default step must link to a human library definition, got %+v", st)
			}
		}
		if s.Data.Project.WorkflowID != nil {
			t.Errorf("an inline default workflow has no library provenance, got %v", *s.Data.Project.WorkflowID)
		}
	})

	aiFirst := createLibraryWorkflow(t, router, "AI-first", []int64{autoAPE.ID, proofread.ID}, user.ID, tenant.ID)

	t.Run("create with workflow_id copies the library steps and stamps provenance", func(t *testing.T) {
		pid := createProjectWithBody(t, router, map[string]any{"workflow_id": aiFirst}, user.ID, tenant.ID)
		s := projectSettings(t, router, pid, user.ID, tenant.ID)
		if got := names(s.Data.WorkflowSteps); len(got) != 2 || got[0] != "Auto APE" || got[1] != "Proofread" {
			t.Fatalf("expected the workflow's steps, got %v", got)
		}
		if s.Data.WorkflowSteps[0].StepType != "ai" || *s.Data.WorkflowSteps[0].StepDefinitionID != autoAPE.ID {
			t.Errorf("step identity not snapshotted from the definition: %+v", s.Data.WorkflowSteps[0])
		}
		if s.Data.Project.WorkflowID == nil || *s.Data.Project.WorkflowID != aiFirst || s.Data.Project.WorkflowName == nil || *s.Data.Project.WorkflowName != "AI-first" {
			t.Errorf("expected provenance AI-first, got %v / %v", s.Data.Project.WorkflowID, s.Data.Project.WorkflowName)
		}
	})

	t.Run("an unknown or foreign workflow_id fails before the project exists", func(t *testing.T) {
		before, _ := q.ListProjects(ctx, tenant.ID)
		for _, id := range []int64{999999, foreign.ID} {
			rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
				"name": "Bad", "source_language": "en", "target_languages": []string{"de"}, "workflow_id": id,
			}, user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
		}
		after, _ := q.ListProjects(ctx, tenant.ID)
		if len(after) != len(before) {
			t.Fatalf("a rejected workflow_id must not leave a project behind: %d → %d", len(before), len(after))
		}
	})

	t.Run("apply replaces the steps, stamps provenance, and refuses over in-flight jobs", func(t *testing.T) {
		pid := createProjectWithBody(t, router, nil, user.ID, tenant.ID)
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/apply", pid),
			map[string]any{"workflow_id": aiFirst}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		s := projectSettings(t, router, pid, user.ID, tenant.ID)
		if got := names(s.Data.WorkflowSteps); len(got) != 2 || got[0] != "Auto APE" {
			t.Fatalf("apply did not replace the steps: %v", got)
		}
		if s.Data.Project.WorkflowID == nil || *s.Data.Project.WorkflowID != aiFirst {
			t.Errorf("apply must stamp provenance, got %v", s.Data.Project.WorkflowID)
		}
		events, _ := q.ListProjectEvents(ctx, db.ListProjectEventsParams{ProjectID: pid, TenantID: tenant.ID, Limit: 20})
		found := false
		for _, e := range events {
			found = found || e.EventType == "project.workflow_applied"
		}
		if !found {
			t.Error("expected a project.workflow_applied event")
		}

		// A job that has reached step 1 pins that step: a workflow whose first
		// step differs is refused, re-applying one that keeps it is fine.
		file := testutil.SeedFile(t, q, pid, "f.txt", db.FileFormatTxt)
		job := testutil.SeedJob(t, q, file.ID, "de")
		if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID: job.ID, WorkflowStepID: s.Data.WorkflowSteps[0].ID, Status: "in_progress",
		}); err != nil {
			t.Fatal(err)
		}
		humanFirst := createLibraryWorkflow(t, router, "Human-first", []int64{proofread.ID, autoAPE.ID}, user.ID, tenant.ID)
		rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/apply", pid),
			map[string]any{"workflow_id": humanFirst}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusConflict)
		rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/apply", pid),
			map[string]any{"workflow_id": aiFirst}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)

		// Unknown / foreign workflow → 400.
		pid2 := createProjectWithBody(t, router, nil, user.ID, tenant.ID)
		rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/apply", pid2),
			map[string]any{"workflow_id": 999999}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	})

	t.Run("detach clears the provenance pointer and keeps the copied steps", func(t *testing.T) {
		pid := createProjectWithBody(t, router, map[string]any{
			"name": "Detach", "source_language": "en", "target_languages": []string{"de"}, "workflow_id": aiFirst,
		}, user.ID, tenant.ID)
		before := projectSettings(t, router, pid, user.ID, tenant.ID)
		if before.Data.Project.WorkflowID == nil {
			t.Fatal("precondition: project should be based on the workflow")
		}
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/detach", pid), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusNoContent)
		after := projectSettings(t, router, pid, user.ID, tenant.ID)
		if after.Data.Project.WorkflowID != nil {
			t.Errorf("detach must clear workflow_id, got %v", *after.Data.Project.WorkflowID)
		}
		if got, want := names(after.Data.WorkflowSteps), names(before.Data.WorkflowSteps); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("detach must leave the steps alone: before %v after %v", want, got)
		}
		// Idempotent.
		rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/detach", pid), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusNoContent)
		// Cross-tenant → 404.
		outsider := testutil.SeedUserWithRole(t, q, other.ID, "out-detach@other.test", "pass", "admin")
		rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/detach", pid), nil, outsider.ID, other.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("cross-tenant: outsider gets 404 on every workflow route, a foreign workflow id is 400", func(t *testing.T) {
		outsider := testutil.SeedUserWithRole(t, q, other.ID, "out-routes@other.test", "pass", "admin")
		pid := createProjectWithBody(t, router, map[string]any{"workflow_id": aiFirst}, user.ID, tenant.ID)
		rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{"name": "Scoped", "workflow_id": aiFirst}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var tmpl struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &tmpl)
		templateSteps := func() []string {
			rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d/workflow-steps", tmpl.Data.ID), nil, user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusOK)
			var out struct{ Data []stepRow }
			testutil.DecodeJSON(t, rec, &out)
			return names(out.Data)
		}
		projectBefore := projectSettings(t, router, pid, user.ID, tenant.ID)
		templateBefore := templateSteps()
		workflowsBefore, err := q.ListWorkflows(ctx, tenant.ID)
		if err != nil {
			t.Fatal(err)
		}

		// The outsider's own tenant has a workflow, so the body is plausible;
		// only the project/template lookup can refuse it.
		outsiderCases := []struct {
			name, path string
			body       map[string]any
		}{
			{"project apply", fmt.Sprintf("/api/projects/%d/workflow/apply", pid), map[string]any{"workflow_id": foreign.ID}},
			{"project save-as", fmt.Sprintf("/api/projects/%d/workflow/save-as", pid), map[string]any{"name": "Stolen"}},
			{"template apply", fmt.Sprintf("/api/templates/%d/workflow/apply", tmpl.Data.ID), map[string]any{"workflow_id": foreign.ID}},
			{"template save-as", fmt.Sprintf("/api/templates/%d/workflow/save-as", tmpl.Data.ID), map[string]any{"name": "Stolen"}},
			{"template detach", fmt.Sprintf("/api/templates/%d/workflow/detach", tmpl.Data.ID), nil},
		}
		for _, c := range outsiderCases {
			rec := testutil.DoRequestWithRole(t, router, "POST", c.path, c.body, outsider.ID, other.ID, "admin")
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s as outsider: want 404, got %d %s", c.name, rec.Code, rec.Body.String())
			}
		}

		// A same-tenant PM applying the other tenant's workflow: 400, nothing changes.
		rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/apply", pid),
			map[string]any{"workflow_id": foreign.ID}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/templates/%d/workflow/apply", tmpl.Data.ID),
			map[string]any{"workflow_id": foreign.ID}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)

		projectAfter := projectSettings(t, router, pid, user.ID, tenant.ID)
		if got, want := names(projectAfter.Data.WorkflowSteps), names(projectBefore.Data.WorkflowSteps); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("project steps must be untouched: before %v after %v", want, got)
		}
		if projectAfter.Data.Project.WorkflowID == nil || *projectAfter.Data.Project.WorkflowID != aiFirst {
			t.Errorf("project provenance must still be %d, got %v", aiFirst, projectAfter.Data.Project.WorkflowID)
		}
		if got := templateSteps(); fmt.Sprint(got) != fmt.Sprint(templateBefore) {
			t.Errorf("template steps must be untouched: before %v after %v", templateBefore, got)
		}
		tmplRow, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: tmpl.Data.ID, TenantID: tenant.ID})
		if err != nil {
			t.Fatal(err)
		}
		profile, err := q.GetSettingsProfile(ctx, tmplRow.SettingsProfileID)
		if err != nil {
			t.Fatal(err)
		}
		if profile.WorkflowID == nil || *profile.WorkflowID != aiFirst {
			t.Errorf("template provenance must survive the outsider's detach, got %v", profile.WorkflowID)
		}
		workflowsAfter, err := q.ListWorkflows(ctx, tenant.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(workflowsAfter) != len(workflowsBefore) {
			t.Errorf("outsider save-as must not create a workflow in this tenant: %d → %d", len(workflowsBefore), len(workflowsAfter))
		}
		if n := testutil.CountWorkflowsNamed(t, q, other.ID, "Stolen"); n != 0 {
			t.Errorf("outsider save-as must not copy our steps into their library, got %d workflow(s)", n)
		}
	})

	t.Run("save-as promotes the project's steps into the library", func(t *testing.T) {
		pid := createProjectWithBody(t, router, nil, user.ID, tenant.ID)
		// Tweak the inline workflow first: Translation → Proofread.
		s := projectSettings(t, router, pid, user.ID, tenant.ID)
		rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", pid), map[string]any{
			"steps": []map[string]any{
				{"step_definition_id": *s.Data.WorkflowSteps[0].StepDefinitionID, "default_assignee": user.ID},
				{"step_definition_id": proofread.ID},
			},
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)

		rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/save-as", pid),
			map[string]any{"name": "From project"}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var created struct {
			Data struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &created)
		steps, err := q.ListWorkflowDefinitionSteps(ctx, db.ListWorkflowDefinitionStepsParams{WorkflowID: created.Data.ID, TenantID: tenant.ID})
		if err != nil || len(steps) != 2 || steps[0].Name != "Translation" || steps[1].Name != "Proofread" {
			t.Fatalf("library workflow should mirror the project steps, got %+v (err=%v)", steps, err)
		}
		if steps[0].DefaultAssignee == nil || *steps[0].DefaultAssignee != user.ID {
			t.Errorf("assignee should travel into the library, got %v", steps[0].DefaultAssignee)
		}
		s = projectSettings(t, router, pid, user.ID, tenant.ID)
		if s.Data.Project.WorkflowID == nil || *s.Data.Project.WorkflowID != created.Data.ID {
			t.Errorf("save-as must point the project at the new workflow, got %v", s.Data.Project.WorkflowID)
		}
		// Duplicate name → 409.
		rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/save-as", pid),
			map[string]any{"name": "from PROJECT"}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusConflict)
	})

	t.Run("save-as refuses a step without library provenance", func(t *testing.T) {
		pid := createProjectWithBody(t, router, nil, user.ID, tenant.ID)
		project, _ := q.GetProject(ctx, db.GetProjectParams{ID: pid, TenantID: tenant.ID})
		if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: project.SettingsProfileID, Name: "Legacy custom", StepOrder: 5, RoleRequired: "linguist",
		}); err != nil {
			t.Fatal(err)
		}
		before, err := q.ListWorkflows(ctx, tenant.ID)
		if err != nil {
			t.Fatal(err)
		}
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/save-as", pid),
			map[string]any{"name": "Nope"}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		// The workflow row is created before its steps are validated, so the
		// 400 must roll it back: same count, and no "Nope" in the library.
		after, err := q.ListWorkflows(ctx, tenant.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(after) != len(before) {
			t.Fatalf("a refused save-as must not leave a workflow behind: %d → %d", len(before), len(after))
		}
		if n := testutil.CountWorkflowsNamed(t, q, tenant.ID, "Nope"); n != 0 {
			t.Fatalf("refused save-as left %d workflow(s) named Nope in the library", n)
		}
	})

	t.Run("a legacy step survives a re-save through existing_step_id", func(t *testing.T) {
		pid := createProjectWithBody(t, router, nil, user.ID, tenant.ID)
		project, _ := q.GetProject(ctx, db.GetProjectParams{ID: pid, TenantID: tenant.ID})
		legacy, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: project.SettingsProfileID, Name: "LQA", StepOrder: 2, RoleRequired: "admin", AiRouterID: &aiRouterID,
		})
		if err != nil {
			t.Fatal(err)
		}
		rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", pid), map[string]any{
			"steps": []map[string]any{{"existing_step_id": legacy.ID}, {"step_definition_id": proofread.ID}},
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		s := projectSettings(t, router, pid, user.ID, tenant.ID)
		if got := s.Data.WorkflowSteps; len(got) != 2 || got[0].Name != "LQA" || got[0].StepType != "hybrid" || got[1].Name != "Proofread" {
			t.Fatalf("legacy identity must be carried forward: %+v", got)
		}
		// Another project's real step row is rejected, in this tenant or another,
		// and this project's steps are left as they are.
		sameTenant := testutil.SeedProject(t, q, tenant.ID, "Sibling", "en", []string{"de"})
		otherTenant := testutil.SeedProject(t, q, other.ID, "Theirs", "en", []string{"de"})
		for _, owner := range []db.Project{sameTenant, otherTenant} {
			foreignStep, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
				SettingsProfileID: owner.SettingsProfileID, Name: "Foreign", StepOrder: 1, RoleRequired: "linguist",
			})
			if err != nil {
				t.Fatal(err)
			}
			rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", pid), map[string]any{
				"steps": []map[string]any{{"existing_step_id": foreignStep.ID}},
			}, user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
			after := projectSettings(t, router, pid, user.ID, tenant.ID).Data.WorkflowSteps
			if len(after) != len(s.Data.WorkflowSteps) {
				t.Fatalf("%s's step %d: a rejected PUT changed the steps: %v → %v", owner.Name, foreignStep.ID, names(s.Data.WorkflowSteps), names(after))
			}
			for i := range after {
				if after[i].ID != s.Data.WorkflowSteps[i].ID || after[i].Name != s.Data.WorkflowSteps[i].Name {
					t.Fatalf("%s's step %d: a rejected PUT changed the steps: %+v → %+v", owner.Name, foreignStep.ID, s.Data.WorkflowSteps, after)
				}
			}
		}
	})

	t.Run("provenance travels through a template made from the form and create-from-template", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", "/api/templates",
			map[string]any{"name": "AI template", "workflow_id": aiFirst}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var tmpl struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &tmpl)
		rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d", tmpl.Data.ID), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var tget struct {
			Data struct {
				Template struct {
					WorkflowID   *int64  `json:"workflow_id"`
					WorkflowName *string `json:"workflow_name"`
				} `json:"template"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &tget)
		if tget.Data.Template.WorkflowID == nil || *tget.Data.Template.WorkflowID != aiFirst {
			t.Fatalf("template should inherit the project's provenance, got %v", tget.Data.Template.WorkflowID)
		}
		pid2 := createProjectWithBody(t, router, map[string]any{"template_id": tmpl.Data.ID}, user.ID, tenant.ID)
		s := projectSettings(t, router, pid2, user.ID, tenant.ID)
		if s.Data.Project.WorkflowID == nil || *s.Data.Project.WorkflowID != aiFirst || names(s.Data.WorkflowSteps)[0] != "Auto APE" {
			t.Errorf("project from template should carry provenance + steps, got %v / %v", s.Data.Project.WorkflowID, names(s.Data.WorkflowSteps))
		}
	})

	t.Run("workflow_id overrides the template's steps at create", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{"name": "Plain"}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var tmpl struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &tmpl)
		pid := createProjectWithBody(t, router, map[string]any{"template_id": tmpl.Data.ID, "workflow_id": aiFirst}, user.ID, tenant.ID)
		s := projectSettings(t, router, pid, user.ID, tenant.ID)
		if got := names(s.Data.WorkflowSteps); got[0] != "Auto APE" {
			t.Errorf("workflow should win over the template's default steps, got %v", got)
		}
	})
}

func TestIntegrationTemplateWorkflowLibrary(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "TplLib")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@tpl.test", "pass", "project_manager")
	proofread := testutil.SeedStepDefinition(t, q, tenant.ID, "Proofread", "human", "", nil)
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{"name": "Default"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmpl struct {
		Data struct {
			ID         int64  `json:"id"`
			WorkflowID *int64 `json:"workflow_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmpl)
	if tmpl.Data.WorkflowID != nil {
		t.Errorf("a fresh template has no provenance, got %v", *tmpl.Data.WorkflowID)
	}

	// A fresh template gets the default pair.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d/workflow-steps", tmpl.Data.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var steps struct{ Data []stepRow }
	testutil.DecodeJSON(t, rec, &steps)
	if got := names(steps.Data); len(got) != 2 || got[0] != "Translation" || got[1] != "Review" {
		t.Fatalf("expected default steps on a new template, got %v", got)
	}

	// Save the template's steps as a workflow, then create a template FROM it.
	wfID := createLibraryWorkflow(t, router, "Three-stage", []int64{*steps.Data[0].StepDefinitionID, *steps.Data[1].StepDefinitionID, proofread.ID}, user.ID, tenant.ID)
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{"name": "Staged", "workflow_id": wfID}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	testutil.DecodeJSON(t, rec, &tmpl)
	if tmpl.Data.WorkflowID == nil || *tmpl.Data.WorkflowID != wfID {
		t.Fatalf("template should be based on the workflow, got %v", tmpl.Data.WorkflowID)
	}
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d/workflow-steps", tmpl.Data.ID), nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &steps)
	if got := names(steps.Data); len(got) != 3 || got[2] != "Proofread" {
		t.Fatalf("template should carry the workflow's 3 steps, got %v", got)
	}

	// Apply another workflow, then save-as.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/templates/%d/workflow/apply", tmpl.Data.ID),
		map[string]any{"workflow_id": 999999}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", tmpl.Data.ID), map[string]any{
		"steps": []map[string]any{{"step_definition_id": proofread.ID}},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/templates/%d/workflow/save-as", tmpl.Data.ID),
		map[string]any{"name": "Proofread only"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var saved struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &saved)
	rows, err := q.ListWorkflowDefinitionSteps(ctx, db.ListWorkflowDefinitionStepsParams{WorkflowID: saved.Data.ID, TenantID: tenant.ID})
	if err != nil || len(rows) != 1 || rows[0].Name != "Proofread" {
		t.Fatalf("saved workflow should have the one Proofread step, got %+v (err=%v)", rows, err)
	}
	// An unknown workflow on template create is rejected before any row exists.
	before, _ := q.ListTemplates(ctx, tenant.ID)
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{"name": "Bad", "workflow_id": 999999}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	after, _ := q.ListTemplates(ctx, tenant.ID)
	if len(after) != len(before) {
		t.Errorf("rejected create must not leave a template: %d → %d", len(before), len(after))
	}
}
