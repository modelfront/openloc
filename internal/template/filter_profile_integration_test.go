package template_test

// A template's filter profile per format: created, edited and read through
// the API, carried by a duplicate and handed to a project made from it. Each
// check starts from a pick, because with none every copy would read the
// Default without carrying anything.

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/filterprofile"
	"openloc/internal/testutil"
)

func TestIntegrationTemplateFilterProfiles(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FilterTemplate")
	user := testutil.SeedUser(t, q, tenant.ID, "u@filter-template.test", "pass")
	plain := testutil.BuiltinFilterProfile(t, q, "builtin:json_i18n-plain@1")

	type templateBody struct {
		ID             int64                  `json:"id"`
		FilterProfiles []filterprofile.Choice `json:"filter_profiles"`
	}
	get := func(t *testing.T, id int64) map[string]int64 {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d", id), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Template templateBody `json:"template"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return testutil.ChoiceIDs(resp.Data.Template.FilterProfiles)
	}
	patch := func(t *testing.T, id int64, body map[string]any) {
		t.Helper()
		testutil.AssertStatus(t, testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", id), body, user.ID, tenant.ID), http.StatusOK)
	}

	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Plain strings", "settings": map[string]any{"filter_profile_ids": map[string]any{"json_i18n": plain}},
		"workflow_id": testutil.LibraryWorkflow(t, q, tenant.ID),
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct {
		Data templateBody `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &created)
	id := created.Data.ID

	t.Run("create_stores_the_pick", func(t *testing.T) {
		if got := get(t, id)["json_i18n"]; got != plain {
			t.Errorf("json_i18n = %d after create, want %d", got, plain)
		}
		if got := testutil.ChoiceIDs(created.Data.FilterProfiles)["json_i18n"]; got != plain {
			t.Errorf("create response json_i18n = %d, want %d", got, plain)
		}
	})

	t.Run("patch_round_trips_and_absent_keeps", func(t *testing.T) {
		patch(t, id, map[string]any{"filter_profile_ids": map[string]any{"json_i18n": nil}})
		if got, ok := get(t, id)["json_i18n"]; ok {
			t.Errorf("json_i18n = %d after PATCH null, want the Default", got)
		}
		patch(t, id, map[string]any{"filter_profile_ids": map[string]any{"json_i18n": plain}})
		patch(t, id, map[string]any{"auto_complete": false})
		if got := get(t, id)["json_i18n"]; got != plain {
			t.Errorf("an unrelated PATCH changed json_i18n to %d", got)
		}
	})

	t.Run("duplicate_carries_it", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/templates/%d/duplicate", id), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var dup struct {
			Data templateBody `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &dup)
		if got := get(t, dup.Data.ID)["json_i18n"]; got != plain {
			t.Errorf("the duplicate's json_i18n = %d, want the source's %d", got, plain)
		}
	})

	t.Run("project_from_template_copies_it", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
			"name": "From plain template", "source_language": "en", "target_languages": []string{"fr"}, "template_id": id,
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var project struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &project)
		rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", project.Data.ID), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var settings struct {
			Data struct {
				Project struct {
					FilterProfiles []filterprofile.Choice `json:"filter_profiles"`
				} `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &settings)
		if got := testutil.ChoiceIDs(settings.Data.Project.FilterProfiles)["json_i18n"]; got != plain {
			t.Errorf("project json_i18n = %d, want %d from the template", got, plain)
		}
	})
}

// Template create and edit refuse another organization's filter profile and
// store nothing.
func TestIntegrationTemplateFilterProfiles_OtherTenantRefused(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FilterTemplateRefused")
	other := testutil.SeedTenant(t, q, "FilterTemplateRefusedOther")
	user := testutil.SeedUser(t, q, tenant.ID, "u@filter-template-refused.test", "pass")
	theirs, err := filterprofile.NewService(q, pool).Create(testutil.Ctx(), other.ID, 0, filterprofile.Input{Name: "Theirs", Format: "xlsx", Options: &filterprofile.Options{}})
	if err != nil {
		t.Fatal(err)
	}
	pick := map[string]any{"filter_profile_ids": map[string]any{"xlsx": theirs.ID}}

	templates := testutil.CountRows(t, pool, `SELECT count(*) FROM templates WHERE tenant_id = $1`, tenant.ID)
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Refused", "settings": pick, "workflow_id": testutil.LibraryWorkflow(t, q, tenant.ID),
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if after := testutil.CountRows(t, pool, `SELECT count(*) FROM templates WHERE tenant_id = $1`, tenant.ID); after != templates {
		t.Errorf("templates %d → %d after a refused create", templates, after)
	}

	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Mine")
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", tmpl.ID), pick, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if got := testutil.FilterChoices(t, q, tmpl.SettingsProfileID); len(got) != 0 {
		t.Errorf("a refused PATCH stored %v", got)
	}
}
