package mcp_test

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// cfgCreateReviewEngine builds a qe_ape engine through the tools and returns its id.
func cfgCreateReviewEngine(t *testing.T, s *sdk.ClientSession, tokenID int64, name string) int64 {
	t.Helper()
	var prompt struct {
		PromptID int64 `json:"prompt_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_ai_prompt", map[string]any{
		"name": name + " prompt", "mode": "qe_ape", "text": "Review the translation.",
	}, &prompt); isErr {
		t.Fatalf("create_ai_prompt: %s", text)
	}
	var engine struct {
		AIEngineID int64 `json:"ai_engine_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_ai_engine", map[string]any{
		"name": name, "purpose": "qe_ape", "token_id": tokenID,
		"model": firstModel(t, s), "system_prompt_id": prompt.PromptID,
	}, &engine); isErr {
		t.Fatalf("create_ai_engine: %s", text)
	}
	return engine.AIEngineID
}

type cfgAIRouterOut struct {
	AIRouterID        int64  `json:"ai_router_id"`
	Name              string `json:"name"`
	DefaultAIEngineID int64  `json:"default_ai_engine_id"`
	Rules             []struct {
		TargetLanguage string `json:"target_language"`
		AIEngineID     int64  `json:"ai_engine_id"`
		EngineName     string `json:"engine_name"`
	} `json:"rules"`
	StepsUsing []string `json:"steps_using"`
}

func TestIntegrationMCPConfigAIRoutersAndEngines(t *testing.T) {
	c := newConfigFixture(t)
	ctx := context.Background()
	tokenID := seedVendorToken(t, c.fixture, "claude")
	engA := cfgCreateReviewEngine(t, c.asOwner, tokenID, "Cfg review A")
	engB := cfgCreateReviewEngine(t, c.asOwner, tokenID, "Cfg review B")
	router := testutil.SeedAIRouter(t, c.q, c.tenantA.ID, "Cfg AI router", &engA)
	testutil.SeedStepDefinition(t, c.q, c.tenantA.ID, "Review", "hybrid", "linguist", &router.ID)

	c.refresh(t)
	// Managers read AI routers; only owner/admin change them.
	var got cfgAIRouterOut
	if isErr, text := callTool(t, c.asManager, "openloc_get_ai_router", map[string]any{"ai_router_id": router.ID}, &got); isErr {
		t.Fatalf("get_ai_router: %s", text)
	}
	if got.DefaultAIEngineID != engA || len(got.StepsUsing) != 1 || got.StepsUsing[0] != "Review" {
		t.Errorf("get_ai_router = %+v", got)
	}
	update := map[string]any{
		"ai_router_id": router.ID, "name": "Cfg AI router v2", "default_ai_engine_id": engA,
		"rules": []map[string]any{{"target_language": "de", "ai_engine_id": engB}},
	}
	cfgMustRefuse(t, c.asManager, "openloc_update_ai_router", update, "owner or admin")
	cfgMustRefuse(t, c.asManager, "openloc_delete_ai_router", map[string]any{"ai_router_id": router.ID}, "owner or admin")
	cfgMustRefuse(t, c.asManager, "openloc_update_ai_engine", map[string]any{"ai_engine_id": engA, "name": "x"}, "owner or admin")
	cfgMustRefuse(t, c.asManager, "openloc_delete_ai_engine", map[string]any{"ai_engine_id": engB}, "owner or admin")

	g := cfgPreviewOnly(t, c.asOwner, "openloc_update_ai_router", update)
	if !strings.Contains(g.Preview.Summary, "Cfg AI router") || !cfgEffectsContain(g, "1 workflow step") {
		t.Errorf("router preview should name the router and its step: %+v", g.Preview)
	}
	if rules, _ := c.q.ListAIRouterRules(ctx, router.ID); len(rules) != 0 {
		t.Fatalf("the preview wrote rules: %+v", rules)
	}
	if isErr, text := confirmedCall(t, c.asOwner, "openloc_update_ai_router", update, &got); isErr {
		t.Fatalf("update_ai_router: %s", text)
	}
	if got.Name != "Cfg AI router v2" || len(got.Rules) != 1 || got.Rules[0].AIEngineID != engB || got.Rules[0].EngineName != "Cfg review B" {
		t.Errorf("after update = %+v", got)
	}
	if rules, _ := c.q.ListAIRouterRules(ctx, router.ID); len(rules) != 1 || rules[0].TargetLang != "de" {
		t.Errorf("stored rules = %+v", rules)
	}

	c.refresh(t)
	// Engine B is now routed to: the delete is refused, the update names the router.
	cfgMustRefuse(t, c.asOwner, "openloc_delete_ai_engine", map[string]any{"ai_engine_id": engB}, "router(s) still use")
	g = cfgPreviewOnly(t, c.asOwner, "openloc_update_ai_engine", map[string]any{"ai_engine_id": engB, "name": "Cfg review B2", "enabled": false})
	if !cfgEffectsContain(g, "1 router") {
		t.Errorf("engine preview should count its router: %+v", g.Preview)
	}
	if e, _ := c.q.GetAIEngine(ctx, db.GetAIEngineParams{ID: engB, TenantID: c.tenantA.ID}); e.Name != "Cfg review B" || !e.Enabled {
		t.Fatalf("the preview changed the engine: %+v", e)
	}
	if isErr, text := confirmedCall(t, c.asOwner, "openloc_update_ai_engine", map[string]any{"ai_engine_id": engB, "name": "Cfg review B2", "enabled": false}, nil); isErr {
		t.Fatalf("update_ai_engine: %s", text)
	}
	e, _ := c.q.GetAIEngine(ctx, db.GetAIEngineParams{ID: engB, TenantID: c.tenantA.ID})
	rows, _ := c.q.ListAIEngineRows(ctx, engB)
	if e.Name != "Cfg review B2" || e.Enabled || len(rows) != 1 || rows[0].TokenID == nil || *rows[0].TokenID != tokenID || rows[0].SystemPromptID == nil {
		t.Errorf("engine after update = %+v rows = %+v; omitted fields must keep their value", e, rows)
	}

	// The step still points at the router, so it cannot be deleted.
	cfgMustRefuse(t, c.asOwner, "openloc_delete_ai_router", map[string]any{"ai_router_id": router.ID}, "workflow step(s) still use")

	c.refresh(t)
	// An unused router and engine delete softly.
	spare := testutil.SeedAIRouter(t, c.q, c.tenantA.ID, "Cfg spare router", &engA)
	// Tenant B's delete of the same unused router is not found and leaves it.
	cfgMustNotFind(t, c.asOwnerB, "openloc_delete_ai_router", map[string]any{"ai_router_id": spare.ID})
	if c.softDeleted(t, "ai_routers", spare.ID) {
		t.Fatal("tenant B's delete call removed tenant A's router")
	}
	if isErr, text := confirmedCall(t, c.asOwner, "openloc_delete_ai_router", map[string]any{"ai_router_id": spare.ID}, nil); isErr {
		t.Fatalf("delete_ai_router: %s", text)
	}
	if !c.softDeleted(t, "ai_routers", spare.ID) {
		t.Error("the router should remain with deleted_at set")
	}
	engC := cfgCreateReviewEngine(t, c.asOwner, tokenID, "Cfg review C")
	if isErr, text := confirmedCall(t, c.asOwner, "openloc_delete_ai_engine", map[string]any{"ai_engine_id": engC}, nil); isErr {
		t.Fatalf("delete_ai_engine: %s", text)
	}
	if !c.softDeleted(t, "ai_engines", engC) {
		t.Error("the engine should remain with deleted_at set")
	}
	if rows, _ := c.q.ListAIEngineRows(ctx, engC); len(rows) != 0 {
		t.Errorf("a deleted engine keeps no provider rows, got %d", len(rows))
	}

	c.refresh(t)
	cfgMustNotFind(t, c.asOwnerB, "openloc_get_ai_router", map[string]any{"ai_router_id": router.ID})
	cfgMustNotFind(t, c.asOwnerB, "openloc_update_ai_router", update)
	cfgMustNotFind(t, c.asOwnerB, "openloc_update_ai_engine", map[string]any{"ai_engine_id": engA, "name": "stolen"})
	cfgMustNotFind(t, c.asOwnerB, "openloc_delete_ai_engine", map[string]any{"ai_engine_id": engA})
}

func TestIntegrationMCPConfigLibrary(t *testing.T) {
	c := newConfigFixture(t)
	ctx := context.Background()
	review := testutil.SeedStepDefinition(t, c.q, c.tenantA.ID, "Cfg Review", "human", "linguist", nil)
	edit := testutil.SeedStepDefinition(t, c.q, c.tenantA.ID, "Cfg Edit", "human", "linguist", nil)

	var wf struct {
		WorkflowID int64 `json:"workflow_id"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_create_workflow", map[string]any{
		"name": "Cfg flow", "step_definition_ids": []int64{review.ID},
	}, &wf); isErr {
		t.Fatalf("create_workflow: %s", text)
	}
	// A template copies the workflow, which the previews report.
	if isErr, text := callTool(t, c.asManager, "openloc_create_template", map[string]any{
		"name": "Cfg flow template", "workflow_id": wf.WorkflowID,
	}, nil); isErr {
		t.Fatalf("create_template: %s", text)
	}

	var sd struct {
		Name           string   `json:"name"`
		WorkflowsUsing []string `json:"workflows_using"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_get_step_definition", map[string]any{"step_definition_id": review.ID}, &sd); isErr {
		t.Fatalf("get_step_definition: %s", text)
	}
	if len(sd.WorkflowsUsing) != 1 || sd.WorkflowsUsing[0] != "Cfg flow" {
		t.Errorf("get_step_definition = %+v", sd)
	}
	g := cfgPreviewOnly(t, c.asManager, "openloc_update_step_definition", map[string]any{
		"step_definition_id": review.ID, "name": "Cfg Proofread", "step_type": "human",
	})
	if !cfgEffectsContain(g, "Cfg flow") {
		t.Errorf("step preview should name the workflow: %+v", g.Preview)
	}
	if cur, _ := c.q.GetStepDefinition(ctx, db.GetStepDefinitionParams{ID: review.ID, TenantID: c.tenantA.ID}); cur.Name != "Cfg Review" {
		t.Fatalf("the preview renamed the step: %+v", cur)
	}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_step_definition", map[string]any{
		"step_definition_id": review.ID, "name": "Cfg Proofread", "step_type": "human",
	}, &sd); isErr || sd.Name != "Cfg Proofread" {
		t.Fatalf("update_step_definition: %s %+v", text, sd)
	}
	cfgMustRefuse(t, c.asManager, "openloc_update_step_definition", map[string]any{
		"step_definition_id": review.ID, "name": "Cfg AI", "step_type": "ai",
	}, "needs an AI router")
	cfgMustRefuse(t, c.asManager, "openloc_delete_step_definition", map[string]any{"step_definition_id": review.ID}, "used by 1 workflow")

	c.refresh(t)
	var detail struct {
		Steps []struct {
			StepDefinitionID  int64 `json:"step_definition_id"`
			DefaultAssigneeID int64 `json:"default_assignee_id"`
		} `json:"steps"`
		CopiedByTemplates int64 `json:"copied_by_templates"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_get_workflow", map[string]any{"workflow_id": wf.WorkflowID}, &detail); isErr {
		t.Fatalf("get_workflow: %s", text)
	}
	if detail.CopiedByTemplates != 1 || len(detail.Steps) != 1 {
		t.Errorf("get_workflow = %+v", detail)
	}
	update := map[string]any{
		"workflow_id": wf.WorkflowID, "name": "Cfg flow v2",
		"steps": []map[string]any{{"step_definition_id": edit.ID, "default_assignee_id": c.linguistA.ID}, {"step_definition_id": review.ID}},
	}
	g = cfgPreviewOnly(t, c.asManager, "openloc_update_workflow", update)
	if !cfgEffectsContain(g, "1 template") {
		t.Errorf("workflow preview should report the template copy: %+v", g.Preview)
	}
	detail.CopiedByTemplates = 0 // the update's answer must carry the count itself
	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_workflow", update, &detail); isErr {
		t.Fatalf("update_workflow: %s", text)
	}
	if detail.CopiedByTemplates != 1 || len(detail.Steps) != 2 || detail.Steps[0].StepDefinitionID != edit.ID || detail.Steps[0].DefaultAssigneeID != c.linguistA.ID {
		t.Errorf("after update = %+v", detail)
	}
	cfgMustRefuse(t, c.asManager, "openloc_update_workflow", map[string]any{
		"workflow_id": wf.WorkflowID, "name": "Cfg flow v3",
		"steps": []map[string]any{{"step_definition_id": review.ID, "default_assignee_id": c.ownerB.ID}},
	}, "not a member")

	c.refresh(t)
	// Tenant B can neither read the live workflow nor reach its update or
	// delete preview; tenant A's own get, update and delete take the same id.
	// The update names tenant B's own step, so only the workflow's tenant check
	// can refuse it.
	stepB := testutil.SeedStepDefinition(t, c.q, c.tenantB.ID, "Cfg theirs", "human", "linguist", nil)
	cfgMustNotFind(t, c.asOwnerB, "openloc_get_workflow", map[string]any{"workflow_id": wf.WorkflowID})
	cfgMustNotFind(t, c.asOwnerB, "openloc_update_workflow", map[string]any{
		"workflow_id": wf.WorkflowID, "name": "Cfg stolen", "steps": []map[string]any{{"step_definition_id": stepB.ID}},
	})
	cfgMustNotFind(t, c.asOwnerB, "openloc_delete_workflow", map[string]any{"workflow_id": wf.WorkflowID})
	stored, err := c.q.GetWorkflow(ctx, db.GetWorkflowParams{ID: wf.WorkflowID, TenantID: c.tenantA.ID})
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	steps, err := c.q.ListWorkflowDefinitionSteps(ctx, db.ListWorkflowDefinitionStepsParams{WorkflowID: wf.WorkflowID, TenantID: c.tenantA.ID})
	if err != nil || stored.Name != "Cfg flow v2" || len(steps) != 2 {
		t.Fatalf("tenant B's update changed tenant A's workflow: %+v steps %+v (err %v)", stored, steps, err)
	}
	if c.softDeleted(t, "workflows", wf.WorkflowID) {
		t.Fatal("tenant B's delete call removed tenant A's workflow")
	}
	// Removing the step from the workflow frees it; both deletes are soft.
	if isErr, text := confirmedCall(t, c.asManager, "openloc_delete_workflow", map[string]any{"workflow_id": wf.WorkflowID}, nil); isErr {
		t.Fatalf("delete_workflow: %s", text)
	}
	if !c.softDeleted(t, "workflows", wf.WorkflowID) {
		t.Error("the workflow should remain with deleted_at set")
	}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_delete_step_definition", map[string]any{"step_definition_id": review.ID}, nil); isErr {
		t.Fatalf("delete_step_definition: %s", text)
	}
	if !c.softDeleted(t, "step_definitions", review.ID) {
		t.Error("the step should remain with deleted_at set")
	}
	cfgMustNotFind(t, c.asOwnerB, "openloc_get_step_definition", map[string]any{"step_definition_id": edit.ID})
	cfgMustNotFind(t, c.asOwnerB, "openloc_update_step_definition", map[string]any{"step_definition_id": edit.ID, "name": "x", "step_type": "human"})
	cfgMustNotFind(t, c.asOwnerB, "openloc_delete_step_definition", map[string]any{"step_definition_id": edit.ID})
	cfgMustRefuse(t, c.asReadOnly, "openloc_update_workflow", update, "write access")
}

func TestIntegrationMCPConfigPreviewAIPrompt(t *testing.T) {
	c := newConfigFixture(t)
	var draft struct {
		Mode         string         `json:"mode"`
		Instruction  string         `json:"instruction"`
		SystemPrompt string         `json:"system_prompt"`
		OutputSchema map[string]any `json:"output_schema"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_preview_ai_prompt", map[string]any{
		"mode": "mt", "text": "Translate like a lawyer.", "target_language": "de",
	}, &draft); isErr {
		t.Fatalf("preview draft: %s", text)
	}
	if draft.Mode != "mt" || draft.Instruction != "Translate like a lawyer." ||
		!strings.HasPrefix(draft.SystemPrompt, "Translate like a lawyer.") || len(draft.SystemPrompt) <= len(draft.Instruction) {
		t.Errorf("draft preview = %+v; the platform suffix must be appended", draft)
	}

	var prompt struct {
		PromptID int64 `json:"prompt_id"`
	}
	if isErr, text := callTool(t, c.asOwner, "openloc_create_ai_prompt", map[string]any{
		"name": "Cfg stored", "mode": "qe_ape", "text": "Check the translation.",
	}, &prompt); isErr {
		t.Fatalf("create_ai_prompt: %s", text)
	}
	if isErr, text := callTool(t, c.asManager, "openloc_preview_ai_prompt", map[string]any{"prompt_id": prompt.PromptID}, &draft); isErr {
		t.Fatalf("preview stored: %s", text)
	}
	if draft.Mode != "qe_ape" || draft.Instruction != "Check the translation." {
		t.Errorf("stored preview = %+v", draft)
	}
	cfgMustRefuse(t, c.asManager, "openloc_preview_ai_prompt", map[string]any{"mode": "poetry"}, "prompt_id")
	cfgMustNotFind(t, c.asOwnerB, "openloc_preview_ai_prompt", map[string]any{"prompt_id": prompt.PromptID})
}
