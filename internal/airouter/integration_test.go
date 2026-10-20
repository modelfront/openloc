// AI router CRUD, validation, role gating, tenant isolation, the delete
// guard, step-router validation and runtime resolution
package airouter_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/aiengine"
	"openloc/internal/airouter"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
	"openloc/internal/testutil"
)

type routerResp struct {
	Data struct {
		ID              int64  `json:"id"`
		TenantID        int64  `json:"tenant_id"`
		Name            string `json:"name"`
		DefaultEngineID *int64 `json:"default_engine_id"`
		Rules           []struct {
			ID         int64  `json:"id"`
			TargetLang string `json:"target_lang"`
			EngineID   int64  `json:"engine_id"`
		} `json:"rules"`
	} `json:"data"`
}

type routerListResp struct {
	Data []struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Rules []struct {
			TargetLang string `json:"target_lang"`
			EngineID   int64  `json:"engine_id"`
		} `json:"rules"`
	} `json:"data"`
}

func seedEngine(t *testing.T, q db.Querier, tenantID int64, name, purpose string) int64 {
	t.Helper()
	e, err := q.CreateAIEngine(context.Background(), db.CreateAIEngineParams{TenantID: tenantID, Name: name, Purpose: purpose})
	if err != nil {
		t.Fatalf("seed AI engine: %v", err)
	}
	return e.ID
}

func TestIntegrationAIRouterCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "A")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@a.com", "pass", "owner")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@a.com", "pass", "project_manager")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "lin@a.com", "pass", "linguist")
	qeA := seedEngine(t, q, tenant.ID, "QE A", aiengine.PurposeQEAPE)
	qeB := seedEngine(t, q, tenant.ID, "QE B", aiengine.PurposeQEAPE)
	mt := seedEngine(t, q, tenant.ID, "MT", aiengine.PurposeMT)
	other := testutil.SeedTenant(t, q, "B")
	foreignEngine := seedEngine(t, q, other.ID, "B's QE", aiengine.PurposeQEAPE)

	post := func(body map[string]any, userID int64, role string) (int, string, *routerResp) {
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/routers", body, userID, tenant.ID, role)
		var out routerResp
		if rec.Code == http.StatusCreated {
			testutil.DecodeJSON(t, rec, &out)
		}
		return rec.Code, rec.Body.String(), &out
	}
	expectBad := func(body map[string]any, wantSubstr string) {
		t.Helper()
		code, resp, _ := post(body, owner.ID, "owner")
		if code != http.StatusBadRequest {
			t.Fatalf("want 400 for %v, got %d %s", body, code, resp)
		}
		if !strings.Contains(resp, wantSubstr) {
			t.Errorf("expected error containing %q, got %s", wantSubstr, resp)
		}
	}

	// Validation: every engine must be this tenant's QE+APE engine, and a
	// language may appear once. Codes are normalized before the check.
	expectBad(map[string]any{"name": "", "rules": []any{}}, "name is required")
	expectBad(map[string]any{"name": "Empty", "rules": []any{}}, "at least one route")
	expectBad(map[string]any{"name": "X", "default_engine_id": mt}, "MT engine")
	expectBad(map[string]any{"name": "X", "default_engine_id": foreignEngine}, "not found")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{{"target_lang": "fr", "engine_id": mt}}}, "MT engine")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{{"target_lang": "fr", "engine_id": foreignEngine}}}, "not found")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{{"target_lang": "", "engine_id": qeA}}}, "target language is required")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{{"target_lang": "*", "engine_id": qeA}}}, "target language is required")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{
		{"target_lang": "fr", "engine_id": qeA}, {"target_lang": "FR", "engine_id": qeB},
	}}, "listed twice")

	// Owner creates a router with a default and two rules; rules come back
	// normalized and in order. A rules-only router (no default) is legal.
	code, body, created := post(map[string]any{
		"name": " Production ", "default_engine_id": qeA,
		"rules": []map[string]any{{"target_lang": "DE", "engine_id": qeB}, {"target_lang": "fr-CA", "engine_id": qeB}},
	}, owner.ID, "owner")
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	if created.Data.Name != "Production" || created.Data.TenantID != tenant.ID || created.Data.DefaultEngineID == nil || *created.Data.DefaultEngineID != qeA {
		t.Fatalf("unexpected router: %+v", created.Data)
	}
	if len(created.Data.Rules) != 2 || created.Data.Rules[0].TargetLang != "de" || created.Data.Rules[1].TargetLang != "fr-ca" || created.Data.Rules[1].EngineID != qeB {
		t.Fatalf("unexpected rules: %+v", created.Data.Rules)
	}
	if code, body, _ := post(map[string]any{"name": "Rules only", "rules": []map[string]any{{"target_lang": "it", "engine_id": qeB}}}, owner.ID, "owner"); code != http.StatusCreated {
		t.Fatalf("rules-only router: %d %s", code, body)
	}

	// Duplicate name (case-insensitive) → 409.
	if code, _, _ := post(map[string]any{"name": "production", "default_engine_id": qeA}, owner.ID, "owner"); code != http.StatusConflict {
		t.Fatalf("duplicate name: want 409, got %d", code)
	}

	// Role gate: managers read, only owner/admin write, linguists see nothing.
	if code, _, _ := post(map[string]any{"name": "PM made"}, pm.ID, "project_manager"); code != http.StatusForbidden {
		t.Fatalf("PM create: want 403, got %d", code)
	}
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/routers", nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list routerListResp
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 2 || list.Data[0].Name != "Production" || len(list.Data[0].Rules) != 2 {
		t.Fatalf("list should carry both routers with rules, got %+v", list.Data)
	}
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/routers", nil, linguist.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/ai/routers/%d", created.Data.ID), map[string]any{"name": "Nope"}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/routers/%d", created.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Update replaces name, default and the whole rule set.
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/ai/routers/%d", created.Data.ID), map[string]any{
		"name": "Production v2", "default_engine_id": nil,
		"rules": []map[string]any{{"target_lang": "es", "engine_id": qeA}},
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var updated routerResp
	testutil.DecodeJSON(t, rec, &updated)
	if updated.Data.Name != "Production v2" || updated.Data.DefaultEngineID != nil || len(updated.Data.Rules) != 1 || updated.Data.Rules[0].TargetLang != "es" {
		t.Fatalf("update not applied: %+v", updated.Data)
	}
	rules, err := q.ListAIRouterRules(context.Background(), created.Data.ID)
	if err != nil || len(rules) != 1 {
		t.Fatalf("old rules must be gone: %v / %d", err, len(rules))
	}
	// A bad update leaves the router untouched.
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/ai/routers/%d", created.Data.ID), map[string]any{
		"name": "Broken", "rules": []map[string]any{{"target_lang": "es", "engine_id": mt}},
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if rt, _ := q.GetAIRouter(context.Background(), db.GetAIRouterParams{ID: created.Data.ID, TenantID: tenant.ID}); rt.Name != "Production v2" {
		t.Fatalf("refused update must not rename: %q", rt.Name)
	}

	// Delete guard: a live library step on the router → 409; once the step is
	// retired the delete goes through and the router leaves the list.
	def := testutil.SeedStepDefinition(t, q, tenant.ID, "AI review", "hybrid", "", &created.Data.ID)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/routers/%d", created.Data.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "1 workflow step") {
		t.Errorf("409 should count the steps, got %s", rec.Body.String())
	}
	if err := q.SoftDeleteStepDefinition(context.Background(), db.SoftDeleteStepDefinitionParams{ID: def.ID, TenantID: tenant.ID}); err != nil {
		t.Fatal(err)
	}
	// A profile snapshot counts too.
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"de"})
	ws, err := q.CreateSettingsWorkflowStep(context.Background(), db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "AI review", StepOrder: 5, RoleRequired: "linguist",
		AiRouterID: &created.Data.ID, StepType: "hybrid",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/routers/%d", created.Data.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if err := q.DeleteSettingsWorkflowStep(context.Background(), db.DeleteSettingsWorkflowStepParams{ID: ws.ID, SettingsProfileID: project.SettingsProfileID}); err != nil {
		t.Fatal(err)
	}
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/routers/%d", created.Data.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/routers", nil, owner.ID, tenant.ID, "owner")
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 1 || list.Data[0].Name != "Rules only" {
		t.Fatalf("deleted router still listed: %+v", list.Data)
	}
	// Soft delete: the row survives for history / running jobs.
	if _, err := q.GetAIRouterAny(context.Background(), db.GetAIRouterAnyParams{ID: created.Data.ID, TenantID: tenant.ID}); err != nil {
		t.Fatalf("soft-deleted router must still resolve for runtime lookups: %v", err)
	}
}

func TestIntegrationAIRouterTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "A")
	tenantB := testutil.SeedTenant(t, q, "B")
	adminB := testutil.SeedUserWithRole(t, q, tenantB.ID, "b@b.com", "pass", "admin")
	engineA := seedEngine(t, q, tenantA.ID, "A's QE", aiengine.PurposeQEAPE)
	routerA := testutil.SeedAIRouter(t, q, tenantA.ID, "A's router", &engineA)

	for _, tc := range []struct{ method, path string }{
		{"GET", fmt.Sprintf("/api/ai/routers/%d", routerA.ID)},
		{"PUT", fmt.Sprintf("/api/ai/routers/%d", routerA.ID)},
		{"DELETE", fmt.Sprintf("/api/ai/routers/%d", routerA.ID)},
	} {
		var body map[string]any
		if tc.method == "PUT" {
			body = map[string]any{"name": "Stolen"}
		}
		rec := testutil.DoRequestWithRole(t, router, tc.method, tc.path, body, adminB.ID, tenantB.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	}
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/routers", nil, adminB.ID, tenantB.ID, "admin")
	var list routerListResp
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 0 {
		t.Fatalf("tenant B sees %d of A's routers", len(list.Data))
	}
	if rt, err := q.GetAIRouter(context.Background(), db.GetAIRouterParams{ID: routerA.ID, TenantID: tenantA.ID}); err != nil || rt.Name != "A's router" {
		t.Fatalf("A's router should survive B's attempts: %v %+v", err, rt)
	}
	// The runtime resolver is tenant-scoped too.
	if _, err := airouter.ResolveEngineID(context.Background(), q, tenantB.ID, routerA.ID, "fr"); err == nil {
		t.Fatal("resolving A's router under tenant B must fail")
	}
}

// ResolveEngineID: rule for the job's language (exact, then same base), else
// the default, else nil — and a retired router still resolves for jobs
// already sitting on it.
func TestIntegrationResolveEngineID(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "Resolve")
	def := seedEngine(t, q, tenant.ID, "Default", aiengine.PurposeQEAPE)
	fr := seedEngine(t, q, tenant.ID, "French", aiengine.PurposeQEAPE)
	deDE := seedEngine(t, q, tenant.ID, "German (DE)", aiengine.PurposeQEAPE)

	withDefault := testutil.SeedAIRouter(t, q, tenant.ID, "With default", &def)
	testutil.SeedAIRouterRule(t, q, withDefault.ID, "fr", fr)
	testutil.SeedAIRouterRule(t, q, withDefault.ID, "de-de", deDE)
	noDefault := testutil.SeedAIRouter(t, q, tenant.ID, "No default", nil)
	testutil.SeedAIRouterRule(t, q, noDefault.ID, "fr", fr)

	cases := []struct {
		router int64
		lang   string
		want   int64 // 0 = nil
	}{
		{withDefault.ID, "fr", fr},
		{withDefault.ID, "fr-CA", fr},
		{withDefault.ID, "de-DE", deDE},
		{withDefault.ID, "de", deDE},
		{withDefault.ID, "es", def},
		{noDefault.ID, "fr", fr},
		{noDefault.ID, "es", 0},
	}
	for _, c := range cases {
		got, err := airouter.ResolveEngineID(ctx, q, tenant.ID, c.router, c.lang)
		if err != nil {
			t.Fatalf("router %d / %s: %v", c.router, c.lang, err)
		}
		switch {
		case c.want == 0 && got != nil:
			t.Errorf("router %d / %s: want no engine, got %d", c.router, c.lang, *got)
		case c.want != 0 && (got == nil || *got != c.want):
			t.Errorf("router %d / %s: want engine %d, got %v", c.router, c.lang, c.want, got)
		}
	}

	if err := q.SoftDeleteAIRouter(ctx, db.SoftDeleteAIRouterParams{ID: withDefault.ID, TenantID: tenant.ID}); err != nil {
		t.Fatal(err)
	}
	if got, err := airouter.ResolveEngineID(ctx, q, tenant.ID, withDefault.ID, "es"); err != nil || got == nil || *got != def {
		t.Fatalf("a retired router must still resolve for running jobs: %v %v", got, err)
	}
	if _, err := airouter.ResolveEngineID(ctx, q, tenant.ID, 999999, "es"); err == nil {
		t.Fatal("unknown router must error, not silently skip")
	}
}

// A step may run any of the tenant's routers, whatever the step is called and
// whichever backend the router reaches — an extension or an LLM.
func TestIntegrationValidateStepRouter(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	httpRouter := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "StepRouters")
	other := testutil.SeedTenant(t, q, "OtherTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "o@a.com", "pass", "owner")
	extTok := testutil.SeedAIVendorToken(t, q, tenant.ID, "ext:mockqe", "Default", "enc")
	llmTok := testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "Default", "enc")
	sysPrompt, err := q.CreateAIPrompt(ctx, db.CreateAIPromptParams{TenantID: tenant.ID, Name: "QE prompt", PromptText: "review", OutStatus: true})
	if err != nil {
		t.Fatal(err)
	}
	newEngine := func(name string, tokenID int64, model string, promptID *int64) int64 {
		t.Helper()
		id := seedEngine(t, q, tenant.ID, name, aiengine.PurposeQEAPE)
		if _, err := q.CreateAIEngineRow(ctx, db.CreateAIEngineRowParams{
			EngineID: id, SourceLang: langcode.Wildcard, TargetLangs: []string{langcode.Wildcard},
			TokenID: &tokenID, Model: model, Settings: []byte("{}"), SystemPromptID: promptID,
		}); err != nil {
			t.Fatalf("create row for %q: %v", name, err)
		}
		return id
	}
	extEngine := newEngine("Extension reviewer", extTok.ID, "mockqe", nil)
	llmEngine := newEngine("LLM reviewer", llmTok.ID, "gpt-4o", &sysPrompt.ID)

	extDefault := testutil.SeedAIRouter(t, q, tenant.ID, "Ext default", &extEngine).ID
	llmOnly := testutil.SeedAIRouter(t, q, tenant.ID, "LLM only", &llmEngine).ID
	otherEngine := seedEngine(t, q, other.ID, "Other reviewer", aiengine.PurposeQEAPE)
	otherRouter := testutil.SeedAIRouter(t, q, other.ID, "Other router", &otherEngine).ID

	t.Run("any of the tenant's routers is accepted", func(t *testing.T) {
		for _, rt := range []int64{extDefault, llmOnly} {
			if msg := airouter.ValidateStepRouter(ctx, q, tenant.ID, &rt); msg != "" {
				t.Errorf("router %d should be accepted, got %q", rt, msg)
			}
		}
	})
	t.Run("plain human step is always valid", func(t *testing.T) {
		if msg := airouter.ValidateStepRouter(ctx, q, tenant.ID, nil); msg != "" {
			t.Errorf("a step with no router should be valid, got %q", msg)
		}
	})
	t.Run("unknown router is refused", func(t *testing.T) {
		bogus := int64(999999)
		if msg := airouter.ValidateStepRouter(ctx, q, tenant.ID, &bogus); !strings.Contains(msg, "not found") {
			t.Errorf("want not-found, got %q", msg)
		}
	})
	t.Run("another tenant's router is refused", func(t *testing.T) {
		if msg := airouter.ValidateStepRouter(ctx, q, tenant.ID, &otherRouter); !strings.Contains(msg, "not found") {
			t.Errorf("want not-found for another tenant's router, got %q", msg)
		}
	})

	// A router may start reaching an extension engine while a step with any
	// name sits on it.
	t.Run("router save may add an extension engine under any step name", func(t *testing.T) {
		testutil.SeedStepDefinition(t, q, tenant.ID, "Proofread", "hybrid", "", &llmOnly)
		rec := testutil.DoRequestWithRole(t, httpRouter, "PUT", fmt.Sprintf("/api/ai/routers/%d", llmOnly), map[string]any{
			"name": "LLM only", "default_engine_id": llmEngine,
			"rules": []map[string]any{{"target_lang": "fr", "engine_id": extEngine}},
		}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		if rules, _ := q.ListAIRouterRules(ctx, llmOnly); len(rules) != 1 || rules[0].EngineID != extEngine {
			t.Fatalf("rules = %+v, want one rule to the extension engine", rules)
		}
	})
}
