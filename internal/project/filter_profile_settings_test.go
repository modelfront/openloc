// The project's filter profile per format through PATCH and GET
// /api/projects/{id}/settings and POST /api/projects. The choice decides how
// every later upload of the format is read, so a pick that does not persist,
// an unrelated save that resets it, or another tenant's profile slipping in
// changes the parse of the next file.
//
// Skipped with -short.
package project_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/filterprofile"
	"openloc/internal/testutil"
)

func TestIntegrationProjectSettings_FilterProfiles(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FilterSettings")
	other := testutil.SeedTenant(t, q, "FilterSettingsOther")
	user := testutil.SeedUser(t, q, tenant.ID, "u@filter-settings.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Filter Project", "en", []string{"fr"})
	settingsPath := fmt.Sprintf("/api/projects/%d/settings", project.ID)
	plainJSON := testutil.BuiltinFilterProfile(t, q, "builtin:json_i18n-plain@1")

	svc := filterprofile.NewService(q, pool)
	theirs, err := svc.Create(testutil.Ctx(), other.ID, 0, filterprofile.Input{Name: "Theirs", Format: "xlsx", Options: &filterprofile.Options{}})
	if err != nil {
		t.Fatal(err)
	}
	htmlProfile, err := svc.Create(testutil.Ctx(), tenant.ID, 0, filterprofile.Input{Name: "Ours", Format: "html", Options: &filterprofile.Options{}})
	if err != nil {
		t.Fatal(err)
	}

	returned := func(t *testing.T) map[string]int64 {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", settingsPath, nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Project struct {
					FilterProfiles []filterprofile.Choice `json:"filter_profiles"`
				} `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return testutil.ChoiceIDs(resp.Data.Project.FilterProfiles)
	}
	patch := func(t *testing.T, body map[string]any, want int) {
		t.Helper()
		testutil.AssertStatus(t, testutil.DoRequest(t, router, "PATCH", settingsPath, body, user.ID, tenant.ID), want)
	}

	t.Run("defaults_to_none", func(t *testing.T) {
		if got := testutil.FilterChoices(t, q, project.SettingsProfileID); len(got) != 0 {
			t.Errorf("a new project names %v, want none (every format's Default)", got)
		}
	})

	t.Run("pick_round_trips", func(t *testing.T) {
		patch(t, map[string]any{"filter_profile_ids": map[string]any{"json_i18n": plainJSON, "html": htmlProfile.ID}}, http.StatusOK)
		want := map[string]int64{"json_i18n": plainJSON, "html": htmlProfile.ID}
		if got := testutil.FilterChoices(t, q, project.SettingsProfileID); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("stored = %v, want %v", got, want)
		}
		if got := returned(t); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("GET returned %v, want %v", got, want)
		}
	})

	// Absent keeps the stored choice: an unrelated save must not reset it.
	t.Run("absent_keeps_stored", func(t *testing.T) {
		patch(t, map[string]any{"fuzzy_threshold": 0.8}, http.StatusOK)
		if got := testutil.FilterChoices(t, q, project.SettingsProfileID)["json_i18n"]; got != plainJSON {
			t.Errorf("an unrelated save changed json_i18n to %d", got)
		}
	})

	t.Run("null_goes_back_to_default", func(t *testing.T) {
		patch(t, map[string]any{"filter_profile_ids": map[string]any{"html": nil}}, http.StatusOK)
		got := testutil.FilterChoices(t, q, project.SettingsProfileID)
		if _, ok := got["html"]; ok {
			t.Errorf("html still names %d after null", got["html"])
		}
		if got["json_i18n"] != plainJSON {
			t.Errorf("clearing html changed json_i18n to %d", got["json_i18n"])
		}
	})

	t.Run("refuses_bad_picks", func(t *testing.T) {
		for name, body := range map[string]map[string]any{
			"other tenant":   {"xlsx": theirs.ID},
			"wrong format":   {"xlsx": plainJSON},
			"unknown format": {"docx": plainJSON},
			"unknown id":     {"xlsx": 999999999},
		} {
			rec := testutil.DoRequest(t, router, "PATCH", settingsPath, map[string]any{"filter_profile_ids": body}, user.ID, tenant.ID)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", name, rec.Code)
			}
		}
		if _, ok := testutil.FilterChoices(t, q, project.SettingsProfileID)["xlsx"]; ok {
			t.Error("a refused pick was stored")
		}
	})

	t.Run("change_is_recorded", func(t *testing.T) {
		n := testutil.CountRows(t, pool, `SELECT count(*) FROM project_events
			WHERE project_id = $1 AND event_type = 'project.settings_updated' AND details->'changes' ? 'filter_profile_html'`, project.ID)
		if n != 2 {
			t.Errorf("settings events carrying filter_profile_html = %d, want 2 (pick, then clear)", n)
		}
		var to string
		if err := pool.QueryRow(testutil.Ctx(), `SELECT details->'changes'->'filter_profile_html'->>'to' FROM project_events
			WHERE project_id = $1 AND details->'changes' ? 'filter_profile_html' ORDER BY id DESC LIMIT 1`, project.ID).Scan(&to); err != nil {
			t.Fatal(err)
		}
		if to != "Default" {
			t.Errorf("the clear is recorded as %q, want Default", to)
		}
	})
}

// POST /api/projects carries the wizard's choice; omitted, a new project
// names none, and one made from a template copies the template's.
func TestIntegrationProjectCreate_FilterProfiles(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FilterCreate")
	user := testutil.SeedUser(t, q, tenant.ID, "u@filter-create.test", "pass")
	plainPO := testutil.BuiltinFilterProfile(t, q, "builtin:po-plain@1")

	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Plain PO")
	if err := filterprofile.SetChoices(testutil.Ctx(), q, tenant.ID, tmpl.SettingsProfileID, map[string]*int64{"po": &plainPO}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		body map[string]any
		want map[string]int64
	}{
		{"picked", map[string]any{"settings": map[string]any{"filter_profile_ids": map[string]any{"po": plainPO}}}, map[string]int64{"po": plainPO}},
		{"omitted", map[string]any{"settings": map[string]any{"mt_on_upload": false}}, map[string]int64{}},
		{"from_template", map[string]any{"template_id": tmpl.ID}, map[string]int64{"po": plainPO}},
		// The New project form sends null for a template's pick set back to
		// Default: the user's reset wins over the template.
		{"template_pick_reset", map[string]any{"template_id": tmpl.ID, "settings": map[string]any{"filter_profile_ids": map[string]any{"po": nil}}}, map[string]int64{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := map[string]any{
				"name": "Create " + c.name, "source_language": "en", "target_languages": []string{"fr"},
				"workflow_id": testutil.LibraryWorkflow(t, q, tenant.ID),
			}
			for k, v := range c.body {
				body[k] = v
			}
			rec := testutil.DoRequest(t, router, "POST", "/api/projects", body, user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusCreated)
			var resp struct {
				Data struct {
					ID int64 `json:"id"`
				} `json:"data"`
			}
			testutil.DecodeJSON(t, rec, &resp)
			project, err := q.GetProject(testutil.Ctx(), db.GetProjectParams{ID: resp.Data.ID, TenantID: tenant.ID})
			if err != nil {
				t.Fatal(err)
			}
			if got := testutil.FilterChoices(t, q, project.SettingsProfileID); fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("stored = %v, want %v", got, c.want)
			}
		})
	}
}

// POST /api/projects refuses another organization's profile, a profile of
// another format and an unknown format, and creates nothing.
func TestIntegrationProjectCreate_FilterProfilesRefused(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FilterCreateRefused")
	other := testutil.SeedTenant(t, q, "FilterCreateRefusedOther")
	user := testutil.SeedUser(t, q, tenant.ID, "u@filter-create-refused.test", "pass")
	theirs, err := filterprofile.NewService(q, pool).Create(testutil.Ctx(), other.ID, 0, filterprofile.Input{Name: "Theirs", Format: "po", Options: &filterprofile.Options{}})
	if err != nil {
		t.Fatal(err)
	}
	plainJSON := testutil.BuiltinFilterProfile(t, q, "builtin:json_i18n-plain@1")

	for name, ids := range map[string]map[string]any{
		"other tenant":   {"po": theirs.ID},
		"wrong format":   {"po": plainJSON},
		"unknown format": {"docx": plainJSON},
	} {
		t.Run(name, func(t *testing.T) {
			projects := testutil.CountRows(t, pool, `SELECT count(*) FROM projects WHERE tenant_id = $1`, tenant.ID)
			rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
				"name": "Refused " + name, "source_language": "en", "target_languages": []string{"fr"},
				"workflow_id": testutil.LibraryWorkflow(t, q, tenant.ID), "settings": map[string]any{"filter_profile_ids": ids},
			}, user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
			if after := testutil.CountRows(t, pool, `SELECT count(*) FROM projects WHERE tenant_id = $1`, tenant.ID); after != projects {
				t.Errorf("projects %d → %d after a refused create", projects, after)
			}
		})
	}
}

// A settings PATCH is one transaction: when the filter pick fails inside it,
// the scalar changes of the same request are not saved and no settings event
// is written. A trigger fails the pick's write; without it the same PATCH
// saves both.
func TestIntegrationProjectSettings_FailedPickSavesNothing(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()
	tenant := testutil.SeedTenant(t, q, "FilterSettingsAtomic")
	user := testutil.SeedUser(t, q, tenant.ID, "u@filter-settings-atomic.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Atomic", "en", []string{"fr"})
	path := fmt.Sprintf("/api/projects/%d/settings", project.ID)
	plainPO := testutil.BuiltinFilterProfile(t, q, "builtin:po-plain@1")
	threshold := func() float32 {
		t.Helper()
		sp, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
		if err != nil {
			t.Fatal(err)
		}
		return sp.FuzzyThreshold
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	// The package's tests share one database: the trigger goes with the test.
	defer func() {
		if _, err := pool.Exec(ctx, `
			DROP TRIGGER IF EXISTS test_fail_filter_pick ON settings_profile_filter_profiles;
			DROP FUNCTION IF EXISTS test_fail_filter_pick();`); err != nil {
			t.Errorf("drop the test trigger: %v", err)
		}
	}()
	exec(`CREATE OR REPLACE FUNCTION test_fail_filter_pick() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'filter pick fails'; END $$`)
	exec(`CREATE TRIGGER test_fail_filter_pick BEFORE INSERT OR UPDATE ON settings_profile_filter_profiles
		FOR EACH ROW EXECUTE FUNCTION test_fail_filter_pick()`)

	before := threshold()
	body := map[string]any{"fuzzy_threshold": 0.9, "filter_profile_ids": map[string]any{"po": plainPO}}
	if rec := testutil.DoRequest(t, router, "PATCH", path, body, user.ID, tenant.ID); rec.Code < http.StatusInternalServerError {
		t.Errorf("PATCH with the pick failing = %d, want a server error", rec.Code)
	}
	if got := threshold(); got != before {
		t.Errorf("fuzzy_threshold = %v after the failed PATCH, want still %v", got, before)
	}
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM project_events WHERE project_id = $1 AND event_type = 'project.settings_updated'`, project.ID); n != 0 {
		t.Errorf("settings events after the failed PATCH = %d, want 0", n)
	}

	exec(`DROP TRIGGER test_fail_filter_pick ON settings_profile_filter_profiles`)
	testutil.AssertStatus(t, testutil.DoRequest(t, router, "PATCH", path, body, user.ID, tenant.ID), http.StatusOK)
	if got := threshold(); got == before {
		t.Errorf("fuzzy_threshold = %v after the PATCH, want 0.9", got)
	}
}
