// AI configuration create tools called by tenant B with one of tenant A's ids
// where its own would go. Each call is refused as not found and creates
// nothing in tenant B; tenant A then makes the call with its own ids and it
// succeeds, so each refusal is the tenant check and not a malformed call.
package mcp_test

import (
	"strings"
	"testing"

	"openloc/internal/testutil"
)

// foreignCreate is one create call. args names one of tenant A's ids and is
// sent by tenant B; own is the same call with only tenant A's ids, sent by
// tenant A. want is the part of the refusal that names the id's kind, so a
// refusal for another reason does not pass. table holds what the tool creates.
type foreignCreate struct {
	name, tool, table, want string
	args, own               map[string]any
}

// mustRefuseForeignCreates runs each call on fresh sessions, so the per-token
// rate limit never decides an outcome. Tenant B's row count in the table must
// not move; tenant A's must grow by one, which shows the count sees a create.
func mustRefuseForeignCreates(t *testing.T, f *fixture, calls []foreignCreate) {
	t.Helper()
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			count := "SELECT count(*) FROM " + c.table + " WHERE tenant_id = $1"
			asB := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
			before := testutil.CountRows(t, f.pool, count, f.tenantB.ID)
			isErr, text := callTool(t, asB, c.tool, c.args, nil)
			if !isErr {
				t.Fatalf("%s %v: tenant B used tenant A's id", c.tool, c.args)
			}
			assertOpaqueNotFound(t, text)
			if !strings.Contains(text, c.want) {
				t.Errorf("refusal = %q, want it to name %q", text, c.want)
			}
			if after := testutil.CountRows(t, f.pool, count, f.tenantB.ID); after != before {
				t.Errorf("tenant B's %s went from %d to %d rows", c.table, before, after)
			}

			asA := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
			ownBefore := testutil.CountRows(t, f.pool, count, f.tenantA.ID)
			if isErr, text := callTool(t, asA, c.tool, c.own, nil); isErr {
				t.Fatalf("%s %v as tenant A: %s", c.tool, c.own, text)
			}
			if after := testutil.CountRows(t, f.pool, count, f.tenantA.ID); after != ownBefore+1 {
				t.Errorf("tenant A's %s went from %d to %d rows, want one more", c.table, ownBefore, after)
			}
		})
	}
}

func TestIntegrationMCPAIEngineAndRouterCreatesRejectOtherTenantIDs(t *testing.T) {
	f := newFixture(t)
	asA := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	asB := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	tokenA := seedVendorToken(t, f, "claude")
	tokenB := testutil.SeedAIVendorToken(t, f.q, f.tenantB.ID, "claude", "Tenant B claude", "placeholder").ID
	model := firstModel(t, asA)
	prompt := func(s sessionT, name string) int64 {
		t.Helper()
		var out struct {
			PromptID int64 `json:"prompt_id"`
		}
		if isErr, text := callTool(t, s, "openloc_create_ai_prompt", map[string]any{
			"name": name, "mode": "qe_ape", "text": "Review the translation.",
		}, &out); isErr {
			t.Fatalf("create_ai_prompt: %s", text)
		}
		return out.PromptID
	}
	promptA, promptB := prompt(asA, "Tenant A review"), prompt(asB, "Tenant B review")
	engine := func(name string, tokenID, promptID int64) map[string]any {
		return map[string]any{"name": name, "purpose": "qe_ape", "token_id": tokenID, "model": model, "system_prompt_id": promptID}
	}
	reviewA := cfgCreateReviewEngine(t, connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID)), tokenA, "Tenant A reviewer")
	mtEngineA := testutil.SeedMTEngine(t, f.q, f.tenantA.ID, "Tenant A MT", "mock").ID
	llmMTA := createMTEngine(t, connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID)), tokenA, "Tenant A LLM MT")

	named := func(name string, args map[string]any) map[string]any {
		args["name"] = name
		return args
	}
	deRule := func(field string, id int64) []map[string]any {
		return []map[string]any{{"target_language": "de", field: id}}
	}
	mustRefuseForeignCreates(t, f, []foreignCreate{
		{
			name: "create_ai_engine token_id", tool: "openloc_create_ai_engine", table: "ai_engines", want: "token not found",
			args: engine("Theirs token", tokenA, promptB), own: engine("Own token", tokenA, promptA),
		},
		{
			name: "create_ai_engine system_prompt_id", tool: "openloc_create_ai_engine", table: "ai_engines", want: "system prompt",
			args: engine("Theirs prompt", tokenB, promptA), own: engine("Own prompt", tokenA, promptA),
		},
		{
			name: "create_ai_router default_ai_engine_id", tool: "openloc_create_ai_router", table: "ai_routers", want: "AI engine",
			args: named("Theirs default", map[string]any{"default_ai_engine_id": reviewA}),
			own:  named("Own default", map[string]any{"default_ai_engine_id": reviewA}),
		},
		{
			name: "create_ai_router rule ai_engine_id", tool: "openloc_create_ai_router", table: "ai_routers", want: "AI engine",
			args: named("Theirs rule", map[string]any{"rules": deRule("ai_engine_id", reviewA)}),
			own:  named("Own rule", map[string]any{"rules": deRule("ai_engine_id", reviewA)}),
		},
		{
			name: "create_mt_router default_mt_engine_id", tool: "openloc_create_mt_router", table: "mt_routers", want: "MT engine",
			args: named("Theirs MT default", map[string]any{"default_mt_engine_id": mtEngineA}),
			own:  named("Own MT default", map[string]any{"default_mt_engine_id": mtEngineA}),
		},
		{
			name: "create_mt_router default_ai_engine_id", tool: "openloc_create_mt_router", table: "mt_routers", want: "AI engine",
			args: named("Theirs LLM default", map[string]any{"default_ai_engine_id": llmMTA}),
			own:  named("Own LLM default", map[string]any{"default_ai_engine_id": llmMTA}),
		},
		{
			name: "create_mt_router rule mt_engine_id", tool: "openloc_create_mt_router", table: "mt_routers", want: "MT engine",
			args: named("Theirs MT rule", map[string]any{"rules": deRule("mt_engine_id", mtEngineA)}),
			own:  named("Own MT rule", map[string]any{"rules": deRule("mt_engine_id", mtEngineA)}),
		},
		{
			name: "create_mt_router rule ai_engine_id", tool: "openloc_create_mt_router", table: "mt_routers", want: "AI engine",
			args: named("Theirs LLM rule", map[string]any{"rules": deRule("ai_engine_id", llmMTA)}),
			own:  named("Own LLM rule", map[string]any{"rules": deRule("ai_engine_id", llmMTA)}),
		},
	})
}

func TestIntegrationMCPStepAndWorkflowCreatesRejectOtherTenantIDs(t *testing.T) {
	f := newFixture(t)
	routerA := testutil.SeedAIRouter(t, f.q, f.tenantA.ID, "Tenant A review router", nil)
	stepA := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Tenant A proofread", "human", "linguist", nil)
	stepA2 := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Tenant A edit", "human", "linguist", nil)
	stepB := testutil.SeedStepDefinition(t, f.q, f.tenantB.ID, "Tenant B proofread", "human", "linguist", nil)
	step := func(name string) map[string]any {
		return map[string]any{"name": name, "step_type": "hybrid", "ai_router_id": routerA.ID}
	}
	workflow := func(name string, steps ...int64) map[string]any {
		return map[string]any{"name": name, "step_definition_ids": steps}
	}

	mustRefuseForeignCreates(t, f, []foreignCreate{
		{
			name: "create_step_definition ai_router_id", tool: "openloc_create_step_definition", table: "step_definitions", want: "AI router",
			args: step("Theirs AI review"), own: step("Own AI review"),
		},
		{
			name: "create_workflow step_definition_ids", tool: "openloc_create_workflow", table: "workflows", want: "step 1: step definition",
			args: workflow("Theirs flow", stepA.ID), own: workflow("Own flow", stepA.ID),
		},
		{
			name: "create_workflow second step_definition_id", tool: "openloc_create_workflow", table: "workflows", want: "step 2: step definition",
			args: workflow("Theirs mixed flow", stepB.ID, stepA.ID), own: workflow("Own long flow", stepA2.ID, stepA.ID),
		},
	})
}
