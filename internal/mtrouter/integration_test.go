// MT router CRUD, validation, role gating, tenant isolation, the delete
// guards (router ← profile, MT engine ← router), the project settings
// `mt_router_id` tri-state and runtime resolution.
package mtrouter_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/aiengine"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/mtrouter"
	"openloc/internal/secrets"
	"openloc/internal/testutil"
)

type ruleJSON struct {
	ID         int64  `json:"id"`
	TargetLang string `json:"target_lang"`
	MTEngineID *int64 `json:"mt_engine_id"`
	AIEngineID *int64 `json:"ai_engine_id"`
}

type routerJSON struct {
	ID                int64      `json:"id"`
	TenantID          int64      `json:"tenant_id"`
	Name              string     `json:"name"`
	DefaultMtEngineID *int64     `json:"default_mt_engine_id"`
	DefaultAiEngineID *int64     `json:"default_ai_engine_id"`
	Rules             []ruleJSON `json:"rules"`
}

type routerResp struct {
	Data routerJSON `json:"data"`
}

type routerListResp struct {
	Data []routerJSON `json:"data"`
}

func seedAIEngine(t *testing.T, q db.Querier, tenantID int64, name, purpose string) int64 {
	t.Helper()
	e, err := q.CreateAIEngine(context.Background(), db.CreateAIEngineParams{TenantID: tenantID, Name: name, Purpose: purpose})
	if err != nil {
		t.Fatalf("seed AI engine: %v", err)
	}
	return e.ID
}

func ptrEq(p *int64, want int64) bool { return p != nil && *p == want }

func TestIntegrationMTRouterCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "A")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@a.com", "pass", "owner")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@a.com", "pass", "project_manager")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "lin@a.com", "pass", "linguist")
	deepl := testutil.SeedMTEngine(t, q, tenant.ID, "DeepL", "deepl").ID
	google := testutil.SeedMTEngine(t, q, tenant.ID, "Google", "google").ID
	llmMT := seedAIEngine(t, q, tenant.ID, "LLM translator", aiengine.PurposeMT)
	reviewer := seedAIEngine(t, q, tenant.ID, "Reviewer", aiengine.PurposeQEAPE)
	other := testutil.SeedTenant(t, q, "B")
	foreignMT := testutil.SeedMTEngine(t, q, other.ID, "B's DeepL", "deepl").ID
	foreignAI := seedAIEngine(t, q, other.ID, "B's LLM", aiengine.PurposeMT)

	post := func(body map[string]any, userID int64, role string) (int, string, *routerResp) {
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-routers", body, userID, tenant.ID, role)
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
	mtRef := func(id int64) map[string]any { return map[string]any{"mt_engine_id": id} }
	aiRef := func(id int64) map[string]any { return map[string]any{"ai_engine_id": id} }
	rule := func(lang string, ref map[string]any) map[string]any {
		r := map[string]any{"target_lang": lang}
		for k, v := range ref {
			r[k] = v
		}
		return r
	}

	// Validation: exactly one engine per ref, every engine this tenant's, an
	// AI engine must translate (purpose 'mt'), a language appears once.
	expectBad(map[string]any{"name": "", "default": mtRef(deepl)}, "name is required")
	expectBad(map[string]any{"name": strings.Repeat("x", 201), "default": mtRef(deepl)}, "name too long")
	expectBad(map[string]any{"name": "Empty", "rules": []any{}}, "at least one route")
	expectBad(map[string]any{"name": "X", "default": map[string]any{"mt_engine_id": deepl, "ai_engine_id": llmMT}}, "not both")
	expectBad(map[string]any{"name": "X", "default": aiRef(reviewer)}, "translation engine")
	expectBad(map[string]any{"name": "X", "default": mtRef(foreignMT)}, "not found")
	expectBad(map[string]any{"name": "X", "default": aiRef(foreignAI)}, "not found")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{rule("fr", mtRef(foreignMT))}}, "not found")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{rule("fr", aiRef(reviewer))}}, "translation engine")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{rule("fr", map[string]any{"mt_engine_id": deepl, "ai_engine_id": llmMT})}}, "not both")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{{"target_lang": "fr"}}}, "an engine is required")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{rule("", mtRef(deepl))}}, "target language is required")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{rule("*", mtRef(deepl))}}, "target language is required")
	expectBad(map[string]any{"name": "X", "rules": []map[string]any{rule("fr", mtRef(deepl)), rule("FR", mtRef(google))}}, "listed twice")
	if n, err := q.ListMTRouters(ctx, tenant.ID); err != nil || len(n) != 0 {
		t.Fatalf("refused creates must not leave routers behind: %v / %d", err, len(n))
	}

	// PM creates a router with a default and two rules (one classic, one
	// LLM); rules come back normalized and in order. This surface is
	// manager-level, unlike AI routers.
	code, body, created := post(map[string]any{
		"name": " Production ", "default": mtRef(deepl),
		"rules": []map[string]any{rule("DE", mtRef(google)), rule("fr-CA", aiRef(llmMT))},
	}, pm.ID, "project_manager")
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	if created.Data.Name != "Production" || created.Data.TenantID != tenant.ID || !ptrEq(created.Data.DefaultMtEngineID, deepl) || created.Data.DefaultAiEngineID != nil {
		t.Fatalf("unexpected router: %+v", created.Data)
	}
	if len(created.Data.Rules) != 2 ||
		created.Data.Rules[0].TargetLang != "de" || !ptrEq(created.Data.Rules[0].MTEngineID, google) || created.Data.Rules[0].AIEngineID != nil ||
		created.Data.Rules[1].TargetLang != "fr-ca" || !ptrEq(created.Data.Rules[1].AIEngineID, llmMT) || created.Data.Rules[1].MTEngineID != nil {
		t.Fatalf("unexpected rules: %+v", created.Data.Rules)
	}
	// A rules-only router (no default) and an LLM-default router are legal.
	if code, body, _ := post(map[string]any{"name": "Rules only", "rules": []map[string]any{rule("it", mtRef(google))}}, owner.ID, "owner"); code != http.StatusCreated {
		t.Fatalf("rules-only router: %d %s", code, body)
	}
	if code, body, _ := post(map[string]any{"name": "LLM default", "default": aiRef(llmMT)}, owner.ID, "owner"); code != http.StatusCreated {
		t.Fatalf("LLM-default router: %d %s", code, body)
	}

	// Duplicate name (case-insensitive) → 409.
	if code, _, _ := post(map[string]any{"name": "production", "default": mtRef(deepl)}, owner.ID, "owner"); code != http.StatusConflict {
		t.Fatalf("duplicate name: want 409, got %d", code)
	}

	// Role gate: managers do everything, linguists nothing.
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/mt-routers", nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list routerListResp
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 3 || list.Data[0].Name != "LLM default" || list.Data[1].Name != "Production" || len(list.Data[1].Rules) != 2 || list.Data[2].Name != "Rules only" {
		t.Fatalf("list should carry all routers, sorted by name, with rules: %+v", list.Data)
	}
	for _, tc := range []struct {
		method, path string
		body         map[string]any
	}{
		{"GET", "/api/mt-routers", nil},
		{"GET", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), nil},
		{"POST", "/api/mt-routers", map[string]any{"name": "Linguist made", "default": mtRef(deepl)}},
		{"PUT", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), map[string]any{"name": "Nope", "default": mtRef(deepl)}},
		{"DELETE", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), nil},
	} {
		rec := testutil.DoRequestWithRole(t, router, tc.method, tc.path, tc.body, linguist.ID, tenant.ID, "linguist")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("linguist %s %s: want 403, got %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// GET one carries the rules.
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got routerResp
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.ID != created.Data.ID || len(got.Data.Rules) != 2 {
		t.Fatalf("GET one: %+v", got.Data)
	}

	// Update replaces name, default and the whole rule set — as a PM.
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), map[string]any{
		"name": "Production v2", "default": aiRef(llmMT),
		"rules": []map[string]any{rule("es", mtRef(deepl))},
	}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var updated routerResp
	testutil.DecodeJSON(t, rec, &updated)
	if updated.Data.Name != "Production v2" || updated.Data.DefaultMtEngineID != nil || !ptrEq(updated.Data.DefaultAiEngineID, llmMT) ||
		len(updated.Data.Rules) != 1 || updated.Data.Rules[0].TargetLang != "es" || !ptrEq(updated.Data.Rules[0].MTEngineID, deepl) {
		t.Fatalf("update not applied: %+v", updated.Data)
	}
	rules, err := q.ListMTRouterRules(ctx, created.Data.ID)
	if err != nil || len(rules) != 1 {
		t.Fatalf("old rules must be gone: %v / %d", err, len(rules))
	}
	// A bad update leaves the router untouched: name, default and rules.
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), map[string]any{
		"name": "Broken", "default": mtRef(google),
		"rules": []map[string]any{rule("es", aiRef(reviewer))},
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	rt, err := q.GetMTRouter(ctx, db.GetMTRouterParams{ID: created.Data.ID, TenantID: tenant.ID})
	if err != nil || rt.Name != "Production v2" || !ptrEq(rt.DefaultAiEngineID, llmMT) {
		t.Fatalf("refused update must not change the router: %v %+v", err, rt)
	}
	if rules, _ := q.ListMTRouterRules(ctx, created.Data.ID); len(rules) != 1 || rules[0].TargetLang != "es" {
		t.Fatalf("refused update must not change the rules: %+v", rules)
	}
	// Renaming onto another live router's name → 409, untouched.
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), map[string]any{
		"name": "RULES ONLY", "default": aiRef(llmMT),
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if rt, _ := q.GetMTRouter(ctx, db.GetMTRouterParams{ID: created.Data.ID, TenantID: tenant.ID}); rt.Name != "Production v2" {
		t.Fatalf("409 rename must not apply: %q", rt.Name)
	}

	// Delete guard: a project profile pointing at the router → 409 with the
	// count; a template profile counts too; once both are detached the
	// delete goes through, the router leaves the list, and the row survives
	// for runtime lookups.
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"de"})
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "T")
	for _, profileID := range []int64{project.SettingsProfileID, tmpl.SettingsProfileID} {
		testutil.SetProfileMTRouter(t, q, profileID, &created.Data.ID)
	}
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "2 project(s)/template(s)") {
		t.Errorf("409 should count the profiles, got %s", rec.Body.String())
	}
	testutil.SetProfileMTRouter(t, q, project.SettingsProfileID, nil)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	testutil.SetProfileMTRouter(t, q, tmpl.SettingsProfileID, nil)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/mt-routers", nil, owner.ID, tenant.ID, "owner")
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 2 || list.Data[0].Name != "LLM default" || list.Data[1].Name != "Rules only" {
		t.Fatalf("deleted router still listed: %+v", list.Data)
	}
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/mt-routers/%d", created.Data.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if _, err := q.GetMTRouterAny(ctx, db.GetMTRouterAnyParams{ID: created.Data.ID, TenantID: tenant.ID}); err != nil {
		t.Fatalf("soft-deleted router must still resolve for runtime lookups: %v", err)
	}
	// The retired router's name is free again.
	if code, body, _ := post(map[string]any{"name": "production v2", "default": mtRef(deepl)}, owner.ID, "owner"); code != http.StatusCreated {
		t.Fatalf("name of a retired router must be reusable: %d %s", code, body)
	}
}

// MT engine delete is refused while a live router references the engine as
// default or in a rule; a retired router does not count.
func TestIntegrationMTRouterGuardsMTEngineDelete(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "EngineGuard")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "o@g.com", "pass", "owner")
	engine := testutil.SeedMTEngine(t, q, tenant.ID, "DeepL", "deepl").ID
	otherEngine := testutil.SeedMTEngine(t, q, tenant.ID, "Google", "google").ID

	del := func() int {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/mt-engines/%d", engine), nil, owner.ID, tenant.ID, "owner")
		if rec.Code == http.StatusConflict && !strings.Contains(rec.Body.String(), "MT router(s) still use this MT engine") {
			t.Fatalf("409 should name the routers, got %s", rec.Body.String())
		}
		return rec.Code
	}

	asDefault := testutil.SeedMTRouter(t, q, tenant.ID, "as default", &engine, nil)
	if code := del(); code != http.StatusConflict {
		t.Fatalf("engine used as router default: want 409, got %d", code)
	}
	if err := q.SoftDeleteMTRouter(ctx, db.SoftDeleteMTRouterParams{ID: asDefault, TenantID: tenant.ID}); err != nil {
		t.Fatal(err)
	}
	inRule := testutil.SeedMTRouter(t, q, tenant.ID, "in a rule", &otherEngine, nil)
	testutil.SeedMTRouterRule(t, q, inRule, "fr", &engine, nil)
	if code := del(); code != http.StatusConflict {
		t.Fatalf("engine used in a router rule: want 409, got %d", code)
	}
	if err := q.SoftDeleteMTRouter(ctx, db.SoftDeleteMTRouterParams{ID: inRule, TenantID: tenant.ID}); err != nil {
		t.Fatal(err)
	}
	if code := del(); code != http.StatusNoContent {
		t.Fatalf("engine with only retired routers: want 204, got %d", code)
	}
	if _, err := q.GetMTEngine(ctx, db.GetMTEngineParams{ID: engine, TenantID: tenant.ID}); err == nil {
		t.Fatal("engine must be gone after 204")
	}
}

func TestIntegrationMTRouterTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "A")
	tenantB := testutil.SeedTenant(t, q, "B")
	adminB := testutil.SeedUserWithRole(t, q, tenantB.ID, "b@b.com", "pass", "admin")
	engineA := testutil.SeedMTEngine(t, q, tenantA.ID, "A's DeepL", "deepl").ID
	engineB := testutil.SeedMTEngine(t, q, tenantB.ID, "B's DeepL", "deepl").ID
	routerA := testutil.SeedMTRouter(t, q, tenantA.ID, "A's router", &engineA, nil)

	for _, tc := range []struct{ method, path string }{
		{"GET", fmt.Sprintf("/api/mt-routers/%d", routerA)},
		{"PUT", fmt.Sprintf("/api/mt-routers/%d", routerA)},
		{"DELETE", fmt.Sprintf("/api/mt-routers/%d", routerA)},
	} {
		var body map[string]any
		if tc.method == "PUT" {
			body = map[string]any{"name": "Stolen", "default": map[string]any{"mt_engine_id": engineB}}
		}
		rec := testutil.DoRequestWithRole(t, router, tc.method, tc.path, body, adminB.ID, tenantB.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	}
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/mt-routers", nil, adminB.ID, tenantB.ID, "admin")
	var list routerListResp
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 0 {
		t.Fatalf("tenant B sees %d of A's routers", len(list.Data))
	}
	if rt, err := q.GetMTRouter(ctx, db.GetMTRouterParams{ID: routerA, TenantID: tenantA.ID}); err != nil || rt.Name != "A's router" || !ptrEq(rt.DefaultMtEngineID, engineA) {
		t.Fatalf("A's router should survive B's attempts: %v %+v", err, rt)
	}
	// The runtime resolver is tenant-scoped too.
	if _, err := mtrouter.ResolveEngine(ctx, q, tenantB.ID, routerA, "fr"); err == nil {
		t.Fatal("resolving A's router under tenant B must fail")
	}
	// B cannot point its own project at A's router.
	projectB := testutil.SeedProject(t, q, tenantB.ID, "B's project", "en", []string{"fr"})
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", projectB.ID), map[string]any{"mt_router_id": routerA}, adminB.ID, tenantB.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if p, _ := q.GetSettingsProfile(ctx, projectB.SettingsProfileID); p.MtRouterID != nil {
		t.Fatalf("refused PATCH must not store A's router, got %d", *p.MtRouterID)
	}
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/projects", map[string]any{
		"name": "B's other project", "source_language": "en", "target_languages": []string{"fr"},
		"settings": map[string]any{"mt_router_id": routerA},
	}, adminB.ID, tenantB.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

// PATCH /api/projects/{id}/settings `mt_router_id` is tri-state: absent
// keeps, N sets (GET shows id + name), null and 0 clear. Create takes the
// same field under `settings`.
func TestIntegrationProjectMTRouterTriState(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "TriState")
	user := testutil.SeedUser(t, q, tenant.ID, "u@t.com", "pass")
	engine := testutil.SeedMTEngine(t, q, tenant.ID, "DeepL", "deepl").ID
	prod := testutil.SeedMTRouter(t, q, tenant.ID, "Prod MT", &engine, nil)
	staging := testutil.SeedMTRouter(t, q, tenant.ID, "Staging MT", &engine, nil)

	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "P", "source_language": "en", "target_languages": []string{"fr"},
		"settings": map[string]any{"mt_router_id": prod},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var createResp struct {
		Data struct{ ID int64 } `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &createResp)
	projectID := createResp.Data.ID

	get := func() (*int64, *string) {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", projectID), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Project struct {
					MTRouterID   *int64  `json:"mt_router_id"`
					MTRouterName *string `json:"mt_router_name"`
				} `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data.Project.MTRouterID, resp.Data.Project.MTRouterName
	}
	patch := func(body map[string]any, want int) {
		t.Helper()
		rec := testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", projectID), body, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, want)
	}

	if id, name := get(); !ptrEq(id, prod) || name == nil || *name != "Prod MT" {
		t.Fatalf("create with settings.mt_router_id: want %d %q, got %v %v", prod, "Prod MT", id, name)
	}
	// Absent keeps.
	patch(map[string]any{"fuzzy_threshold": 0.8}, http.StatusOK)
	if id, _ := get(); !ptrEq(id, prod) {
		t.Fatalf("PATCH without mt_router_id must keep the router, got %v", id)
	}
	// Set swaps.
	patch(map[string]any{"mt_router_id": staging}, http.StatusOK)
	if id, name := get(); !ptrEq(id, staging) || name == nil || *name != "Staging MT" {
		t.Fatalf("after set: want %d %q, got %v %v", staging, "Staging MT", id, name)
	}
	// Garbage is refused, pointer untouched.
	patch(map[string]any{"mt_router_id": "abc"}, http.StatusBadRequest)
	patch(map[string]any{"mt_router_id": -1}, http.StatusBadRequest)
	patch(map[string]any{"mt_router_id": 999999}, http.StatusBadRequest)
	if id, _ := get(); !ptrEq(id, staging) {
		t.Fatalf("refused PATCHes must keep the router, got %v", id)
	}
	// Null clears.
	patch(map[string]any{"mt_router_id": nil}, http.StatusOK)
	if id, name := get(); id != nil || name != nil {
		t.Fatalf("after null: want no router, got %v %v", id, name)
	}
	// 0 clears too (legacy client compat, same as qa_profile_id).
	patch(map[string]any{"mt_router_id": prod}, http.StatusOK)
	patch(map[string]any{"mt_router_id": 0}, http.StatusOK)
	if id, _ := get(); id != nil {
		t.Fatalf("after 0: want no router, got %d", *id)
	}
	// A retired router the project still points at keeps resolving its name
	// so the settings page can show what the project is on.
	patch(map[string]any{"mt_router_id": prod}, http.StatusOK)
	if err := q.SoftDeleteMTRouter(ctx, db.SoftDeleteMTRouterParams{ID: prod, TenantID: tenant.ID}); err != nil {
		t.Fatal(err)
	}
	if id, name := get(); !ptrEq(id, prod) || name == nil || *name != "Prod MT" {
		t.Fatalf("retired router must still show on the project: %v %v", id, name)
	}
	// ...but a retired router cannot be newly assigned.
	patch(map[string]any{"mt_router_id": staging}, http.StatusOK)
	patch(map[string]any{"mt_router_id": prod}, http.StatusBadRequest)
	if id, _ := get(); !ptrEq(id, staging) {
		t.Fatalf("assigning a retired router must be refused and keep the current one, got %v", id)
	}
}

// ResolveEngine: rule for the job's target language (exact, then same base),
// else the default, else an unset ref — and a retired router still resolves
// for jobs already sitting on it.
func TestIntegrationResolveEngine(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "Resolve")
	def := testutil.SeedMTEngine(t, q, tenant.ID, "Default", "deepl").ID
	fr := testutil.SeedMTEngine(t, q, tenant.ID, "French", "deepl").ID
	deDE := seedAIEngine(t, q, tenant.ID, "German (DE) via LLM", aiengine.PurposeMT)
	ptBR := seedAIEngine(t, q, tenant.ID, "Portuguese via LLM", aiengine.PurposeMT)

	withDefault := testutil.SeedMTRouter(t, q, tenant.ID, "With default", &def, nil)
	testutil.SeedMTRouterRule(t, q, withDefault, "fr", &fr, nil)
	testutil.SeedMTRouterRule(t, q, withDefault, "de-de", nil, &deDE)
	noDefault := testutil.SeedMTRouter(t, q, tenant.ID, "No default", nil, nil)
	testutil.SeedMTRouterRule(t, q, noDefault, "fr", &fr, nil)
	llmDefault := testutil.SeedMTRouter(t, q, tenant.ID, "LLM default", nil, &ptBR)

	cases := []struct {
		router int64
		lang   string
		wantMT int64 // 0 = nil
		wantAI int64 // 0 = nil
	}{
		{withDefault, "fr", fr, 0},
		{withDefault, "fr-CA", fr, 0},
		{withDefault, "FR_ca", fr, 0},
		{withDefault, "de-DE", 0, deDE},
		{withDefault, "de", 0, deDE},
		{withDefault, "es", def, 0},
		{noDefault, "fr", fr, 0},
		{noDefault, "es", 0, 0},
		{llmDefault, "pt-BR", 0, ptBR},
	}
	for _, c := range cases {
		got, err := mtrouter.ResolveEngine(ctx, q, tenant.ID, c.router, c.lang)
		if err != nil {
			t.Fatalf("router %d / %s: %v", c.router, c.lang, err)
		}
		switch {
		case c.wantMT == 0 && got.MTEngineID != nil:
			t.Errorf("router %d / %s: want no MT engine, got %d", c.router, c.lang, *got.MTEngineID)
		case c.wantMT != 0 && !ptrEq(got.MTEngineID, c.wantMT):
			t.Errorf("router %d / %s: want MT engine %d, got %v", c.router, c.lang, c.wantMT, got.MTEngineID)
		case c.wantAI == 0 && got.AIEngineID != nil:
			t.Errorf("router %d / %s: want no AI engine, got %d", c.router, c.lang, *got.AIEngineID)
		case c.wantAI != 0 && !ptrEq(got.AIEngineID, c.wantAI):
			t.Errorf("router %d / %s: want AI engine %d, got %v", c.router, c.lang, c.wantAI, got.AIEngineID)
		case c.wantMT == 0 && c.wantAI == 0 && got.IsSet():
			t.Errorf("router %d / %s: want unset ref, got %+v", c.router, c.lang, got)
		}
	}

	if err := q.SoftDeleteMTRouter(ctx, db.SoftDeleteMTRouterParams{ID: withDefault, TenantID: tenant.ID}); err != nil {
		t.Fatal(err)
	}
	if got, err := mtrouter.ResolveEngine(ctx, q, tenant.ID, withDefault, "es"); err != nil || !ptrEq(got.MTEngineID, def) {
		t.Fatalf("a retired router must still resolve for running jobs: %+v %v", got, err)
	}
	if _, err := mtrouter.ResolveEngine(ctx, q, tenant.ID, 999999, "es"); err == nil {
		t.Fatal("unknown router must error, not silently skip")
	}
}

// A rule names one target language and one engine, both known at save time, so
// picking a language the engine does not write is refused in the dialog rather
// than at translate time. Only the target side is checkable: a router routes by
// target and carries no source.
func TestIntegrationMTRouter_RejectsALanguageTheEngineCannotWrite(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	// An engine that publishes en and de, and nothing else.
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"code":"en","name":"English","targets":["de"]},{"code":"de","name":"German","targets":["en"]}]`)
	}))
	defer catalog.Close()

	tenant := testutil.SeedTenant(t, q, "RouterLangCo")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@routerlang.test", "pass", "project_manager")
	url := catalog.URL
	eng, err := q.CreateMTEngine(ctx, db.CreateMTEngineParams{
		Name: "Catalog engine", EngineType: "libretranslate", ApiUrl: &url,
		Enabled: true, TenantID: tenant.ID,
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}

	post := func(targetLang string) (int, string) {
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-routers", map[string]any{
			"name":    "Router " + targetLang,
			"default": map[string]any{},
			"rules":   []map[string]any{{"target_lang": targetLang, "mt_engine_id": eng.ID}},
		}, pm.ID, tenant.ID, "project_manager")
		return rec.Code, rec.Body.String()
	}

	code, body := post("th")
	if code != http.StatusBadRequest {
		t.Fatalf("Thai on an en/de engine: status %d, want 400 (body: %s)", code, body)
	}
	if !strings.Contains(body, "th") {
		t.Errorf("message should name the language: %s", body)
	}

	// The control: a language the engine does write still saves, so the check
	// cannot be passing by refusing everything.
	if code, body := post("de"); code != http.StatusCreated {
		t.Errorf("German on an en/de engine: status %d, want 201 (body: %s)", code, body)
	}
}

// A mock or unreachable engine publishes no catalog of its own, so a rule
// against it is never refused — the same rule ValidateAssignment follows.
func TestIntegrationMTRouter_AcceptsAnyLanguageOnAnEngineWithNoCatalog(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "RouterMockCo")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@routermock.test", "pass", "project_manager")
	mock := testutil.SeedMTEngine(t, q, tenant.ID, "Mock", "mock")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-routers", map[string]any{
		"name":    "Mock router",
		"default": map[string]any{},
		"rules":   []map[string]any{{"target_lang": "th", "mt_engine_id": mock.ID}},
	}, pm.ID, tenant.ID, "project_manager")
	if rec.Code != http.StatusCreated {
		t.Errorf("mock engine has no catalog to refuse on: status %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// Each rule can trigger a catalog fetch against a cold cache, and a failed
// fetch is never cached — so an uncapped rules slice buys one stalled request
// per rule from a single save.
func TestIntegrationMTRouter_CapsTheRuleCount(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "RouterCapCo")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@routercap.test", "pass", "project_manager")
	mock := testutil.SeedMTEngine(t, q, tenant.ID, "Mock", "mock")

	rules := make([]map[string]any, 0, 300)
	for i := range 300 {
		rules = append(rules, map[string]any{"target_lang": fmt.Sprintf("x%d", i), "mt_engine_id": mock.ID})
	}
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-routers", map[string]any{
		"name": "Too many", "default": map[string]any{}, "rules": rules,
	}, pm.ID, tenant.ID, "project_manager")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("300 rules: status %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// A router reports the state of the engines it routes to,
// so every picker that offers one — a project's settings, a template's, the
// workflow step dialog — can warn without loading the engine lists itself.
//
// Computed server-side for exactly that reason: the pickers do not fetch
// engines, and a second client-side implementation is how two surfaces end up
// disagreeing about whether a router works.
func TestIntegrationMTRouterReportsItsEngineHealth(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "HealthTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@health.test", "pass", "owner")
	tok := testutil.SeedAIVendorToken(t, q, tenant.ID, "deepl", "EU", "ENC(dk)")

	good, err := q.CreateMTEngine(ctx, db.CreateMTEngineParams{
		Name: "Good", EngineType: "deepl", Enabled: true, TenantID: tenant.ID, TokenID: &tok.ID,
	})
	if err != nil {
		t.Fatalf("seed good engine: %v", err)
	}
	off, err := q.CreateMTEngine(ctx, db.CreateMTEngineParams{
		Name: "Off", EngineType: "deepl", Enabled: false, TenantID: tenant.ID, TokenID: &tok.ID,
	})
	if err != nil {
		t.Fatalf("seed off engine: %v", err)
	}

	create := func(name string, defaultID, ruleID int64) string {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-routers", map[string]any{
			"name":    name,
			"default": map[string]any{"mt_engine_id": defaultID},
			"rules":   []map[string]any{{"target_lang": "de", "mt_engine_id": ruleID}},
		}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var out struct {
			Data struct {
				Unusable string `json:"unusable"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &out)
		return out.Data.Unusable
	}

	if got := create("All good", good.ID, good.ID); got != "" {
		t.Errorf("a healthy router reports %q, want no warning", got)
	}
	// A broken RULE is enough, even with a healthy default.
	if got := create("Bad rule", good.ID, off.ID); got != "disabled" {
		t.Errorf("a router with a switched-off rule reports %q, want disabled", got)
	}
	// ...and so is a broken DEFAULT, which is the widest breakage of all: it is
	// what every unlisted language uses.
	if got := create("Bad default", off.ID, good.ID); got != "disabled" {
		t.Errorf("a router with a switched-off default reports %q, want disabled", got)
	}

	// Deleting the key leaves both engines keyless, and `no_key` outranks the
	// switch — a router with both leads with the breakage.
	rec := testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/ai/vendors/deepl/tokens/%d", tok.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/mt-routers", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listed struct {
		Data []struct {
			Name     string `json:"name"`
			Unusable string `json:"unusable"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listed)
	if len(listed.Data) != 3 {
		t.Fatalf("listed %d routers, want 3", len(listed.Data))
	}
	for _, r := range listed.Data {
		if r.Unusable != "no_key" {
			t.Errorf("%s reports %q after its key was deleted, want no_key", r.Name, r.Unusable)
		}
	}
}

// DeepL and Google authenticate with a vendor token: `mt_engines.api_key` is
// always NULL for them, and the key lives encrypted on the token. A catalog
// read that takes the key from the column gets an empty one, and an engine with
// no key serves the static table — which refuses nothing, because a compiled-in
// guess is never the engine's own word.
//
// LibreTranslate cannot catch this: it authenticates per instance and needs no
// key at all.
func TestIntegrationMTRouter_RefusesOnATokenBackedEngine(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	// A DeepL that reads en and de but writes only de.
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") == "source" {
			fmt.Fprint(w, `[{"language":"EN","name":"English"},{"language":"DE","name":"German"}]`)
			return
		}
		fmt.Fprint(w, `[{"language":"DE","name":"German"}]`)
	}))
	defer catalog.Close()

	enc, err := secrets.NewEncryptor(testutil.TestAISecretsKey)
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	sealed, err := enc.Encrypt("deepl-key")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	tenant := testutil.SeedTenant(t, q, "RouterTokenCo")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@routertoken.test", "pass", "project_manager")
	tok := testutil.SeedAIVendorTokenBase(t, q, tenant.ID, "deepl", "DeepL key", sealed, catalog.URL)
	eng, err := q.CreateMTEngine(ctx, db.CreateMTEngineParams{
		Name: "DeepL", EngineType: "deepl", Enabled: true,
		TenantID: tenant.ID, TokenID: &tok.ID,
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}

	post := func(targetLang string) (int, string) {
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-routers", map[string]any{
			"name":    "Token router " + targetLang,
			"default": map[string]any{},
			"rules":   []map[string]any{{"target_lang": targetLang, "mt_engine_id": eng.ID}},
		}, pm.ID, tenant.ID, "project_manager")
		return rec.Code, rec.Body.String()
	}

	code, body := post("th")
	if code != http.StatusBadRequest {
		t.Fatalf("Thai on a token-backed DeepL that writes only de: status %d, want 400 (body: %s)", code, body)
	}

	// The control: the language it does write still saves, so the refusal is
	// reading the live catalog rather than failing every rule.
	if code, body := post("de"); code != http.StatusCreated {
		t.Errorf("German on the same engine: status %d, want 201 (body: %s)", code, body)
	}
}

// A router's DEFAULT engine is deliberately exempt from the language check: it
// answers for every language no rule names, so there is nothing to check it
// against until a project supplies its targets.
//
// Pinned separately from the rule tests because the exemption is load-bearing
// rather than incidental — j21's failure-path journey routes through a default
// precisely because a rule for the same engine and language is refused, and the
// mock engine every other default test uses publishes no catalog, so it could
// not tell an exemption from an engine that has nothing to refuse on.
func TestIntegrationMTRouter_DoesNotLanguageCheckTheDefaultEngine(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	// The same en/de engine a rule for Thai is refused on.
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"code":"en","name":"English","targets":["de"]},{"code":"de","name":"German","targets":["en"]}]`)
	}))
	defer catalog.Close()

	tenant := testutil.SeedTenant(t, q, "RouterDefaultCo")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@routerdefault.test", "pass", "project_manager")
	url := catalog.URL
	eng, err := q.CreateMTEngine(ctx, db.CreateMTEngineParams{
		Name: "Catalog engine", EngineType: "libretranslate", ApiUrl: &url,
		Enabled: true, TenantID: tenant.ID,
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-routers", map[string]any{
		"name":    "Default only",
		"default": map[string]any{"mt_engine_id": eng.ID},
		"rules":   []map[string]any{},
	}, pm.ID, tenant.ID, "project_manager")
	if rec.Code != http.StatusCreated {
		t.Fatalf("a default engine carries no target language to check: status %d (body: %s)", rec.Code, rec.Body.String())
	}

	// The control: the same engine, the same language, as a RULE — refused.
	// Without this the test would pass on an engine that refuses nothing.
	ruleRec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-routers", map[string]any{
		"name":    "Rule for th",
		"default": map[string]any{},
		"rules":   []map[string]any{{"target_lang": "th", "mt_engine_id": eng.ID}},
	}, pm.ID, tenant.ID, "project_manager")
	if ruleRec.Code != http.StatusBadRequest {
		t.Errorf("control: a rule for Thai on the same engine must be refused, got %d (body: %s)", ruleRec.Code, ruleRec.Body.String())
	}
}
