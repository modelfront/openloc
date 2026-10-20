package mcp_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type mtEngineUse struct {
	Engine    string   `json:"engine"`
	Languages []string `json:"languages"`
}

type projectMTRow struct {
	ID           int64         `json:"id"`
	MTRouterName string        `json:"mt_router_name"`
	MTEngines    []mtEngineUse `json:"mt_engines"`
	MTNoEngine   []string      `json:"mt_no_engine"`
}

// Every project row says which MT router it translates through and which
// engine each target language goes to — the router's rule for the language
// (a rule for "fr" covers fr-ca), else its default — so "which engines do my
// projects use?" is one list_projects call.
func TestIntegrationMCPProjectRowsCarryMTEngines(t *testing.T) {
	f := newFixture(t)

	deepl := testutil.SeedMTEngine(t, f.q, f.tenantA.ID, "DeepL Pro", "deepl")
	google := testutil.SeedMTEngine(t, f.q, f.tenantA.ID, "Google NMT", "google")
	llm := testutil.SeedAIEngine(t, f.q, f.tenantA.ID, "Claude MT", "mt")

	// Rules plus a default.
	main := testutil.SeedProject(t, f.q, f.tenantA.ID, "Main", "en", []string{"de", "fr-ca", "ja", "pt-br"})
	mainRouter := testutil.SeedMTRouter(t, f.q, f.tenantA.ID, "Main MT", &deepl.ID, nil)
	testutil.SeedMTRouterRule(t, f.q, mainRouter, "fr", &google.ID, nil)
	testutil.SeedMTRouterRule(t, f.q, mainRouter, "ja", nil, &llm.ID)
	testutil.SetProfileMTRouter(t, f.q, main.SettingsProfileID, &mainRouter)

	// Rules only: a language with no rule has no engine.
	rulesOnly := testutil.SeedProject(t, f.q, f.tenantA.ID, "Rules only", "en", []string{"es", "it"})
	rulesRouter := testutil.SeedMTRouter(t, f.q, f.tenantA.ID, "Spanish only", nil, nil)
	testutil.SeedMTRouterRule(t, f.q, rulesRouter, "es", &google.ID, nil)
	testutil.SetProfileMTRouter(t, f.q, rulesOnly.SettingsProfileID, &rulesRouter)

	// Another tenant's router never shows on this tenant's project, even if
	// the profile points at it.
	bravoEngine := testutil.SeedMTEngine(t, f.q, f.tenantB.ID, "Bravo Engine", "deepl")
	bravoRouter := testutil.SeedMTRouter(t, f.q, f.tenantB.ID, "Bravo MT", &bravoEngine.ID, nil)
	testutil.SetProfileMTRouter(t, f.q, f.projectA.SettingsProfileID, &bravoRouter)

	want := map[int64]projectMTRow{
		main.ID: {MTRouterName: "Main MT", MTEngines: []mtEngineUse{
			{Engine: "DeepL Pro", Languages: []string{"de", "pt-br"}},
			{Engine: "Google NMT", Languages: []string{"fr-ca"}},
			{Engine: "Claude MT", Languages: []string{"ja"}},
		}},
		rulesOnly.ID: {MTRouterName: "Spanish only",
			MTEngines:  []mtEngineUse{{Engine: "Google NMT", Languages: []string{"es"}}},
			MTNoEngine: []string{"it"}},
		f.projectA.ID: {MTEngines: []mtEngineUse{}, MTNoEngine: []string{"de"}},
	}
	check := func(where string, got projectMTRow) {
		t.Helper()
		w, ok := want[got.ID]
		if !ok {
			t.Fatalf("%s: unexpected project %d", where, got.ID)
		}
		w.ID = got.ID
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s project %d MT = %+v, want %+v", where, got.ID, got, w)
		}
	}

	s := connect(t, f.url, f.tokenA)
	var list struct {
		Projects []projectMTRow `json:"projects"`
	}
	isErr, text := callTool(t, s, "openloc_list_projects", map[string]any{}, &list)
	if isErr {
		t.Fatalf("list_projects: %s", text)
	}
	if strings.Contains(text, "Bravo") {
		t.Errorf("tenant B's router or engine leaked into tenant A's list: %s", text)
	}
	if len(list.Projects) != len(want) {
		t.Fatalf("projects = %+v, want %d", list.Projects, len(want))
	}
	for _, p := range list.Projects {
		check("list_projects", p)
	}

	for id := range want {
		var one projectMTRow
		if isErr, text := callTool(t, s, "openloc_get_project", map[string]any{"project_id": id}, &one); isErr {
			t.Fatalf("get_project %d: %s", id, text)
		}
		check("get_project", one)
	}
}

// A retired router still routes its projects, but an engine that has been
// deleted translates nothing: the pipeline skips it, so the project rows list
// its languages under mt_no_engine instead of naming it. All four places a
// router names an engine are covered: the default MT engine, the default AI
// engine, a rule's MT engine and a rule's AI engine.
func TestIntegrationMCPProjectRowsSkipDeletedEngines(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	kept := testutil.SeedMTEngine(t, f.q, f.tenantA.ID, "Kept engine", "deepl")
	goneDefault := testutil.SeedMTEngine(t, f.q, f.tenantA.ID, "Deleted engine", "google")
	goneRule := testutil.SeedMTEngine(t, f.q, f.tenantA.ID, "Deleted rule engine", "deepl")
	goneRuleLLM := testutil.SeedAIEngine(t, f.q, f.tenantA.ID, "Deleted LLM", "mt")
	goneDefaultLLM := testutil.SeedAIEngine(t, f.q, f.tenantA.ID, "Deleted default LLM", "mt")

	// The default MT engine and a rule's AI engine are deleted.
	mtDefault := testutil.SeedProject(t, f.q, f.tenantA.ID, "Retired MT router", "en", []string{"de", "fr", "ja"})
	mtRouter := testutil.SeedMTRouter(t, f.q, f.tenantA.ID, "Old MT", &goneDefault.ID, nil)
	testutil.SeedMTRouterRule(t, f.q, mtRouter, "de", &kept.ID, nil)
	testutil.SeedMTRouterRule(t, f.q, mtRouter, "ja", nil, &goneRuleLLM.ID)
	testutil.SetProfileMTRouter(t, f.q, mtDefault.SettingsProfileID, &mtRouter)

	// The default AI engine and a rule's MT engine are deleted.
	aiDefault := testutil.SeedProject(t, f.q, f.tenantA.ID, "Retired AI router", "en", []string{"es", "it"})
	aiRouter := testutil.SeedMTRouter(t, f.q, f.tenantA.ID, "Old AI", nil, &goneDefaultLLM.ID)
	testutil.SeedMTRouterRule(t, f.q, aiRouter, "es", &goneRule.ID, nil)
	testutil.SetProfileMTRouter(t, f.q, aiDefault.SettingsProfileID, &aiRouter)

	// Retire the routers, then delete the engines only they still point at.
	for _, id := range []int64{mtRouter, aiRouter} {
		if err := f.q.SoftDeleteMTRouter(ctx, db.SoftDeleteMTRouterParams{ID: id, TenantID: f.tenantA.ID}); err != nil {
			t.Fatalf("retire router %d: %v", id, err)
		}
	}
	for _, id := range []int64{goneDefault.ID, goneRule.ID} {
		if err := f.q.SoftDeleteMTEngine(ctx, db.SoftDeleteMTEngineParams{ID: id, TenantID: f.tenantA.ID}); err != nil {
			t.Fatalf("delete MT engine %d: %v", id, err)
		}
	}
	for _, id := range []int64{goneRuleLLM.ID, goneDefaultLLM.ID} {
		if err := f.q.SoftDeleteAIEngine(ctx, db.SoftDeleteAIEngineParams{ID: id, TenantID: f.tenantA.ID}); err != nil {
			t.Fatalf("delete AI engine %d: %v", id, err)
		}
	}

	s := connect(t, f.url, f.tokenA)
	for _, want := range []projectMTRow{
		{ID: mtDefault.ID, MTRouterName: "Old MT",
			MTEngines:  []mtEngineUse{{Engine: "Kept engine", Languages: []string{"de"}}},
			MTNoEngine: []string{"fr", "ja"}},
		{ID: aiDefault.ID, MTRouterName: "Old AI",
			MTEngines:  []mtEngineUse{},
			MTNoEngine: []string{"es", "it"}},
	} {
		var got projectMTRow
		isErr, text := callTool(t, s, "openloc_get_project", map[string]any{"project_id": want.ID}, &got)
		if isErr {
			t.Fatalf("get_project %d: %s", want.ID, text)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("project %d MT = %+v, want %+v", want.ID, got, want)
		}
		if strings.Contains(text, "Deleted") {
			t.Errorf("project %d: a deleted engine is still named: %s", want.ID, text)
		}
	}
}
