package project_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Workflow-step copies (template from the form, apply-template) must preserve
// ai_router_id — an AI review step that silently loses its AI router is a
// broken step. Deadline is deliberately NOT copied (per-project).
func TestIntegrationTemplateCopyPreservesAIEngineID(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "CopyCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "copy@test.com", "pass")

	aiSys, err := q.CreateAIEngine(context.Background(), db.CreateAIEngineParams{
		TenantID: tenant.ID, Name: "QE reviewer", Purpose: "qe_ape",
	})
	if err != nil {
		t.Fatalf("seed AI engine: %v", err)
	}

	aiRouter := testutil.SeedRouterForEngine(t, q, tenant.ID, aiSys.ID)

	// A template saved from the form with a workflow that includes an AI step.
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "AI Copy Template",
		"workflow_steps": []map[string]any{
			testutil.LibraryStep(t, q, tenant.ID, "translation", map[string]any{"role_required": "linguist"}),
			testutil.LibraryStep(t, q, tenant.ID, "ai review", map[string]any{"role_required": "linguist", "ai_router_id": aiRouter}),
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)

	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d/workflow-steps", tmplResp.Data.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var stepsResp struct {
		Data []struct {
			Name       string `json:"name"`
			AiRouterID *int64 `json:"ai_router_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &stepsResp)

	if len(stepsResp.Data) != 2 {
		t.Fatalf("expected 2 workflow steps on template, got %d", len(stepsResp.Data))
	}
	var found bool
	for _, s := range stepsResp.Data {
		if s.Name == "ai review" {
			found = true
			if s.AiRouterID == nil || *s.AiRouterID != aiRouter {
				t.Errorf("ai_router_id dropped on save-as-template copy: got %v, want %d", s.AiRouterID, aiRouter)
			}
		}
	}
	if !found {
		t.Fatal("ai review step missing from template workflow steps")
	}

	// A project created from the template keeps the router on its copy.
	pid := createProjectWithBody(t, q, router, map[string]any{"template_id": tmplResp.Data.ID}, user.ID, tenant.ID)
	found = false
	for _, s := range projectSettings(t, router, pid, user.ID, tenant.ID).Data.WorkflowSteps {
		if s.Name == "ai review" {
			found = true
			if s.AiRouterID == nil || *s.AiRouterID != aiRouter {
				t.Errorf("ai_router_id dropped on create-from-template copy: got %s, want %d", testutil.Show(s.AiRouterID), aiRouter)
			}
		}
	}
	if !found {
		t.Fatal("ai review step missing from the project created from the template")
	}
}

// A project made from a template whose steps were edited after its library
// workflow was applied reads "edited" too: the mark travels with the steps.
func TestIntegrationTemplateCopyKeepsWorkflowEditedMark(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "EditedCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "edited@test.com", "pass")
	tr := testutil.SeedStepDefinition(t, q, tenant.ID, "Translate", "human", "", nil)
	rv := testutil.SeedStepDefinition(t, q, tenant.ID, "Check", "human", "", nil)
	pr := testutil.SeedStepDefinition(t, q, tenant.ID, "Polish", "human", "", nil)
	wf := createLibraryWorkflow(t, router, "Two steps", []int64{tr.ID, rv.ID}, user.ID, tenant.ID)

	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{"name": "Edited template"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	tmpl := testutil.DecodeData[struct {
		ID int64 `json:"id"`
	}](t, rec)
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/templates/%d/workflow/apply", tmpl.ID), map[string]any{"workflow_id": wf}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", tmpl.ID), map[string]any{"steps": []map[string]any{
		{"step_definition_id": tr.ID}, {"step_definition_id": pr.ID},
	}}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	pid := createProjectWithBody(t, q, router, map[string]any{"template_id": tmpl.ID}, user.ID, tenant.ID)
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", pid), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	got := testutil.DecodeData[struct {
		Project struct {
			WorkflowID     *int64 `json:"workflow_id"`
			WorkflowEdited bool   `json:"workflow_edited"`
		} `json:"project"`
	}](t, rec)
	if got.Project.WorkflowID == nil || *got.Project.WorkflowID != wf || !got.Project.WorkflowEdited {
		t.Errorf("want workflow %d marked edited, got %v edited=%v", wf, testutil.Show(got.Project.WorkflowID), got.Project.WorkflowEdited)
	}
}
