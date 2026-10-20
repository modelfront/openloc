package mcp_test

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

type cfgMTEngineOut struct {
	MTEngineID int64          `json:"mt_engine_id"`
	Name       string         `json:"name"`
	EngineType string         `json:"engine_type"`
	Enabled    bool           `json:"enabled"`
	Settings   map[string]any `json:"settings"`
}

func TestIntegrationMCPConfigMTEngines(t *testing.T) {
	c := newConfigFixture(t)
	ctx := context.Background()

	// A project manager creates MT engines, but only of a
	// type that needs no key from them.
	var eng cfgMTEngineOut
	if isErr, text := callTool(t, c.asManager, "openloc_create_mt_engine", map[string]any{
		"name": "Cfg mock", "engine_type": "mock",
	}, &eng); isErr {
		t.Fatalf("create_mt_engine: %s", text)
	}
	row, err := c.q.GetMTEngine(ctx, db.GetMTEngineParams{ID: eng.MTEngineID, TenantID: c.tenantA.ID})
	if err != nil || row.Name != "Cfg mock" || !row.Enabled || row.ApiKey != nil || row.TokenID != nil {
		t.Fatalf("engine row = %+v (err %v), want an enabled keyless mock", row, err)
	}
	cfgMustRefuse(t, c.asManager, "openloc_create_mt_engine", map[string]any{"name": "DeepL", "engine_type": "deepl"}, "AI settings")
	cfgMustRefuse(t, c.asReadOnly, "openloc_create_mt_engine", map[string]any{"name": "RO", "engine_type": "mock"}, "write access")

	var catalog struct {
		Types []struct {
			EngineType string `json:"engine_type"`
			NeedsKey   bool   `json:"needs_key"`
		} `json:"types"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_get_mt_engine_catalog", map[string]any{}, &catalog); isErr {
		t.Fatalf("catalog: %s", text)
	}
	needsKey := map[string]bool{}
	for _, ty := range catalog.Types {
		needsKey[ty.EngineType] = ty.NeedsKey
	}
	if k, ok := needsKey["mock"]; !ok || k {
		t.Errorf("catalog should offer mock without a key: %+v", catalog.Types)
	}
	if !needsKey["deepl"] {
		t.Errorf("catalog should mark deepl as needing a key: %+v", catalog.Types)
	}

	var langs struct {
		Targets []struct {
			Code string `json:"code"`
		} `json:"targets"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_list_mt_languages", map[string]any{"mt_engine_id": eng.MTEngineID}, &langs); isErr {
		t.Fatalf("list_mt_languages: %s", text)
	}
	if len(langs.Targets) == 0 {
		t.Error("the mock engine should report languages")
	}

	c.refresh(t)
	// A router uses the engine, so the update preview says so and the delete is refused.
	routerID := testutil.SeedMTRouter(t, c.q, c.tenantA.ID, "Cfg MT router", &eng.MTEngineID, nil)
	g := cfgPreviewOnly(t, c.asManager, "openloc_update_mt_engine", map[string]any{
		"mt_engine_id": eng.MTEngineID, "name": "Cfg mock renamed", "enabled": false,
	})
	if !strings.Contains(g.Preview.Summary, "Cfg mock") || !cfgEffectsContain(g, "1 MT router") || !cfgEffectsContain(g, "stops machine translation") {
		t.Errorf("update preview should name the engine, the router and the switch-off: %+v", g.Preview)
	}
	if row, _ := c.q.GetMTEngine(ctx, db.GetMTEngineParams{ID: eng.MTEngineID, TenantID: c.tenantA.ID}); row.Name != "Cfg mock" || !row.Enabled {
		t.Fatalf("the preview changed the engine: %+v", row)
	}
	var updated cfgMTEngineOut
	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_mt_engine", map[string]any{
		"mt_engine_id": eng.MTEngineID, "name": "Cfg mock renamed", "enabled": false,
	}, &updated); isErr {
		t.Fatalf("update confirm: %s", text)
	}
	row, _ = c.q.GetMTEngine(ctx, db.GetMTEngineParams{ID: eng.MTEngineID, TenantID: c.tenantA.ID})
	if row.Name != "Cfg mock renamed" || row.Enabled || updated.Name != "Cfg mock renamed" {
		t.Errorf("after confirm row = %+v, result = %+v", row, updated)
	}
	if row.EngineType != "mock" || row.ApiUrl != nil {
		t.Errorf("an update must not touch type or endpoint: %+v", row)
	}
	cfgMustRefuse(t, c.asManager, "openloc_delete_mt_engine", map[string]any{"mt_engine_id": eng.MTEngineID}, "MT router(s) still use")

	c.refresh(t)
	// Another tenant sees nothing.
	cfgMustNotFind(t, c.asOwnerB, "openloc_update_mt_engine", map[string]any{"mt_engine_id": eng.MTEngineID, "name": "stolen"})
	cfgMustNotFind(t, c.asOwnerB, "openloc_delete_mt_engine", map[string]any{"mt_engine_id": eng.MTEngineID})
	cfgMustNotFind(t, c.asOwnerB, "openloc_list_mt_languages", map[string]any{"mt_engine_id": eng.MTEngineID})

	// Once the router is gone the delete runs, and it is a soft delete.
	if isErr, text := confirmedCall(t, c.asManager, "openloc_delete_mt_router", map[string]any{"mt_router_id": routerID}, nil); isErr {
		t.Fatalf("delete_mt_router: %s", text)
	}
	cfgPreviewOnly(t, c.asManager, "openloc_delete_mt_engine", map[string]any{"mt_engine_id": eng.MTEngineID})
	if c.softDeleted(t, "mt_engines", eng.MTEngineID) {
		t.Fatal("the delete preview deleted the engine")
	}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_delete_mt_engine", map[string]any{"mt_engine_id": eng.MTEngineID}, nil); isErr {
		t.Fatalf("delete_mt_engine: %s", text)
	}
	if !c.softDeleted(t, "mt_engines", eng.MTEngineID) {
		t.Error("the engine row should remain with deleted_at set")
	}
	var list struct {
		Engines []cfgMTEngineOut `json:"engines"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_list_mt_engines", map[string]any{}, &list); isErr {
		t.Fatalf("list_mt_engines: %s", text)
	}
	for _, e := range list.Engines {
		if e.MTEngineID == eng.MTEngineID {
			t.Error("a deleted engine is still listed")
		}
	}
}

func TestIntegrationMCPConfigMTRouters(t *testing.T) {
	c := newConfigFixture(t)
	ctx := context.Background()
	e1 := testutil.SeedMTEngine(t, c.q, c.tenantA.ID, "Cfg MT one", "mock")
	e2 := testutil.SeedMTEngine(t, c.q, c.tenantA.ID, "Cfg MT two", "mock")
	routerID := testutil.SeedMTRouterForProfile(t, c.q, c.tenantA.ID, c.projectA.SettingsProfileID, &e1.ID, nil)

	var got struct {
		MTRouterID        int64 `json:"mt_router_id"`
		DefaultMTEngineID int64 `json:"default_mt_engine_id"`
		UsedBy            int64 `json:"used_by"`
		Rules             []struct {
			TargetLanguage string `json:"target_language"`
			MTEngineID     int64  `json:"mt_engine_id"`
			EngineName     string `json:"engine_name"`
		} `json:"rules"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_get_mt_router", map[string]any{"mt_router_id": routerID}, &got); isErr {
		t.Fatalf("get_mt_router: %s", text)
	}
	if got.DefaultMTEngineID != e1.ID || got.UsedBy != 1 {
		t.Errorf("get_mt_router = %+v", got)
	}

	// A project manager edits MT routers.
	update := map[string]any{
		"mt_router_id": routerID, "name": "Cfg MT routed", "default_mt_engine_id": e1.ID,
		"rules": []map[string]any{{"target_language": "de", "mt_engine_id": e2.ID}},
	}
	g := cfgPreviewOnly(t, c.asManager, "openloc_update_mt_router", update)
	if !cfgEffectsContain(g, "1 settings profile") {
		t.Errorf("preview should count the project using the router: %+v", g.Preview)
	}
	if rules, _ := c.q.ListMTRouterRules(ctx, routerID); len(rules) != 0 {
		t.Fatalf("the preview wrote rules: %+v", rules)
	}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_mt_router", update, &got); isErr {
		t.Fatalf("update_mt_router: %s", text)
	}
	if len(got.Rules) != 1 || got.Rules[0].MTEngineID != e2.ID || got.Rules[0].EngineName != "Cfg MT two" {
		t.Errorf("after update = %+v", got)
	}
	cfgMustRefuse(t, c.asManager, "openloc_update_mt_router", map[string]any{"mt_router_id": routerID, "name": "Empty"}, "at least one route")
	cfgMustRefuse(t, c.asManager, "openloc_delete_mt_router", map[string]any{"mt_router_id": routerID}, "still use this MT router")
	cfgMustNotFind(t, c.asOwnerB, "openloc_get_mt_router", map[string]any{"mt_router_id": routerID})
	cfgMustNotFind(t, c.asOwnerB, "openloc_delete_mt_router", map[string]any{"mt_router_id": routerID})

	// Tenant B's update routes to its own engine, so only the router's tenant
	// check can refuse it; tenant A previews the same call on its own engine.
	engineB := testutil.SeedMTEngine(t, c.q, c.tenantB.ID, "Cfg MT theirs", "mock")
	takeover := func(engineID int64) map[string]any {
		return map[string]any{"mt_router_id": routerID, "name": "Cfg MT stolen", "default_mt_engine_id": engineID}
	}
	cfgMustNotFind(t, c.asOwnerB, "openloc_update_mt_router", takeover(engineB.ID))
	rt, err := c.q.GetMTRouter(ctx, db.GetMTRouterParams{ID: routerID, TenantID: c.tenantA.ID})
	if err != nil {
		t.Fatalf("read router: %v", err)
	}
	rules, err := c.q.ListMTRouterRules(ctx, routerID)
	if err != nil || rt.Name != "Cfg MT routed" || ptr.Deref(rt.DefaultMtEngineID) != e1.ID || len(rules) != 1 {
		t.Errorf("tenant B's update changed tenant A's router: %+v rules %+v (err %v)", rt, rules, err)
	}
	cfgPreviewOnly(t, c.asManager, "openloc_update_mt_router", takeover(e2.ID))
}
