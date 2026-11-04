package publicapi_test

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/filterprofile"
	"openloc/internal/publicapi"
	"openloc/internal/testutil"
)

// Filter profiles over v1: every member lists and reads, a manager copies a
// built-in, replaces and deletes the copy; a project created with a pick
// names it, and an upload may pick another profile of its format.
func TestIntegrationV1_FilterProfiles(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Filter")
	c := f.client
	pm := func() string { return f.key(t, "project_manager") }
	linguist := func() string { return f.key(t, "linguist") }

	rec := c.do(http.MethodGet, "/api/v1/filter-profiles?format=json_i18n&limit=100", nil, linguist())
	wantStatus(t, rec, http.StatusOK)
	var plain *publicapi.FilterProfile
	list := testutil.DecodeData[[]publicapi.FilterProfile](t, rec)
	for i, p := range list {
		if p.Format != "json_i18n" {
			t.Errorf("a json_i18n list holds %+v", p)
		}
		if p.BuiltinKey != nil && *p.BuiltinKey == "builtin:json_i18n-plain@1" {
			plain = &list[i]
		}
	}
	if plain == nil || plain.Options.InlineHTML == nil || *plain.Options.InlineHTML {
		t.Fatalf("no plain-text JSON built-in in %+v", list)
	}
	rec = c.do(http.MethodGet, fmt.Sprintf("/api/v1/filter-profiles/%d", f.ids["filterProfileID"]), nil, linguist())
	wantStatus(t, rec, http.StatusOK)

	// Copy the built-in, replace the copy, refuse a stale replace.
	rec = c.do(http.MethodPost, "/api/v1/filter-profiles", map[string]any{"name": "Plain JSON", "from_id": plain.ID}, pm())
	wantStatus(t, rec, http.StatusCreated)
	copied := testutil.DecodeData[publicapi.FilterProfile](t, rec)
	if copied.Format != "json_i18n" || copied.BasedOnID == nil || *copied.BasedOnID != plain.ID || copied.Builtin {
		t.Fatalf("copy = %+v", copied)
	}
	path := fmt.Sprintf("/api/v1/filter-profiles/%d", copied.ID)
	rec = c.do(http.MethodPut, path, map[string]any{"name": "Plain JSON v2", "options": map[string]any{"inline_html": false}, "updated_at": copied.UpdatedAt}, pm())
	wantStatus(t, rec, http.StatusOK)
	rec = c.do(http.MethodPut, path, map[string]any{"name": "Stale", "options": map[string]any{}, "updated_at": copied.UpdatedAt}, pm())
	wantStatus(t, rec, http.StatusConflict)
	rec = c.do(http.MethodPost, "/api/v1/filter-profiles", map[string]any{"name": "Mine", "format": "html", "options": map[string]any{}}, linguist())
	wantStatus(t, rec, http.StatusForbidden)

	// A project created with the pick names it; an upload takes another.
	rec = c.do(http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Filtered", "source_language": "en", "target_languages": []string{"de"},
		"workflow_id": testutil.LibraryWorkflow(t, q, f.tenant.ID), "filter_profile_ids": map[string]any{"json_i18n": copied.ID},
	}, pm())
	wantStatus(t, rec, http.StatusCreated)
	projectID := testutil.DecodeData[struct {
		ID int64 `json:"id"`
	}](t, rec).ID
	project, err := f.q.GetProject(testutil.Ctx(), db.GetProjectParams{ID: projectID, TenantID: f.tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	choices, err := filterprofile.Choices(testutil.Ctx(), f.q, project.SettingsProfileID)
	if err != nil || len(choices) != 1 || choices[0].ID != copied.ID {
		t.Errorf("project's picks = %+v (%v), want json_i18n %d", choices, err, copied.ID)
	}
	// Another organization's profile is refused on create.
	theirs, err := f.q.CreateFilterProfile(testutil.Ctx(), db.CreateFilterProfileParams{
		TenantID: testutil.SeedTenant(t, f.q, "FilterV1Other").ID, Name: "Theirs", Format: "json_i18n", Options: []byte(`{"inline_html":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = c.do(http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Refused", "source_language": "en", "target_languages": []string{"de"},
		"workflow_id": testutil.LibraryWorkflow(t, q, f.tenant.ID), "filter_profile_ids": map[string]any{"json_i18n": theirs},
	}, pm())
	wantStatus(t, rec, http.StatusBadRequest)

	rec = c.uploadWith(fmt.Sprintf("/api/v1/projects/%d/files", projectID), "strings.json", []byte(`{"a": "Use <b>bold</b>"}`),
		map[string]string{"filter_profile_id": strconv.FormatInt(f.ids["filterProfileID"], 10)}, pm())
	wantStatus(t, rec, http.StatusBadRequest) // an Excel profile for a JSON file

	rec = c.do(http.MethodDelete, path, nil, pm())
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodGet, path, nil, pm())
	wantStatus(t, rec, http.StatusNotFound)
}

// The org default over v1: a manager sets it, a project created without a
// template or a pick starts with it, a null pick stays the built-in
// Default, and the default cannot be deleted until it is cleared.
func TestIntegrationV1_FilterProfileDefaults(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "FilterDefault")
	c := f.client
	pm := func() string { return f.key(t, "project_manager") }

	rec := c.do(http.MethodPost, "/api/v1/filter-profiles", map[string]any{"name": "Plain JSON", "format": "json_i18n", "options": map[string]any{"inline_html": false}}, pm())
	wantStatus(t, rec, http.StatusCreated)
	mine := testutil.DecodeData[publicapi.FilterProfile](t, rec)
	path := fmt.Sprintf("/api/v1/filter-profiles/%d", mine.ID)

	rec = c.do(http.MethodPut, "/api/v1/filter-profiles/defaults", map[string]any{"format": "json_i18n", "filter_profile_id": mine.ID}, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusForbidden)
	rec = c.do(http.MethodPut, "/api/v1/filter-profiles/defaults", map[string]any{"format": "xlsx", "filter_profile_id": mine.ID}, pm())
	wantStatus(t, rec, http.StatusBadRequest) // a JSON profile for Excel
	rec = c.do(http.MethodPut, "/api/v1/filter-profiles/defaults", map[string]any{"format": "json_i18n", "filter_profile_id": mine.ID}, pm())
	wantStatus(t, rec, http.StatusOK)
	defaults := testutil.DecodeData[[]publicapi.FilterProfileDefault](t, rec)
	if len(defaults) != 1 || defaults[0].Format != "json_i18n" || defaults[0].FilterProfileID != mine.ID || defaults[0].Name != "Plain JSON" {
		t.Fatalf("defaults = %+v", defaults)
	}
	rec = c.do(http.MethodGet, path, nil, pm())
	wantStatus(t, rec, http.StatusOK)
	if !testutil.DecodeData[publicapi.FilterProfile](t, rec).OrgDefault {
		t.Error("the default profile reads org_default false")
	}

	create := func(picks map[string]any) []filterprofile.Choice {
		t.Helper()
		body := map[string]any{"name": "Defaulted", "source_language": "en", "target_languages": []string{"de"},
			"workflow_id": testutil.LibraryWorkflow(t, q, f.tenant.ID)}
		if picks != nil {
			body["filter_profile_ids"] = picks
		}
		rec := c.do(http.MethodPost, "/api/v1/projects", body, pm())
		wantStatus(t, rec, http.StatusCreated)
		id := testutil.DecodeData[struct {
			ID int64 `json:"id"`
		}](t, rec).ID
		project, err := f.q.GetProject(testutil.Ctx(), db.GetProjectParams{ID: id, TenantID: f.tenant.ID})
		if err != nil {
			t.Fatal(err)
		}
		choices, err := filterprofile.Choices(testutil.Ctx(), f.q, project.SettingsProfileID)
		if err != nil {
			t.Fatal(err)
		}
		return choices
	}
	if got := create(nil); len(got) != 1 || got[0].Format != "json_i18n" || got[0].ID != mine.ID {
		t.Errorf("a project without picks = %+v, want the org default", got)
	}
	if got := create(map[string]any{"json_i18n": nil}); len(got) != 0 {
		t.Errorf("a project picking Default = %+v, want no pick", got)
	}

	rec = c.do(http.MethodDelete, path, nil, pm())
	wantStatus(t, rec, http.StatusConflict)
	rec = c.do(http.MethodPut, "/api/v1/filter-profiles/defaults", map[string]any{"format": "json_i18n", "filter_profile_id": nil}, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.FilterProfileDefault](t, rec); len(got) != 0 {
		t.Errorf("defaults after clearing = %+v", got)
	}
	rec = c.do(http.MethodDelete, path, nil, pm())
	wantStatus(t, rec, http.StatusNoContent)
}
