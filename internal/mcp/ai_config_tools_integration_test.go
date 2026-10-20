// AI-configuration tools over the real protocol: the full assembly path from a
// pair of prompts to a workflow, and the owner/admin gate that keeps MCP from
// widening what the REST routes allow.
package mcp_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/mtrouter"
	"openloc/internal/testutil"
)

// The whole point of the feature: an org can go from nothing to a project
// running a QE+APE workflow without leaving the conversation. Each step feeds
// the next its id, exactly as a model would chain them.
func TestIntegrationMCPAssembleAIWorkflow(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	ctx := context.Background()

	// A provider connection is the one piece MCP cannot create — it holds an
	// API key. Seed it the way AI settings would.
	tokenID := seedVendorToken(t, f, "claude")

	var conns struct {
		Connections []struct {
			TokenID  int64    `json:"token_id"`
			Provider string   `json:"provider"`
			Models   []string `json:"models"`
		} `json:"connections"`
	}
	if isErr, text := callTool(t, s, "openloc_list_ai_connections", map[string]any{}, &conns); isErr {
		t.Fatalf("list_ai_connections failed: %s", text)
	}
	if len(conns.Connections) == 0 {
		t.Fatal("the seeded connection is not listed")
	}
	if len(conns.Connections[0].Models) == 0 {
		t.Fatal("connection lists no models, so create_ai_engine has nothing to pick")
	}
	model := conns.Connections[0].Models[0]

	var prompt struct {
		PromptID int64  `json:"prompt_id"`
		Mode     string `json:"mode"`
	}
	if isErr, text := callTool(t, s, "openloc_create_ai_prompt", map[string]any{
		"name": "Demo APE — tag placement",
		"mode": "ape",
		"text": "You are a strict post-editor. Check inline tag placement and correct it.",
	}, &prompt); isErr {
		t.Fatalf("create_ai_prompt failed: %s", text)
	}
	if prompt.Mode != "ape" {
		t.Fatalf("mode = %q, want ape — the flags must round-trip", prompt.Mode)
	}

	var system struct {
		AIEngineID int64  `json:"ai_engine_id"`
		Purpose    string `json:"purpose"`
	}
	if isErr, text := callTool(t, s, "openloc_create_ai_engine", map[string]any{
		"name":             "Demo APE engine",
		"purpose":          "qe_ape",
		"token_id":         tokenID,
		"model":            model,
		"system_prompt_id": prompt.PromptID,
	}, &system); isErr {
		t.Fatalf("create_ai_engine failed: %s", text)
	}

	// The engine is only reachable through a router, so the chain continues
	// here — then the model finds it back through the list tool, as it would.
	var aiRouter struct {
		AIRouterID int64 `json:"ai_router_id"`
		Routes     int   `json:"routes"`
	}
	if isErr, text := callTool(t, s, "openloc_create_ai_router", map[string]any{
		"name":                 "Demo APE router",
		"default_ai_engine_id": system.AIEngineID,
	}, &aiRouter); isErr {
		t.Fatalf("create_ai_router failed: %s", text)
	}
	if aiRouter.Routes != 1 {
		t.Fatalf("routes = %d, want 1 (the default)", aiRouter.Routes)
	}

	var routers struct {
		Routers []struct {
			AIRouterID        int64  `json:"ai_router_id"`
			Name              string `json:"name"`
			DefaultEngineName string `json:"default_engine_name"`
		} `json:"routers"`
	}
	if isErr, text := callTool(t, s, "openloc_list_ai_routers", map[string]any{}, &routers); isErr {
		t.Fatalf("list_ai_routers failed: %s", text)
	}
	if len(routers.Routers) != 1 {
		t.Fatalf("routers = %+v, want the one seeded", routers.Routers)
	}
	if got := routers.Routers[0].DefaultEngineName; got != "Demo APE engine" {
		t.Fatalf("router default engine = %q, want the engine the step will run", got)
	}

	var step struct {
		StepDefinitionID int64 `json:"step_definition_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_step_definition", map[string]any{
		"name":         "Review",
		"step_type":    "hybrid",
		"ai_router_id": routers.Routers[0].AIRouterID,
	}, &step); isErr {
		t.Fatalf("create_step_definition failed: %s", text)
	}

	// The step library reports the router the step runs, so a later call can
	// see what a step is wired to without re-deriving it.
	var steps struct {
		Steps []struct {
			StepDefinitionID int64  `json:"step_definition_id"`
			AIRouterID       int64  `json:"ai_router_id"`
			AIRouterName     string `json:"ai_router_name"`
		} `json:"steps"`
	}
	if isErr, text := callTool(t, s, "openloc_list_step_definitions", map[string]any{}, &steps); isErr {
		t.Fatalf("list_step_definitions failed: %s", text)
	}
	var listed bool
	for _, sd := range steps.Steps {
		if sd.StepDefinitionID != step.StepDefinitionID {
			continue
		}
		listed = true
		if sd.AIRouterID != routers.Routers[0].AIRouterID || sd.AIRouterName != "Demo APE router" {
			t.Fatalf("step lists router %d/%q, want the one it was created with",
				sd.AIRouterID, sd.AIRouterName)
		}
	}
	if !listed {
		t.Fatalf("the created step is missing from the library: %+v", steps.Steps)
	}

	var wf struct {
		WorkflowID int64 `json:"workflow_id"`
		Steps      int   `json:"steps"`
	}
	if isErr, text := callTool(t, s, "openloc_create_workflow", map[string]any{
		"name":                "Demo tag workflow",
		"step_definition_ids": []int64{step.StepDefinitionID},
	}, &wf); isErr {
		t.Fatalf("create_workflow failed: %s", text)
	}
	if wf.Steps != 1 {
		t.Fatalf("steps = %d, want 1", wf.Steps)
	}

	// The payoff: the workflow is usable by the existing project tool.
	var proj struct {
		ProjectID       int64 `json:"project_id"`
		WorkflowApplied bool  `json:"workflow_applied"`
	}
	if isErr, text := callTool(t, s, "openloc_create_project", map[string]any{
		"name":             "Tag placement demo",
		"source_language":  "en",
		"target_languages": []string{"de"},
		"workflow_id":      wf.WorkflowID,
	}, &proj); isErr {
		t.Fatalf("create_project failed: %s", text)
	}
	if !proj.WorkflowApplied {
		t.Fatal("workflow_applied false — the created workflow did not reach the project")
	}

	// And it really landed in this tenant, not just in the response.
	got, err := f.q.GetAIEngine(ctx, db.GetAIEngineParams{ID: system.AIEngineID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatalf("AI system not found in tenant A: %v", err)
	}
	if got.Purpose != "qe_ape" {
		t.Fatalf("purpose = %q, want qe_ape", got.Purpose)
	}
}

// The gate that matters: REST restricts prompt and engine creation to
// owner/admin, so MCP must too. A project manager writing through MCP would
// otherwise be a way around the UI's own rule.
func TestIntegrationMCPAIConfigRequiresOwnerAdmin(t *testing.T) {
	f := newFixture(t)
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "mcp-pm@test.com", "pass", "project_manager")
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))

	ownerAdminOnly := []struct {
		tool string
		args map[string]any
	}{
		{"openloc_create_ai_prompt", map[string]any{
			"name": "PM prompt", "mode": "ape", "text": "text",
		}},
		{"openloc_create_ai_engine", map[string]any{
			"name": "PM engine", "purpose": "qe_ape", "token_id": 1, "model": "x",
		}},
	}
	for _, c := range ownerAdminOnly {
		var out map[string]any
		isErr, text := callTool(t, s, c.tool, c.args, &out)
		if !isErr {
			t.Errorf("%s: a project manager was allowed to create one", c.tool)
			continue
		}
		if !strings.Contains(text, "owner or admin") {
			t.Errorf("%s: error should name the required role, got %q", c.tool, text)
		}
	}

	// Workflows and step definitions ARE manager-allowed in REST, so the same
	// project manager must still be able to compose one.
	var step struct {
		StepDefinitionID int64 `json:"step_definition_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_step_definition", map[string]any{
		"name": "PM human step", "step_type": "human",
	}, &step); isErr {
		t.Fatalf("a project manager must be able to create a step definition: %s", text)
	}
	var wf struct {
		WorkflowID int64 `json:"workflow_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_workflow", map[string]any{
		"name": "PM workflow", "step_definition_ids": []int64{step.StepDefinitionID},
	}, &wf); isErr {
		t.Fatalf("a project manager must be able to create a workflow: %s", text)
	}
}

// A prompt's mode decides which engines can use it, so a mismatch has to be
// refused rather than stored and discovered at translate time.
func TestIntegrationMCPAIEngineRejectsPromptModeMismatch(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	tokenID := seedVendorToken(t, f, "claude")

	var prompt struct {
		PromptID int64 `json:"prompt_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_ai_prompt", map[string]any{
		"name": "An MT prompt", "mode": "mt", "text": "Translate faithfully.",
	}, &prompt); isErr {
		t.Fatalf("create_ai_prompt failed: %s", text)
	}
	// An MT prompt cannot drive a reviewing engine.
	var out map[string]any
	isErr, text := callTool(t, s, "openloc_create_ai_engine", map[string]any{
		"name": "Mismatched", "purpose": "qe_ape", "token_id": tokenID,
		"model": "claude-sonnet-5", "system_prompt_id": prompt.PromptID,
	}, &out)
	if !isErr {
		t.Fatal("an MT prompt was accepted for a qe_ape engine")
	}
	if !strings.Contains(text, "MT prompt") {
		t.Errorf("error should explain the mode mismatch, got %q", text)
	}
}

// Segment context and per-language rules are editable over MCP, not UI-only: an
// agent that can create a prompt but not say what travels with each segment can
// only make half a prompt. The list has to read them back, or the replace-all
// update is impossible to do safely.
func TestIntegrationMCPAIPromptContextAndLanguageRulesRoundTrip(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var created struct {
		PromptID int64 `json:"prompt_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_ai_prompt", map[string]any{
		"name": "Reviewer with context", "mode": "qe_ape", "text": "Judge strictly.",
		"segment_context": []string{"tm_match", "terms"},
		"language_additions": []map[string]any{
			{"target_languages": []string{"DE"}, "text": "Use formal address."},
		},
	}, &created); isErr {
		t.Fatalf("create_ai_prompt failed: %s", text)
	}

	type listed struct {
		Prompts []struct {
			PromptID          int64    `json:"prompt_id"`
			Mode              string   `json:"mode"`
			SegmentContext    []string `json:"segment_context"`
			LanguageAdditions []struct {
				TargetLanguages []string `json:"target_languages"`
				Text            string   `json:"text"`
			} `json:"language_additions"`
		} `json:"prompts"`
	}
	find := func() (p struct {
		PromptID          int64    `json:"prompt_id"`
		Mode              string   `json:"mode"`
		SegmentContext    []string `json:"segment_context"`
		LanguageAdditions []struct {
			TargetLanguages []string `json:"target_languages"`
			Text            string   `json:"text"`
		} `json:"language_additions"`
	}) {
		t.Helper()
		var out listed
		if isErr, text := callTool(t, s, "openloc_list_ai_prompts", map[string]any{}, &out); isErr {
			t.Fatalf("list_ai_prompts failed: %s", text)
		}
		for _, c := range out.Prompts {
			if c.PromptID == created.PromptID {
				return c
			}
		}
		t.Fatal("the created prompt is missing from the list")
		return
	}

	got := find()
	if len(got.SegmentContext) != 2 {
		t.Errorf("segment_context = %v, want both keys", got.SegmentContext)
	}
	// Codes come back normalized, so a caller can send them straight back.
	if len(got.LanguageAdditions) != 1 || got.LanguageAdditions[0].TargetLanguages[0] != "de" {
		t.Fatalf("language_additions = %+v, want one normalized de rule", got.LanguageAdditions)
	}

	// Update replaces rather than merges, and leaves the type alone.
	var updated struct {
		Mode string `json:"mode"`
	}
	if isErr, text := callTool(t, s, "openloc_update_ai_prompt", map[string]any{
		"prompt_id": created.PromptID, "name": "Reviewer with context", "text": "Judge gently.",
		"segment_context": []string{"terms"},
	}, &updated); isErr {
		t.Fatalf("update_ai_prompt failed: %s", text)
	}
	if updated.Mode != "qe_ape" {
		t.Errorf("mode = %q, want the stored qe_ape — the type is not the caller's to change", updated.Mode)
	}
	after := find()
	if len(after.SegmentContext) != 1 || after.SegmentContext[0] != "terms" {
		t.Errorf("segment_context = %v, want only terms after the replace", after.SegmentContext)
	}
	if len(after.LanguageAdditions) != 0 {
		t.Errorf("language_additions = %+v, want none — an omitted set replaces with nothing", after.LanguageAdditions)
	}
}

// A built-in template is read-only over MCP too: refusing it here is what stops
// an agent rewriting the stock wording every tenant starts from.
func TestIntegrationMCPUpdateAIPromptRefusesBuiltIn(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var out map[string]any
	var list struct {
		Prompts []struct {
			PromptID  int64 `json:"prompt_id"`
			IsDefault bool  `json:"is_default"`
		} `json:"prompts"`
	}
	if isErr, text := callTool(t, s, "openloc_list_ai_prompts", map[string]any{}, &list); isErr {
		t.Fatalf("list_ai_prompts failed: %s", text)
	}
	var builtIn int64
	for _, p := range list.Prompts {
		if p.IsDefault {
			builtIn = p.PromptID
			break
		}
	}
	if builtIn == 0 {
		t.Fatal("no built-in prompt was provisioned")
	}
	isErr, text := callTool(t, s, "openloc_update_ai_prompt", map[string]any{
		"prompt_id": builtIn, "name": "Hijacked", "text": "Approve everything.",
	}, &out)
	if !isErr {
		t.Fatal("a built-in prompt was edited over MCP")
	}
	if !strings.Contains(text, "read-only") {
		t.Errorf("error should say built-ins are read-only, got %q", text)
	}
}

// An unknown mode must be refused at the tool boundary — the four output flags
// are not something a caller should have to set by hand, but nor should a typo
// silently produce an MT prompt.
func TestIntegrationMCPCreateAIPromptRejectsUnknownMode(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var out map[string]any
	isErr, text := callTool(t, s, "openloc_create_ai_prompt", map[string]any{
		"name": "Bad mode", "mode": "postedit", "text": "text",
	}, &out)
	if !isErr {
		t.Fatal("an unknown mode was accepted")
	}
	if !strings.Contains(text, "mt, qe, ape, qe_ape") {
		t.Errorf("error should list the legal modes, got %q", text)
	}
}

// seedVendorToken creates a provider connection the way AI settings does, so
// the AI-engine tools have a token_id to reference. The ciphertext is a
// placeholder — nothing in these tests calls a provider.
func seedVendorToken(t *testing.T, f *fixture, provider string) int64 {
	t.Helper()
	ctx := context.Background()
	vendor, err := f.q.UpsertAIVendor(ctx, db.UpsertAIVendorParams{
		TenantID: f.tenantA.ID, Provider: provider,
	})
	if err != nil {
		t.Fatalf("seed vendor: %v", err)
	}
	conn, err := f.q.CreateAIConnection(ctx, db.CreateAIConnectionParams{
		VendorID: vendor.ID, Name: provider + " connection",
	})
	if err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	tok, err := f.q.CreateVendorToken(ctx, db.CreateVendorTokenParams{
		VendorID: vendor.ID, ConnectionID: conn.ID, Name: provider + " key",
		ApiKeyEncrypted: "placeholder", KeyLast4: "1234",
	})
	if err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return tok.ID
}

// Pointing a project at a router is what makes a created engine take effect:
// the engine and the router are org-wide, the pointer is per project.
func TestIntegrationMCPSetProjectMTRouter(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	ctx := context.Background()
	tokenID := seedVendorToken(t, f, "claude")

	engineID := createMTEngine(t, s, tokenID, "MCP_Demo MT engine")
	primary := testutil.SeedMTRouter(t, f.q, f.tenantA.ID, "MCP_Primary MT router", nil, &engineID)

	// Discovery first: the set tool is only usable if the list tool names the
	// router and what it routes to.
	var routers struct {
		Routers []struct {
			MTRouterID        int64  `json:"mt_router_id"`
			Name              string `json:"name"`
			DefaultEngineName string `json:"default_engine_name"`
		} `json:"routers"`
	}
	if isErr, text := callTool(t, s, "openloc_list_mt_routers", map[string]any{}, &routers); isErr {
		t.Fatalf("list_mt_routers failed: %s", text)
	}
	var listed bool
	for _, rt := range routers.Routers {
		if rt.MTRouterID != primary {
			continue
		}
		listed = true
		if rt.Name != "MCP_Primary MT router" || rt.DefaultEngineName != "MCP_Demo MT engine" {
			t.Fatalf("router listed as %q on %q, want the seeded name and engine",
				rt.Name, rt.DefaultEngineName)
		}
	}
	if !listed {
		t.Fatalf("the seeded router is missing from the listing: %+v", routers.Routers)
	}

	var out struct {
		ProjectID    int64  `json:"project_id"`
		MTRouterID   int64  `json:"mt_router_id"`
		MTRouterName string `json:"mt_router_name"`
	}
	if isErr, text := callTool(t, s, "openloc_set_project_mt_router", map[string]any{
		"project_id": f.projectA.ID, "mt_router_id": primary,
	}, &out); isErr {
		t.Fatalf("set_project_mt_router failed: %s", text)
	}
	if out.MTRouterID != primary || out.MTRouterName != "MCP_Primary MT router" {
		t.Fatalf("result = %+v, want the router just set", out)
	}

	// The pointer reached the project's settings profile, not just the response.
	profile, err := f.q.GetSettingsProfile(ctx, f.projectA.SettingsProfileID)
	if err != nil {
		t.Fatalf("read settings profile: %v", err)
	}
	if profile.MtRouterID == nil || *profile.MtRouterID != primary {
		t.Fatalf("profile router = %v, want %d", profile.MtRouterID, primary)
	}

	// A project points at exactly ONE router, so naming a second REPLACES the
	// first rather than adding to it.
	fallback := testutil.SeedMTRouter(t, f.q, f.tenantA.ID, "MCP_Fallback MT router", nil, &engineID)
	if isErr, text := callTool(t, s, "openloc_set_project_mt_router", map[string]any{
		"project_id": f.projectA.ID, "mt_router_id": fallback,
	}, &out); isErr {
		t.Fatalf("second set_project_mt_router failed: %s", text)
	}
	profile, err = f.q.GetSettingsProfile(ctx, f.projectA.SettingsProfileID)
	if err != nil {
		t.Fatalf("re-read settings profile: %v", err)
	}
	if profile.MtRouterID == nil || *profile.MtRouterID != fallback {
		t.Fatalf("profile router = %v, want the second router %d", profile.MtRouterID, fallback)
	}
}

// Both ids are a tenant boundary. Another org's router would route this org's
// jobs through engines it does not own; another org's project would have its
// machine translation rewired. Either reads as not-found, and nothing is written.
func TestIntegrationMCPSetProjectMTRouterRejectsOtherTenantIDs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	asA := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	asB := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))

	// No engine on either router: the tool must refuse before it looks at what
	// a router routes to.
	routerA := testutil.SeedMTRouter(t, f.q, f.tenantA.ID, "MCP_Tenant A router", nil, nil)
	routerB := testutil.SeedMTRouter(t, f.q, f.tenantB.ID, "MCP_Tenant B router", nil, nil)

	for _, tc := range []struct {
		name   string
		caller *sdk.ClientSession
	}{
		{"tenant A names tenant B's router", asA},
		{"tenant B names tenant A's project", asB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isErr, text := callTool(t, tc.caller, "openloc_set_project_mt_router", map[string]any{
				"project_id": f.projectA.ID, "mt_router_id": routerB,
			}, nil)
			if !isErr {
				t.Fatal("tenant A's project was pointed at tenant B's MT router")
			}
			assertOpaqueNotFound(t, text)

			profile, err := f.q.GetSettingsProfile(ctx, f.projectA.SettingsProfileID)
			if err != nil {
				t.Fatalf("read settings profile: %v", err)
			}
			if profile.MtRouterID != nil {
				t.Fatalf("project picked up router %d", *profile.MtRouterID)
			}
		})
	}

	// Each tenant may use its own ids, so the refusals above come from the
	// tenant check rather than from an unusable project or router.
	for _, own := range []struct {
		caller            *sdk.ClientSession
		project, router   int64
		settingsProfileID int64
	}{
		{asA, f.projectA.ID, routerA, f.projectA.SettingsProfileID},
		{asB, f.projectB.ID, routerB, f.projectB.SettingsProfileID},
	} {
		if isErr, text := callTool(t, own.caller, "openloc_set_project_mt_router", map[string]any{
			"project_id": own.project, "mt_router_id": own.router,
		}, nil); isErr {
			t.Fatalf("own project %d on own router %d: %s", own.project, own.router, text)
		}
		profile, err := f.q.GetSettingsProfile(ctx, own.settingsProfileID)
		if err != nil {
			t.Fatalf("read settings profile: %v", err)
		}
		if profile.MtRouterID == nil || *profile.MtRouterID != own.router {
			t.Fatalf("project %d router = %v, want %d", own.project, profile.MtRouterID, own.router)
		}
	}
}

// The tag-placement demo, end to end and in one conversation: two prompts, an
// MT engine and an APE engine, a router for each, a human step and a hybrid one,
// a workflow, a project on it, and the project pointed at the MT router. It is
// the flow a user reads out loud, and every step here is a tool call — routers
// included.
func TestIntegrationMCPTagPlacementDemoEndToEnd(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	ctx := context.Background()
	tokenID := seedVendorToken(t, f, "openai")
	model := firstModel(t, s)

	createPrompt := func(name, mode, text string) int64 {
		t.Helper()
		var out struct {
			PromptID int64 `json:"prompt_id"`
		}
		if isErr, msg := callTool(t, s, "openloc_create_ai_prompt", map[string]any{
			"name": name, "mode": mode, "text": text,
		}, &out); isErr {
			t.Fatalf("create_ai_prompt(%s) failed: %s", name, msg)
		}
		return out.PromptID
	}
	createEngine := func(name, purpose string, systemPromptID int64) int64 {
		t.Helper()
		var out struct {
			AIEngineID int64 `json:"ai_engine_id"`
		}
		if isErr, msg := callTool(t, s, "openloc_create_ai_engine", map[string]any{
			"name": name, "purpose": purpose, "token_id": tokenID, "model": model,
			"system_prompt_id": systemPromptID,
		}, &out); isErr {
			t.Fatalf("create_ai_engine(%s) failed: %s", name, msg)
		}
		return out.AIEngineID
	}

	// 2-5: the prompts and the two engines they drive.
	mtEngine := createEngine("Wrong Tag Placement MT Engine", "mt",
		createPrompt("Wrong Tag Placement MT", "mt", "Translate this technical documentation faithfully."))
	apeEngine := createEngine("Tag Placement APE engine", "qe_ape",
		createPrompt("Tag Placement APE", "ape", "You are a strict post-editor. Fix inline tag placement."))

	// The APE engine only runs once a router points at it.
	var aiRouter struct {
		AIRouterID int64 `json:"ai_router_id"`
	}
	if isErr, msg := callTool(t, s, "openloc_create_ai_router", map[string]any{
		"name":                 "Tag Placement APE router",
		"default_ai_engine_id": apeEngine,
	}, &aiRouter); isErr {
		t.Fatalf("create_ai_router failed: %s", msg)
	}

	// 6: a plain human step, and a hybrid one that runs the APE router.
	createStep := func(name, stepType string, routerID int64) int64 {
		t.Helper()
		args := map[string]any{"name": name, "step_type": stepType}
		if routerID != 0 {
			args["ai_router_id"] = routerID
		}
		var out struct {
			StepDefinitionID int64 `json:"step_definition_id"`
		}
		if isErr, msg := callTool(t, s, "openloc_create_step_definition", args, &out); isErr {
			t.Fatalf("create_step_definition(%s) failed: %s", name, msg)
		}
		return out.StepDefinitionID
	}
	translate := createStep("Wrong_Tag_Placement_Translation", "human", 0)
	review := createStep("Tag_Placement_Review", "hybrid", aiRouter.AIRouterID)

	// 7-8: the workflow, in order, and a project running on it.
	var wf struct {
		WorkflowID int64 `json:"workflow_id"`
		Steps      int   `json:"steps"`
	}
	if isErr, msg := callTool(t, s, "openloc_create_workflow", map[string]any{
		"name":                "Tag Placement Demo",
		"step_definition_ids": []int64{translate, review},
	}, &wf); isErr {
		t.Fatalf("create_workflow failed: %s", msg)
	}
	if wf.Steps != 2 {
		t.Fatalf("steps = %d, want 2", wf.Steps)
	}
	var proj struct {
		ProjectID       int64 `json:"project_id"`
		WorkflowApplied bool  `json:"workflow_applied"`
	}
	if isErr, msg := callTool(t, s, "openloc_create_project", map[string]any{
		"name": "Tag Placement Demo", "source_language": "en",
		"target_languages": []string{"de"}, "workflow_id": wf.WorkflowID,
	}, &proj); isErr {
		t.Fatalf("create_project failed: %s", msg)
	}
	if !proj.WorkflowApplied {
		t.Fatal("workflow_applied false — the project is not on the demo workflow")
	}

	// 9: machine translation is the MT engine, which means a router over it and
	// the project pointed there.
	var mtRouter struct {
		MTRouterID int64 `json:"mt_router_id"`
	}
	if isErr, msg := callTool(t, s, "openloc_create_mt_router", map[string]any{
		"name":                 "Wrong Tag Placement MT router",
		"default_ai_engine_id": mtEngine,
	}, &mtRouter); isErr {
		t.Fatalf("create_mt_router failed: %s", msg)
	}
	if isErr, msg := callTool(t, s, "openloc_set_project_mt_router", map[string]any{
		"project_id": proj.ProjectID, "mt_router_id": mtRouter.MTRouterID,
	}, &struct{}{}); isErr {
		t.Fatalf("set_project_mt_router failed: %s", msg)
	}

	// What the demo actually needs to be true afterwards: the project's steps
	// are the two in order, and its MT resolves to the LLM engine for German.
	project, err := f.q.GetProjectByID(ctx, proj.ProjectID)
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	steps, err := f.q.ListSettingsWorkflowSteps(ctx, project.SettingsProfileID)
	if err != nil {
		t.Fatalf("read workflow steps: %v", err)
	}
	var names []string
	for _, st := range steps {
		names = append(names, st.Name)
	}
	if len(names) != 2 || names[0] != "Wrong_Tag_Placement_Translation" || names[1] != "Tag_Placement_Review" {
		t.Fatalf("project steps = %v, want the demo pair in order", names)
	}
	ref, err := mtrouter.ResolveEngine(ctx, f.q, f.tenantA.ID, mtRouter.MTRouterID, "de")
	if err != nil {
		t.Fatalf("resolve MT engine: %v", err)
	}
	if ref.AIEngineID == nil || *ref.AIEngineID != mtEngine {
		t.Fatalf("de resolves to %+v, want the LLM MT engine %d", ref, mtEngine)
	}
}

// An AI router is owner/admin like the engines it routes to, while an MT router
// is manager-level like the MT engines it routes to. The two
// gates differ, so both are pinned.
func TestIntegrationMCPRouterCreateRoleGates(t *testing.T) {
	f := newFixture(t)
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "mcp-router-pm@test.com", "pass", "project_manager")
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))

	var out map[string]any
	isErr, text := callTool(t, s, "openloc_create_ai_router", map[string]any{
		"name": "PM AI router", "default_ai_engine_id": 1,
	}, &out)
	if !isErr {
		t.Error("a project manager was allowed to create an AI router")
	} else if !strings.Contains(text, "owner or admin") {
		t.Errorf("error should name the required role, got %q", text)
	}

	// The MT side is theirs to run: a manager configures MT engines and routers
	// in the UI too, so the tool must not be stricter than the page.
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	engineID := createMTEngine(t, owner, seedVendorToken(t, f, "claude"), "MCP_PM MT engine")
	var router struct {
		MTRouterID int64 `json:"mt_router_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_mt_router", map[string]any{
		"name": "PM MT router", "default_ai_engine_id": engineID,
	}, &router); isErr {
		t.Fatalf("a project manager must be able to create an MT router: %s", text)
	}
	if router.MTRouterID == 0 {
		t.Fatal("no router id returned")
	}
}

// A router with nothing to route to would silently skip every job, and an MT
// router pointed at a review engine would fail at translate time. Both are
// refused with a message the model can act on.
func TestIntegrationMCPCreateRouterRejectsBadRoutes(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	tokenID := seedVendorToken(t, f, "claude")

	var out map[string]any
	if isErr, text := callTool(t, s, "openloc_create_mt_router", map[string]any{
		"name": "Empty router",
	}, &out); !isErr {
		t.Error("a router with no default and no rules was accepted")
	} else if !strings.Contains(text, "at least one route") {
		t.Errorf("error should explain the empty router, got %q", text)
	}

	// A qe_ape engine cannot machine-translate.
	apeEngine := func() int64 {
		var prompt struct {
			PromptID int64 `json:"prompt_id"`
		}
		if isErr, text := callTool(t, s, "openloc_create_ai_prompt", map[string]any{
			"name": "Reject APE prompt", "mode": "ape", "text": "post-edit",
		}, &prompt); isErr {
			t.Fatalf("create_ai_prompt failed: %s", text)
		}
		var engine struct {
			AIEngineID int64 `json:"ai_engine_id"`
		}
		if isErr, text := callTool(t, s, "openloc_create_ai_engine", map[string]any{
			"name": "Reject APE engine", "purpose": "qe_ape", "token_id": tokenID,
			"model": firstModel(t, s), "system_prompt_id": prompt.PromptID,
		}, &engine); isErr {
			t.Fatalf("create_ai_engine failed: %s", text)
		}
		return engine.AIEngineID
	}()
	if isErr, text := callTool(t, s, "openloc_create_mt_router", map[string]any{
		"name": "Wrong-purpose router", "default_ai_engine_id": apeEngine,
	}, &out); !isErr {
		t.Error("an MT router over a QE+APE engine was accepted")
	} else if !strings.Contains(text, "QE+APE") {
		t.Errorf("error should explain the purpose mismatch, got %q", text)
	}
}

// createMTEngine builds an mt-purpose engine through the tools, the way the
// demo prompt does, and returns its id.
func createMTEngine(t *testing.T, s *sdk.ClientSession, tokenID int64, name string) int64 {
	t.Helper()
	var prompt struct {
		PromptID int64 `json:"prompt_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_ai_prompt", map[string]any{
		"name": name + " prompt", "mode": "mt", "text": "Translate faithfully.",
	}, &prompt); isErr {
		t.Fatalf("create_ai_prompt failed: %s", text)
	}
	var sys struct {
		AIEngineID int64 `json:"ai_engine_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_ai_engine", map[string]any{
		"name": name, "purpose": "mt", "token_id": tokenID,
		"model": firstModel(t, s), "system_prompt_id": prompt.PromptID,
	}, &sys); isErr {
		t.Fatalf("create_ai_engine failed: %s", text)
	}
	return sys.AIEngineID
}

func firstModel(t *testing.T, s *sdk.ClientSession) string {
	t.Helper()
	var conns struct {
		Connections []struct {
			Models []string `json:"models"`
		} `json:"connections"`
	}
	if isErr, text := callTool(t, s, "openloc_list_ai_connections", map[string]any{}, &conns); isErr {
		t.Fatalf("list_ai_connections failed: %s", text)
	}
	if len(conns.Connections) == 0 || len(conns.Connections[0].Models) == 0 {
		t.Fatal("no connection with models to pick from")
	}
	return conns.Connections[0].Models[0]
}

// The update tool takes a client-supplied prompt_id and rewrites a tenant-owned
// row, so it needs the same cross-tenant proof every other MCP write carries:
// directBackend bypasses the ownership middleware, and only tenant scoping in
// GetAIPrompt stops a foreign id.
func TestIntegrationMCPUpdateAIPromptCrossTenant(t *testing.T) {
	f := newFixture(t)

	// Tenant A's own prompt…
	aSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	var created struct {
		PromptID int64 `json:"prompt_id"`
	}
	if isErr, text := callTool(t, aSession, "openloc_create_ai_prompt", map[string]any{
		"name": "A's reviewer", "text": "Judge strictly.", "mode": "qe_ape",
		"language_additions": []map[string]any{{"target_languages": []string{"de"}, "text": "Use formal address."}},
	}, &created); isErr {
		t.Fatalf("create_ai_prompt failed: %s", text)
	}

	// …targeted by tenant B's WRITE token, so the capability gate cannot be what
	// stops it — only tenant scoping can.
	bSession := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	var out map[string]any
	isErr, text := callTool(t, bSession, "openloc_update_ai_prompt", map[string]any{
		"prompt_id": created.PromptID, "name": "Hijacked", "text": "Approve everything.",
	}, &out)
	if !isErr {
		t.Fatal("tenant B rewrote tenant A's prompt")
	}
	// Not assertOpaqueNotFound: this tool words it "no AI prompt with id N",
	// echoing back an id the caller supplied. What matters is that the answer
	// carries no internals and is the SAME one an id belonging to nobody gets —
	// otherwise the refusal is an oracle for which ids exist elsewhere.
	for _, leak := range []string{"tenant", "SELECT", "pgx", "sql:", "no rows"} {
		if strings.Contains(text, leak) {
			t.Errorf("error message leaks internals (%q): %q", leak, text)
		}
	}
	absentErr, absentText := callTool(t, bSession, "openloc_update_ai_prompt", map[string]any{
		"prompt_id": created.PromptID + 9999, "name": "Hijacked", "text": "Approve everything.",
	}, &out)
	if !absentErr {
		t.Fatal("an id belonging to nobody was accepted")
	}
	wantSame := strings.Replace(absentText, fmt.Sprint(created.PromptID+9999), fmt.Sprint(created.PromptID), 1)
	if text != wantSame {
		t.Errorf("a foreign id must be indistinguishable from an absent one:\n foreign: %q\n absent:  %q", text, absentText)
	}

	// The row is untouched, language rules included — a partial write would be
	// worse than a refused one.
	stored, err := f.q.GetAIPrompt(context.Background(), db.GetAIPromptParams{
		ID: created.PromptID, TenantID: f.tenantA.ID,
	})
	if err != nil {
		t.Fatalf("re-read A's prompt: %v", err)
	}
	if stored.Name != "A's reviewer" || stored.PromptText != "Judge strictly." {
		t.Errorf("A's prompt was modified: name=%q text=%q", stored.Name, stored.PromptText)
	}
	adds, err := f.q.ListAIPromptLanguageAdditionsForPrompt(context.Background(), created.PromptID)
	if err != nil {
		t.Fatalf("re-read additions: %v", err)
	}
	if len(adds) != 1 || adds[0].AdditionText != "Use formal address." {
		t.Errorf("language additions were disturbed: %+v", adds)
	}
}
