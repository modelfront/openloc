// TM lookup order through the HTTP surfaces that carry
// match scoring: project settings, TM profiles, templates, detach, and
// project-from-template. A value that is dropped on any of these paths
// silently changes which TM fills a segment.
//
// Skipped with -short.
package project_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/settings"
	"openloc/internal/testutil"
)

func TestIntegrationTMLookupOrder_Surfaces(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "LookupOrderCorp")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "test@test.com", "pass", "project_manager")
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		return testutil.DoRequestWithRole(t, router, method, path, body, pm.ID, tenant.ID, "project_manager")
	}
	stored := func(settingsProfileID int64) string {
		t.Helper()
		sp, err := q.GetSettingsProfile(ctx, settingsProfileID)
		if err != nil {
			t.Fatalf("GetSettingsProfile: %v", err)
		}
		return sp.TmLookupOrder
	}
	projectOrder := func(projectID int64) string {
		t.Helper()
		p, err := q.GetProjectByID(ctx, projectID)
		if err != nil {
			t.Fatalf("GetProjectByID: %v", err)
		}
		return stored(p.SettingsProfileID)
	}

	t.Run("project settings PATCH", func(t *testing.T) {
		project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"fr"})
		path := fmt.Sprintf("/api/projects/%d/settings", project.ID)

		if got := stored(project.SettingsProfileID); got != settings.TMLookupOrderMatchPercent {
			t.Fatalf("default = %q, want match_percent", got)
		}
		testutil.AssertStatus(t, do("PATCH", path, map[string]any{"tm_lookup_order": "tm_order"}), http.StatusOK)
		if got := stored(project.SettingsProfileID); got != settings.TMLookupOrderTMOrder {
			t.Errorf("after PATCH = %q, want tm_order", got)
		}
		// An unrelated save keeps it (nil-keeps-stored).
		testutil.AssertStatus(t, do("PATCH", path, map[string]any{"fuzzy_threshold": 0.8}), http.StatusOK)
		if got := stored(project.SettingsProfileID); got != settings.TMLookupOrderTMOrder {
			t.Errorf("after unrelated save = %q, want tm_order", got)
		}
		// Unknown value → 400, stored value untouched.
		testutil.AssertStatus(t, do("PATCH", path, map[string]any{"tm_lookup_order": "best"}), http.StatusBadRequest)
		if got := stored(project.SettingsProfileID); got != settings.TMLookupOrderTMOrder {
			t.Errorf("after rejected PATCH = %q, want tm_order", got)
		}
		rec := do("GET", path, nil)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Project struct {
					TMLookupOrder string `json:"tm_lookup_order"`
				} `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if resp.Data.Project.TMLookupOrder != settings.TMLookupOrderTMOrder {
			t.Errorf("GET = %q, want tm_order", resp.Data.Project.TMLookupOrder)
		}
	})

	t.Run("linked project shows the profile's order, refuses edits, detach copies it", func(t *testing.T) {
		project := testutil.SeedProject(t, q, tenant.ID, "Linked", "en", []string{"fr"})
		tp := testutil.SeedTMProfile(t, q, tenant.ID, "Ordered", 0.75)
		testutil.SetTMLookupOrder(t, q, tp.SettingsProfileID, settings.TMLookupOrderTMOrder)
		testutil.LinkTMProfile(t, q, project.SettingsProfileID, tp.ID)
		path := fmt.Sprintf("/api/projects/%d/settings", project.ID)

		rec := do("GET", path, nil)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Project struct {
					TMLookupOrder string `json:"tm_lookup_order"`
				} `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if resp.Data.Project.TMLookupOrder != settings.TMLookupOrderTMOrder {
			t.Errorf("linked GET = %q, want the profile's tm_order", resp.Data.Project.TMLookupOrder)
		}

		testutil.AssertStatus(t, do("PATCH", path, map[string]any{"tm_lookup_order": "match_percent"}), http.StatusConflict)
		if got := stored(tp.SettingsProfileID); got != settings.TMLookupOrderTMOrder {
			t.Errorf("profile order changed through a linked project: %q", got)
		}

		testutil.AssertStatus(t, do("POST", fmt.Sprintf("/api/projects/%d/tms/detach", project.ID), nil), http.StatusOK)
		if got := stored(project.SettingsProfileID); got != settings.TMLookupOrderTMOrder {
			t.Errorf("detached project order = %q, want the profile's tm_order", got)
		}
	})

	t.Run("TM profile create, PATCH and save-as", func(t *testing.T) {
		rec := do("POST", "/api/tm-profiles", map[string]any{"name": "Created ordered", "tm_lookup_order": "tm_order"})
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var created struct {
			Data struct {
				ID                int64  `json:"id"`
				SettingsProfileID int64  `json:"settings_profile_id"`
				TMLookupOrder     string `json:"tm_lookup_order"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &created)
		if created.Data.TMLookupOrder != settings.TMLookupOrderTMOrder {
			t.Errorf("create response = %q, want tm_order", created.Data.TMLookupOrder)
		}
		profilePath := fmt.Sprintf("/api/tm-profiles/%d", created.Data.ID)
		testutil.AssertStatus(t, do("PATCH", profilePath, map[string]any{"tm_lookup_order": "match_percent"}), http.StatusOK)
		if got := stored(created.Data.SettingsProfileID); got != settings.TMLookupOrderMatchPercent {
			t.Errorf("after profile PATCH = %q, want match_percent", got)
		}
		testutil.AssertStatus(t, do("PATCH", profilePath, map[string]any{"tm_lookup_order": "nope"}), http.StatusBadRequest)
		testutil.AssertStatus(t, do("POST", "/api/tm-profiles", map[string]any{"name": "Bad order", "tm_lookup_order": "nope"}), http.StatusBadRequest)

		// Save-as snapshots the project's order into the new profile.
		project := testutil.SeedProject(t, q, tenant.ID, "Save as", "en", []string{"fr"})
		testutil.SetTMLookupOrder(t, q, project.SettingsProfileID, settings.TMLookupOrderTMOrder)
		rec = do("POST", fmt.Sprintf("/api/projects/%d/save-tm-profile", project.ID), map[string]any{"name": "Saved ordered"})
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var saved struct {
			Data struct {
				TMProfile struct {
					ID int64 `json:"id"`
				} `json:"tm_profile"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &saved)
		savedProfile, err := q.GetTMProfile(ctx, db.GetTMProfileParams{ID: saved.Data.TMProfile.ID, TenantID: tenant.ID})
		if err != nil {
			t.Fatalf("GetTMProfile: %v", err)
		}
		if got := stored(savedProfile.SettingsProfileID); got != settings.TMLookupOrderTMOrder {
			t.Errorf("saved-as profile order = %q, want tm_order", got)
		}
	})

	t.Run("template PATCH, then a project made from it copies the order", func(t *testing.T) {
		tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Ordered template")
		tmplPath := fmt.Sprintf("/api/templates/%d", tmpl.ID)
		testutil.AssertStatus(t, do("PATCH", tmplPath, map[string]any{"name": tmpl.Name, "tm_lookup_order": "bad"}), http.StatusBadRequest)
		testutil.AssertStatus(t, do("PATCH", tmplPath, map[string]any{"name": tmpl.Name, "tm_lookup_order": "tm_order"}), http.StatusOK)
		if got := stored(tmpl.SettingsProfileID); got != settings.TMLookupOrderTMOrder {
			t.Fatalf("template order = %q, want tm_order", got)
		}

		rec := do("POST", "/api/projects", map[string]any{
			"name": "From ordered template", "source_language": "en", "target_languages": []string{"fr"},
			"template_id": tmpl.ID,
		})
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var created struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &created)
		if got := projectOrder(created.Data.ID); got != settings.TMLookupOrderTMOrder {
			t.Errorf("project from template order = %q, want tm_order", got)
		}
	})

	t.Run("wizard setup and template-from-form carry it; unknown value is refused", func(t *testing.T) {
		base := func(name string, order string) map[string]any {
			return map[string]any{
				"name": name, "source_language": "en", "target_languages": []string{"fr"},
				"settings": map[string]any{"tm_lookup_order": order},
			}
		}
		rec := do("POST", "/api/projects", base("Wizard ordered", "tm_order"))
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var created struct {
			Data struct {
				ID                int64 `json:"id"`
				SettingsProfileID int64 `json:"settings_profile_id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &created)
		if got := projectOrder(created.Data.ID); got != settings.TMLookupOrderTMOrder {
			t.Errorf("wizard project order = %q, want tm_order", got)
		}
		testutil.AssertStatus(t, do("POST", "/api/projects", base("Wizard bad", "best")), http.StatusBadRequest)

		rec = do("POST", "/api/templates", base("Form template", "tm_order"))
		testutil.AssertStatus(t, rec, http.StatusCreated)
		testutil.DecodeJSON(t, rec, &created)
		tmplRow, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: created.Data.ID, TenantID: tenant.ID})
		if err != nil {
			t.Fatalf("GetTemplate: %v", err)
		}
		if got := stored(tmplRow.SettingsProfileID); got != settings.TMLookupOrderTMOrder {
			t.Errorf("template-from-form order = %q, want tm_order", got)
		}
		testutil.AssertStatus(t, do("POST", "/api/templates", base("Form bad", "best")), http.StatusBadRequest)
	})
}
