// Workflow library CRUD, validation, tenant isolation and role gating
package workflowlib_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type wfDetail struct {
	Data struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Steps []struct {
			StepOrder        int32   `json:"step_order"`
			StepDefinitionID int64   `json:"step_definition_id"`
			Name             string  `json:"name"`
			StepType         string  `json:"step_type"`
			DefaultAssignee  *int64  `json:"default_assignee"`
			AssigneeName     *string `json:"assignee_name"`
		} `json:"steps"`
	} `json:"data"`
}

type wfList struct {
	Data []struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Steps []struct {
			Name     string `json:"name"`
			StepType string `json:"step_type"`
		} `json:"steps"`
	} `json:"data"`
}

func seedDef(t *testing.T, q db.Querier, tenantID int64, name, stepType string, router *int64) int64 {
	t.Helper()
	sd, err := q.CreateStepDefinition(context.Background(), db.CreateStepDefinitionParams{
		TenantID: tenantID, Name: name, StepType: stepType, RoleRequired: "linguist", AiRouterID: router,
	})
	if err != nil {
		t.Fatalf("seed step definition %q: %v", name, err)
	}
	return sd.ID
}

func TestIntegrationWorkflowCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "A")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@a.com", "pass", "project_manager")
	reviewer := testutil.SeedUserWithRole(t, q, tenant.ID, "rev@a.com", "pass", "linguist")
	other := testutil.SeedTenant(t, q, "B")
	outsider := testutil.SeedUserWithRole(t, q, other.ID, "out@b.com", "pass", "admin")

	engine, err := q.CreateAIEngine(context.Background(), db.CreateAIEngineParams{TenantID: tenant.ID, Name: "APE", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	translation := seedDef(t, q, tenant.ID, "Translation", "human", nil)
	review := seedDef(t, q, tenant.ID, "Review", "human", nil)
	aiRouterID := testutil.SeedRouterForEngine(t, q, tenant.ID, engine.ID)
	autoAPE := seedDef(t, q, tenant.ID, "Auto APE", "ai", &aiRouterID)

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		return testutil.DoRequestWithRole(t, router, method, path, body, pm.ID, tenant.ID, "project_manager")
	}

	// Create: steps come back ordered with the definition's identity resolved
	// and the assignee's name joined in.
	rec := do("POST", "/api/workflows", map[string]any{
		"name": "Standard",
		"steps": []map[string]any{
			{"step_definition_id": autoAPE},
			{"step_definition_id": translation},
			{"step_definition_id": review, "default_assignee": reviewer.ID},
		},
	})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created wfDetail
	testutil.DecodeJSON(t, rec, &created)
	if len(created.Data.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(created.Data.Steps))
	}
	if created.Data.Steps[0].Name != "Auto APE" || created.Data.Steps[0].StepType != "ai" {
		t.Errorf("step 0 should be the ai step, got %+v", created.Data.Steps[0])
	}
	if s := created.Data.Steps[2]; s.DefaultAssignee == nil || *s.DefaultAssignee != reviewer.ID || s.AssigneeName == nil {
		t.Errorf("step 2 should carry the reviewer, got %+v", s)
	}
	wfID := created.Data.ID

	// Validation.
	for name, body := range map[string]map[string]any{
		"no steps":         {"name": "Empty", "steps": []map[string]any{}},
		"blank name":       {"name": "  ", "steps": []map[string]any{{"step_definition_id": translation}}},
		"unknown step":     {"name": "Ghost", "steps": []map[string]any{{"step_definition_id": 999999}}},
		"foreign assignee": {"name": "Foreign", "steps": []map[string]any{{"step_definition_id": translation, "default_assignee": outsider.ID}}},
		"too many steps":   {"name": "Long", "steps": repeatStep(translation, 10)},
	} {
		rec = do("POST", "/api/workflows", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
	rec = do("POST", "/api/workflows", map[string]any{"name": "standard", "steps": []map[string]any{{"step_definition_id": translation}}})
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// List: chips per workflow.
	rec = do("GET", "/api/workflows", nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list wfList
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 1 || len(list.Data[0].Steps) != 3 || list.Data[0].Steps[1].Name != "Translation" {
		t.Fatalf("unexpected list: %+v", list.Data)
	}

	// Update: rename + reorder + drop the ai step.
	rec = do("PUT", fmt.Sprintf("/api/workflows/%d", wfID), map[string]any{
		"name": "Standard v2",
		"steps": []map[string]any{
			{"step_definition_id": translation},
			{"step_definition_id": review},
		},
	})
	testutil.AssertStatus(t, rec, http.StatusOK)
	var updated wfDetail
	testutil.DecodeJSON(t, rec, &updated)
	if updated.Data.Name != "Standard v2" || len(updated.Data.Steps) != 2 || updated.Data.Steps[0].Name != "Translation" {
		t.Fatalf("update not applied: %+v", updated.Data)
	}
	// The dropped definition is untouched by the workflow edit.
	if _, err := q.GetStepDefinition(context.Background(), db.GetStepDefinitionParams{ID: autoAPE, TenantID: tenant.ID}); err != nil {
		t.Fatalf("removing a step from a workflow must not delete its definition: %v", err)
	}

	// Linguists are shut out.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/workflows", nil, reviewer.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Cross-tenant: 404 on read/write/delete, and B's list is empty.
	for _, tc := range []struct{ method, path string }{
		{"GET", fmt.Sprintf("/api/workflows/%d", wfID)},
		{"PUT", fmt.Sprintf("/api/workflows/%d", wfID)},
		{"DELETE", fmt.Sprintf("/api/workflows/%d", wfID)},
	} {
		var body map[string]any
		if tc.method == "PUT" {
			body = map[string]any{"name": "Stolen", "steps": []map[string]any{{"step_definition_id": translation}}}
		}
		rec = testutil.DoRequestWithRole(t, router, tc.method, tc.path, body, outsider.ID, other.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	}
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/workflows", nil, outsider.ID, other.ID, "admin")
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 0 {
		t.Fatalf("tenant B sees %d of A's workflows", len(list.Data))
	}
	// B cannot compose A's definitions into its own workflow.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/workflows", map[string]any{
		"name": "Borrowed", "steps": []map[string]any{{"step_definition_id": translation}},
	}, outsider.ID, other.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Soft delete: gone from reads, row remains, name is reusable.
	rec = do("DELETE", fmt.Sprintf("/api/workflows/%d", wfID), nil)
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = do("GET", fmt.Sprintf("/api/workflows/%d", wfID), nil)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	row, err := q.GetWorkflowAny(context.Background(), db.GetWorkflowAnyParams{ID: wfID, TenantID: tenant.ID})
	if err != nil || !row.DeletedAt.Valid {
		t.Fatalf("expected soft-deleted row (err=%v deleted_at=%v)", err, row.DeletedAt)
	}
	rec = do("POST", "/api/workflows", map[string]any{"name": "Standard v2", "steps": []map[string]any{{"step_definition_id": translation}}})
	testutil.AssertStatus(t, rec, http.StatusCreated)
}

func repeatStep(id int64, n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{"step_definition_id": id}
	}
	return out
}
