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
	pid := createProjectWithBody(t, router, map[string]any{"template_id": tmplResp.Data.ID}, user.ID, tenant.ID)
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
