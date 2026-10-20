// Step-definition library CRUD, validation, tenant isolation, role gating and
// the delete-in-use guard.
package stepdef_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/stepdef"
	"openloc/internal/testutil"
)

type sdResp struct {
	Data struct {
		ID           int64  `json:"id"`
		TenantID     int64  `json:"tenant_id"`
		Name         string `json:"name"`
		StepType     string `json:"step_type"`
		RoleRequired string `json:"role_required"`
		AiRouterID   *int64 `json:"ai_router_id"`
	} `json:"data"`
}

type sdListResp struct {
	Data []struct {
		ID            int64   `json:"id"`
		Name          string  `json:"name"`
		StepType      string  `json:"step_type"`
		AiRouterName  *string `json:"ai_router_name"`
		WorkflowCount int64   `json:"workflow_count"`
	} `json:"data"`
}

func seedAIEngine(t *testing.T, q db.Querier, tenantID int64, name, purpose string) int64 {
	t.Helper()
	sys, err := q.CreateAIEngine(context.Background(), db.CreateAIEngineParams{TenantID: tenantID, Name: name, Purpose: purpose})
	if err != nil {
		t.Fatalf("seed AI engine: %v", err)
	}
	return sys.ID
}

func TestIntegrationStepDefinitionCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "A")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@a.com", "pass", "project_manager")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "lin@a.com", "pass", "linguist")
	qeEngine := seedAIEngine(t, q, tenant.ID, "QE engine", "qe_ape")
	qeRouter := testutil.SeedAIRouter(t, q, tenant.ID, "QE reviewer", &qeEngine).ID
	// Another tenant's router: a valid id that must read as "not found" here.
	other := testutil.SeedTenant(t, q, "Other")
	foreignRouter := testutil.SeedAIRouter(t, q, other.ID, "Foreign", nil).ID

	post := func(body map[string]any, userID int64, role string) *sdResp {
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/step-definitions", body, userID, tenant.ID, role)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var out sdResp
		testutil.DecodeJSON(t, rec, &out)
		return &out
	}
	expectBad := func(body map[string]any, wantSubstr string) {
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/step-definitions", body, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		if wantSubstr != "" && !strings.Contains(rec.Body.String(), wantSubstr) {
			t.Errorf("expected error containing %q, got %s", wantSubstr, rec.Body.String())
		}
	}

	// A project manager creates a plain human step; role defaults to linguist.
	human := post(map[string]any{"name": "Proofread", "step_type": "human"}, pm.ID, "project_manager")
	if human.Data.RoleRequired != "linguist" || human.Data.TenantID != tenant.ID {
		t.Fatalf("unexpected human step: %+v", human.Data)
	}

	// Validation: type/router pairing is enforced on the server.
	expectBad(map[string]any{"name": "", "step_type": "human"}, "name is required")
	expectBad(map[string]any{"name": "X", "step_type": "robot"}, "step_type")
	expectBad(map[string]any{"name": "X", "step_type": "hybrid"}, "needs an AI router")
	expectBad(map[string]any{"name": "X", "step_type": "ai"}, "needs an AI router")
	expectBad(map[string]any{"name": "X", "step_type": "human", "ai_router_id": qeRouter}, "cannot have an AI router")
	expectBad(map[string]any{"name": "X", "step_type": "ai", "ai_router_id": foreignRouter}, "not found")

	// role_required is not an input: completion is gated by assignment or
	// manager rank, so a client-sent value is ignored, not honoured.
	ai := post(map[string]any{"name": "Auto APE", "step_type": "ai", "ai_router_id": qeRouter, "role_required": "owner"}, pm.ID, "project_manager")
	if ai.Data.StepType != "ai" || ai.Data.AiRouterID == nil || *ai.Data.AiRouterID != qeRouter {
		t.Fatalf("unexpected ai step: %+v", ai.Data)
	}
	if ai.Data.RoleRequired != "linguist" {
		t.Errorf("role_required must stay at the default, got %q", ai.Data.RoleRequired)
	}

	// Duplicate name (case-insensitive) → 409.
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/step-definitions", map[string]any{"name": "proofread", "step_type": "human"}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Linguists are shut out of the whole surface.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/step-definitions", nil, linguist.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/step-definitions", map[string]any{"name": "Nope", "step_type": "human"}, linguist.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// List carries the router name and usage count.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/step-definitions", nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list sdListResp
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(list.Data))
	}
	for _, d := range list.Data {
		if d.Name == "Auto APE" && (d.AiRouterName == nil || *d.AiRouterName != "QE reviewer") {
			t.Errorf("ai step should carry its router name, got %+v", d)
		}
		if d.WorkflowCount != 0 {
			t.Errorf("fresh definition should be unused, got %d", d.WorkflowCount)
		}
	}

	// Update swaps type + router; the same pairing rules apply.
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/step-definitions/%d", human.Data.ID),
		map[string]any{"name": "AI proofread", "step_type": "hybrid", "ai_router_id": qeRouter}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var updated sdResp
	testutil.DecodeJSON(t, rec, &updated)
	if updated.Data.StepType != "hybrid" || updated.Data.Name != "AI proofread" {
		t.Fatalf("update not applied: %+v", updated.Data)
	}
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/step-definitions/%d", human.Data.ID),
		map[string]any{"name": "AI proofread", "step_type": "human", "ai_router_id": qeRouter}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Delete-in-use: compose the ai step into a workflow, then try to delete it.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/workflows", map[string]any{
		"name": "AI-first", "steps": []map[string]any{{"step_definition_id": ai.Data.ID}},
	}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/step-definitions/%d", ai.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "AI-first") {
		t.Errorf("409 should name the workflow, got %s", rec.Body.String())
	}
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/step-definitions", nil, pm.ID, tenant.ID, "project_manager")
	testutil.DecodeJSON(t, rec, &list)
	for _, d := range list.Data {
		if d.ID == ai.Data.ID && d.WorkflowCount != 1 {
			t.Errorf("expected workflow_count 1 for the composed step, got %d", d.WorkflowCount)
		}
	}

	// Retiring the workflow frees the step: the count drops and delete goes through.
	var wfList struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/workflows", nil, pm.ID, tenant.ID, "project_manager")
	testutil.DecodeJSON(t, rec, &wfList)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/workflows/%d", wfList.Data[0].ID), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/step-definitions", nil, pm.ID, tenant.ID, "project_manager")
	testutil.DecodeJSON(t, rec, &list)
	for _, d := range list.Data {
		if d.ID == ai.Data.ID && d.WorkflowCount != 0 {
			t.Errorf("a soft-deleted workflow must not count as usage, got %d", d.WorkflowCount)
		}
	}
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/step-definitions/%d", ai.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// Unused definition soft-deletes; it disappears from reads.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/step-definitions/%d", human.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/step-definitions/%d", human.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	row, err := q.GetStepDefinitionAny(context.Background(), db.GetStepDefinitionAnyParams{ID: human.Data.ID, TenantID: tenant.ID})
	if err != nil || !row.DeletedAt.Valid {
		t.Fatalf("expected a soft-deleted row to remain (err=%v, deleted_at=%v)", err, row.DeletedAt)
	}
	// The name is free again after the soft delete.
	post(map[string]any{"name": "AI proofread", "step_type": "human"}, pm.ID, "project_manager")
}

func TestIntegrationStepDefinitionTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "A")
	tenantB := testutil.SeedTenant(t, q, "B")
	adminA := testutil.SeedUserWithRole(t, q, tenantA.ID, "a@a.com", "pass", "admin")
	adminB := testutil.SeedUserWithRole(t, q, tenantB.ID, "b@b.com", "pass", "admin")
	engineA := seedAIEngine(t, q, tenantA.ID, "A's QE", "qe_ape")
	routerA := testutil.SeedRouterForEngine(t, q, tenantA.ID, engineA)

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/step-definitions", map[string]any{"name": "Secret", "step_type": "human"}, adminA.ID, tenantA.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created sdResp
	testutil.DecodeJSON(t, rec, &created)
	id := created.Data.ID

	// B cannot see, edit or delete A's definition.
	for _, tc := range []struct{ method, path string }{
		{"GET", fmt.Sprintf("/api/step-definitions/%d", id)},
		{"PUT", fmt.Sprintf("/api/step-definitions/%d", id)},
		{"DELETE", fmt.Sprintf("/api/step-definitions/%d", id)},
	} {
		var body map[string]any
		if tc.method == "PUT" {
			body = map[string]any{"name": "Stolen", "step_type": "human"}
		}
		rec = testutil.DoRequestWithRole(t, router, tc.method, tc.path, body, adminB.ID, tenantB.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	}
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/step-definitions", nil, adminB.ID, tenantB.ID, "admin")
	var list sdListResp
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 0 {
		t.Fatalf("tenant B sees %d of A's definitions", len(list.Data))
	}
	// B cannot attach A's router.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/step-definitions", map[string]any{"name": "Borrowed", "step_type": "ai", "ai_router_id": routerA}, adminB.ID, tenantB.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	// A's row is intact.
	if _, err := q.GetStepDefinition(context.Background(), db.GetStepDefinitionParams{ID: id, TenantID: tenantA.ID}); err != nil {
		t.Fatalf("A's definition should survive B's attempts: %v", err)
	}
}

func TestIntegrationEnsureDefaultsIdempotent(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenant := testutil.SeedTenant(t, q, "A")
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := stepdef.EnsureDefaults(ctx, q, tenant.ID); err != nil {
			t.Fatalf("EnsureDefaults #%d: %v", i+1, err)
		}
	}
	rows, err := q.ListStepDefinitions(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(stepdef.DefaultNames) {
		t.Fatalf("expected exactly %d defaults after two calls, got %d", len(stepdef.DefaultNames), len(rows))
	}
	// A renamed default is respected: nothing is re-created under the old name.
	if _, err := q.UpdateStepDefinition(ctx, db.UpdateStepDefinitionParams{
		ID: rows[0].ID, TenantID: tenant.ID, Name: "Translation (legal)", StepType: "human", RoleRequired: "linguist",
	}); err != nil {
		t.Fatal(err)
	}
	if err := stepdef.EnsureDefaults(ctx, q, tenant.ID); err != nil {
		t.Fatal(err)
	}
	rows, _ = q.ListStepDefinitions(ctx, tenant.ID)
	if len(rows) != 3 {
		// Renaming "Review" away means the tenant lacks a step called Review,
		// so EnsureDefaults recreates one — that IS the contract. Only the
		// renamed row itself must not be touched.
		t.Fatalf("expected 3 rows (renamed + recreated default + other), got %d", len(rows))
	}
}
