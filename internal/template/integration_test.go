package template_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationTemplateCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "My Template",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var resp struct {
		Data struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Name != "My Template" {
		t.Errorf("expected 'My Template', got %q", resp.Data.Name)
	}

	listed := func() bool {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", "/api/templates", nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var list struct {
			Data []struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &list)
		for _, row := range list.Data {
			if row.ID == resp.Data.ID {
				return true
			}
		}
		return false
	}
	if !listed() {
		t.Fatal("created template missing from the list")
	}
	tmpl, err := q.GetTemplate(context.Background(), db.GetTemplateParams{ID: resp.Data.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	if _, err := q.GetSettingsProfile(context.Background(), tmpl.SettingsProfileID); err != nil {
		t.Fatalf("template's settings profile before delete: %v", err)
	}

	path := fmt.Sprintf("/api/templates/%d", resp.Data.ID)
	rec = testutil.DoRequest(t, router, "DELETE", path, nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	rec = testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if listed() {
		t.Error("deleted template still in the list")
	}
	if _, err := q.GetSettingsProfile(context.Background(), tmpl.SettingsProfileID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("template's settings profile after delete: want pgx.ErrNoRows, got %v", err)
	}
}

// The New project picker pages through the list and searches it by name.
func TestIntegrationTemplateListPagination(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")
	other := testutil.SeedTenant(t, q, "OtherCorp")
	testutil.SeedTemplate(t, q, other.ID, "Marketing other tenant")

	for i := 1; i <= 5; i++ {
		testutil.SeedTemplate(t, q, tenant.ID, fmt.Sprintf("Marketing %d", i))
	}
	testutil.SeedTemplate(t, q, tenant.ID, "Legal review")

	type page struct {
		Data []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Cursor  int64 `json:"cursor"`
		HasMore bool  `json:"has_more"`
	}
	get := func(query string) page {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", "/api/templates"+query, nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var p page
		testutil.DecodeJSON(t, rec, &p)
		return p
	}

	// No query params: every template of this tenant, and only this tenant's.
	all := get("")
	if len(all.Data) != 6 || all.HasMore {
		t.Fatalf("unpaginated list: got %d rows has_more=%v, want 6 false", len(all.Data), all.HasMore)
	}
	for _, row := range all.Data {
		if row.Name == "Marketing other tenant" {
			t.Fatal("another tenant's template leaked into the list")
		}
	}

	// Paged: the second page continues where the first stopped, no overlap.
	first := get("?limit=4")
	if len(first.Data) != 4 || !first.HasMore || first.Cursor == 0 {
		t.Fatalf("first page: got %d rows has_more=%v cursor=%d", len(first.Data), first.HasMore, first.Cursor)
	}
	second := get(fmt.Sprintf("?limit=4&cursor=%d", first.Cursor))
	if len(second.Data) != 2 || second.HasMore {
		t.Fatalf("second page: got %d rows has_more=%v, want 2 false", len(second.Data), second.HasMore)
	}
	seen := map[int64]bool{}
	for _, row := range append(first.Data, second.Data...) {
		if seen[row.ID] {
			t.Fatalf("template %d appears on both pages", row.ID)
		}
		seen[row.ID] = true
	}

	// Search is by name, case-insensitive, and pages the same way.
	found := get("?q=marketing")
	if len(found.Data) != 5 {
		t.Fatalf("search: got %d rows, want 5", len(found.Data))
	}
	searchPage := get("?q=marketing&limit=2")
	if len(searchPage.Data) != 2 || !searchPage.HasMore {
		t.Fatalf("search page: got %d rows has_more=%v, want 2 true", len(searchPage.Data), searchPage.HasMore)
	}
	if none := get("?q=nothing-matches-this"); len(none.Data) != 0 || none.HasMore {
		t.Fatalf("empty search: got %d rows has_more=%v", len(none.Data), none.HasMore)
	}
}

func TestIntegrationTemplateTMAssignments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")
	tmColl := testutil.SeedTM(t, q, tenant.ID, "Main TM")
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Template with TMs")

	// TM assignments are per-TM (no source/target languages).
	rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/tms", tmpl.ID), map[string]any{
		"assignments": []map[string]any{
			{"tm_id": tmColl.ID, "mode": "read_write", "priority": 1},
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	assignments, err := q.ListSettingsTMAssignments(context.Background(), tmpl.SettingsProfileID)
	if err != nil {
		t.Fatalf("ListSettingsTMAssignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("expected 1 TM assignment, got %d", len(assignments))
	}
	if assignments[0].TmID != tmColl.ID || assignments[0].Mode != "read_write" {
		t.Errorf("unexpected assignment: tm=%d mode=%q", assignments[0].TmID, assignments[0].Mode)
	}
}

// TestIntegrationTemplateTMLookupLanguageMatch pins that template TM lookups
// match a TM declared for "fr" by both "fr" and the regional "fr-fr".
func TestIntegrationTemplateTMLookupLanguageMatch(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "TestCorp")
	// Coverage comes from the TM's declared languages (en → [fr]).
	tmColl := testutil.SeedTMWithLangs(t, q, tenant.ID, "FR TM", "en", []string{"fr"})
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "FR Template")

	// Per-TM assignment — no per-pair languages.
	_, err := q.CreateSettingsTMAssignment(context.Background(), db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: tmpl.SettingsProfileID,
		TmID:              tmColl.ID,
		Mode:              "read_write",
		Priority:          1,
	})
	if err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}

	// Lookup with exact match — should find TM
	rows, err := q.LookupSettingsWriteTMs(context.Background(), db.LookupSettingsWriteTMsParams{
		SettingsProfileID: tmpl.SettingsProfileID,
		SourceLanguage:    "en",
		TargetLanguage:    "fr",
	})
	if err != nil {
		t.Fatalf("LookupSettingsWriteTMs: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 write TM for en→fr, got %d", len(rows))
	}

	// Lookup with "fr-fr" — should also find TM via base-language fallback
	rows, err = q.LookupSettingsWriteTMs(context.Background(), db.LookupSettingsWriteTMsParams{
		SettingsProfileID: tmpl.SettingsProfileID,
		SourceLanguage:    "en",
		TargetLanguage:    "fr-fr",
	})
	if err != nil {
		t.Fatalf("LookupSettingsWriteTMs with fr-fr: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 write TM for en→fr-fr (base fallback), got %d", len(rows))
	}

	// Lookup with a target the TM does NOT declare — must NOT match (proves the
	// per-TM lookup respects the TM's declared target list, not a wildcard).
	rows, err = q.LookupSettingsWriteTMs(context.Background(), db.LookupSettingsWriteTMsParams{
		SettingsProfileID: tmpl.SettingsProfileID,
		SourceLanguage:    "en",
		TargetLanguage:    "de",
	})
	if err != nil {
		t.Fatalf("LookupSettingsWriteTMs with de: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 write TMs for en→de (TM declares only fr), got %d", len(rows))
	}
}

// PATCH /api/templates/{id} `mt_router_id` is tri-state: absent keeps, a
// router id sets (GET echoes id + name), null clears, and a router from
// another tenant is refused without touching the stored pointer.
func TestIntegrationTemplateMTRouterTriState(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")
	engine := testutil.SeedMTEngine(t, q, tenant.ID, "DeepL", "deepl")
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Template with MT")
	mtRouter := testutil.SeedMTRouter(t, q, tenant.ID, "Prod MT", &engine.ID, nil)
	other := testutil.SeedTenant(t, q, "OtherCorp")
	otherEngine := testutil.SeedMTEngine(t, q, other.ID, "Theirs", "deepl")
	foreignRouter := testutil.SeedMTRouter(t, q, other.ID, "Their router", &otherEngine.ID, nil)

	get := func() (*int64, *string) {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d", tmpl.ID), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Template struct {
					MTRouterID   *int64  `json:"mt_router_id"`
					MTRouterName *string `json:"mt_router_name"`
				} `json:"template"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data.Template.MTRouterID, resp.Data.Template.MTRouterName
	}

	// Set.
	rec := testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", tmpl.ID), map[string]any{
		"mt_router_id": mtRouter,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if id, name := get(); id == nil || *id != mtRouter || name == nil || *name != "Prod MT" {
		t.Fatalf("after set: want router %d %q, got %v %v", mtRouter, "Prod MT", id, name)
	}

	// Absent field keeps the pointer.
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", tmpl.ID), map[string]any{
		"name": "Template with MT (renamed)",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if id, _ := get(); id == nil || *id != mtRouter {
		t.Fatalf("a PATCH without mt_router_id must keep the router, got %v", id)
	}

	// Foreign router: 400, pointer untouched.
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", tmpl.ID), map[string]any{
		"mt_router_id": foreignRouter,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if id, _ := get(); id == nil || *id != mtRouter {
		t.Fatalf("a refused PATCH must keep the router, got %v", id)
	}

	// Null clears.
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", tmpl.ID), map[string]any{
		"mt_router_id": nil,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if id, name := get(); id != nil || name != nil {
		t.Fatalf("after null: want no router, got %v %v", id, name)
	}
	profile, err := q.GetSettingsProfile(ctx, tmpl.SettingsProfileID)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if profile.MtRouterID != nil {
		t.Fatalf("profile row must have mt_router_id NULL after clear, got %d", *profile.MtRouterID)
	}
}
