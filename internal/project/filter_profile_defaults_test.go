// The org's default filter profile on POST /api/projects and POST
// /api/templates. The default is copied at creation: a project or template
// without a template starts with it, a template's picks win over it, and a
// later change of the default leaves existing projects as they are.
//
// Skipped with -short.
package project_test

import (
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/filterprofile"
	"openloc/internal/project"
	"openloc/internal/testutil"
)

func TestIntegrationProjectCreate_OrgFilterDefaults(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FilterDefaults")
	user := testutil.SeedUser(t, q, tenant.ID, "u@filter-defaults.test", "pass")
	plainJSON := testutil.BuiltinFilterProfile(t, q, "builtin:json_i18n-plain@1")
	plainXLSX := testutil.BuiltinFilterProfile(t, q, "builtin:xlsx-plain@1")
	mine, err := filterprofile.NewService(q, pool).Create(testutil.Ctx(), tenant.ID, 0,
		filterprofile.Input{Name: "Mine", Format: "json_i18n", Options: &filterprofile.Options{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := filterprofile.SetOrgDefault(testutil.Ctx(), q, tenant.ID, user.ID, "json_i18n", &mine.ID); err != nil {
		t.Fatal(err)
	}

	created := func(t *testing.T, path string, body map[string]any) int64 {
		t.Helper()
		rec := testutil.DoRequest(t, router, "POST", path, body, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var resp struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data.ID
	}
	projectPicks := func(t *testing.T, id int64) map[string]int64 {
		t.Helper()
		p, err := q.GetProject(testutil.Ctx(), db.GetProjectParams{ID: id, TenantID: tenant.ID})
		if err != nil {
			t.Fatal(err)
		}
		return testutil.FilterChoices(t, q, p.SettingsProfileID)
	}
	templatePicks := func(t *testing.T, id int64) map[string]int64 {
		t.Helper()
		tmpl, err := q.GetTemplate(testutil.Ctx(), db.GetTemplateParams{ID: id, TenantID: tenant.ID})
		if err != nil {
			t.Fatal(err)
		}
		return testutil.FilterChoices(t, q, tmpl.SettingsProfileID)
	}
	newProject := func(extra map[string]any) map[string]any {
		body := map[string]any{"name": "Defaulted", "source_language": "en", "target_languages": []string{"fr"},
			"workflow_id": testutil.LibraryWorkflow(t, q, tenant.ID)}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}

	var withoutTemplate int64
	t.Run("project_without_template_starts_with_the_default", func(t *testing.T) {
		withoutTemplate = created(t, "/api/projects", newProject(nil))
		if got := projectPicks(t, withoutTemplate); got["json_i18n"] != mine.ID || len(got) != 1 {
			t.Errorf("picks = %v, want json_i18n %d only", got, mine.ID)
		}
	})

	t.Run("explicit_pick_and_explicit_default_win", func(t *testing.T) {
		id := created(t, "/api/projects", newProject(map[string]any{"settings": map[string]any{"filter_profile_ids": map[string]any{"json_i18n": plainJSON}}}))
		if got := projectPicks(t, id)["json_i18n"]; got != plainJSON {
			t.Errorf("an explicit pick stored %d, want %d", got, plainJSON)
		}
		id = created(t, "/api/projects", newProject(map[string]any{"settings": map[string]any{"filter_profile_ids": map[string]any{"json_i18n": nil}}}))
		if got := projectPicks(t, id); len(got) != 0 {
			t.Errorf("an explicit Default stored %v, want no pick", got)
		}
	})

	// project.Create (no template, no setup) stores the defaults itself.
	t.Run("bare_create_starts_with_the_default", func(t *testing.T) {
		p, err := project.Create(testutil.Ctx(), pool, q, tenant.ID, user.ID, project.CreateParams{
			Name: "Bare draft", SourceLanguage: "en", TargetLanguages: []string{"fr"}, Status: "draft",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := projectPicks(t, p.ID); got["json_i18n"] != mine.ID || len(got) != 1 {
			t.Errorf("picks = %v, want json_i18n %d only", got, mine.ID)
		}
	})

	t.Run("template_create_starts_with_the_default", func(t *testing.T) {
		id := created(t, "/api/templates", map[string]any{"name": "Defaulted template", "workflow_id": testutil.LibraryWorkflow(t, q, tenant.ID)})
		if got := templatePicks(t, id); got["json_i18n"] != mine.ID {
			t.Errorf("template picks = %v, want json_i18n %d", got, mine.ID)
		}
	})

	t.Run("project_from_template_takes_only_the_template", func(t *testing.T) {
		tmpl := created(t, "/api/templates", map[string]any{
			"name": "Excel only", "workflow_id": testutil.LibraryWorkflow(t, q, tenant.ID),
			"settings": map[string]any{"filter_profile_ids": map[string]any{"xlsx": plainXLSX, "json_i18n": nil}},
		})
		id := created(t, "/api/projects", map[string]any{"name": "From template", "source_language": "en", "target_languages": []string{"fr"}, "template_id": tmpl})
		if got := projectPicks(t, id); got["xlsx"] != plainXLSX || len(got) != 1 {
			t.Errorf("picks = %v, want the template's xlsx %d only", got, plainXLSX)
		}
	})

	t.Run("changing_the_default_leaves_projects_alone", func(t *testing.T) {
		if err := filterprofile.SetOrgDefault(testutil.Ctx(), q, tenant.ID, user.ID, "json_i18n", &plainJSON); err != nil {
			t.Fatal(err)
		}
		if got := projectPicks(t, withoutTemplate)["json_i18n"]; got != mine.ID {
			t.Errorf("after the default changed the project names %d, want %d", got, mine.ID)
		}
	})
}
