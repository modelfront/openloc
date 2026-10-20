// PATCH /api/projects/{id}/settings round-trip for "Strings contain HTML"
// (inline_html). It decides how every later JSON/PO/TXT/XLSX upload is read,
// so a value that does not persist, or an unrelated save that resets it,
// changes the parse of the next file.
//
// Skipped with -short.
package project_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationProjectSettings_InlineHTML(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "InlineHTMLSettings")
	user := testutil.SeedUser(t, q, tenant.ID, "u@inline-settings.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Inline HTML Project", "en", []string{"fr"})
	settingsPath := fmt.Sprintf("/api/projects/%d/settings", project.ID)

	stored := func(t *testing.T) bool {
		t.Helper()
		p, err := q.GetSettingsProfile(testutil.Ctx(), project.SettingsProfileID)
		if err != nil {
			t.Fatalf("GetSettingsProfile: %v", err)
		}
		return p.InlineHtml
	}
	returned := func(t *testing.T) bool {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", settingsPath, nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Project struct {
					InlineHTML *bool `json:"inline_html"`
				} `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if resp.Data.Project.InlineHTML == nil {
			t.Fatal("GET /settings has no inline_html")
		}
		return *resp.Data.Project.InlineHTML
	}
	patch := func(t *testing.T, body map[string]any) {
		t.Helper()
		testutil.AssertStatus(t, testutil.DoRequest(t, router, "PATCH", settingsPath, body, user.ID, tenant.ID), http.StatusOK)
	}

	t.Run("defaults_to_on", func(t *testing.T) {
		if !stored(t) || !returned(t) {
			t.Error("inline_html is off on a new project, want on")
		}
	})

	t.Run("off_round_trips", func(t *testing.T) {
		patch(t, map[string]any{"inline_html": false})
		if stored(t) {
			t.Error("stored inline_html = true after PATCH false")
		}
		if returned(t) {
			t.Error("GET returned inline_html = true after PATCH false")
		}
	})

	// Absent keeps the stored value: a full-replace update would switch it
	// back on whenever another setting is saved.
	t.Run("absent_keeps_stored", func(t *testing.T) {
		patch(t, map[string]any{"fuzzy_threshold": 0.8, "auto_complete": false})
		if stored(t) {
			t.Error("an unrelated save turned inline_html back on")
		}
	})

	t.Run("on_again", func(t *testing.T) {
		patch(t, map[string]any{"inline_html": true})
		if !stored(t) || !returned(t) {
			t.Error("inline_html did not turn back on")
		}
	})

	t.Run("change_is_recorded", func(t *testing.T) {
		n := testutil.CountRows(t, pool, `SELECT count(*) FROM project_events
			WHERE project_id = $1 AND event_type = 'project.settings_updated' AND details->'changes' ? 'inline_html'`, project.ID)
		if n != 2 {
			t.Errorf("settings events carrying inline_html = %d, want 2 (off, then on)", n)
		}
	})
}

// POST /api/projects carries the wizard's choice; omitted, a new project
// reads its strings as HTML.
func TestIntegrationProjectCreate_InlineHTML(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "InlineHTMLCreate")
	user := testutil.SeedUser(t, q, tenant.ID, "u@inline-create.test", "pass")

	for _, c := range []struct {
		name     string
		settings map[string]any
		want     bool
	}{
		{"off", map[string]any{"inline_html": false}, false},
		{"omitted", map[string]any{"mt_on_upload": false}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
				"name": "Create " + c.name, "source_language": "en", "target_languages": []string{"fr"}, "settings": c.settings,
			}, user.ID, tenant.ID)
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
			p, err := q.GetSettingsProfile(testutil.Ctx(), project.SettingsProfileID)
			if err != nil {
				t.Fatal(err)
			}
			if p.InlineHtml != c.want {
				t.Errorf("stored inline_html = %v, want %v", p.InlineHtml, c.want)
			}
		})
	}
}
