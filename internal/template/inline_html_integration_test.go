package template_test

// "Strings contain HTML" (inline_html) on a template: created, edited and
// read through the API, carried by a duplicate and handed to a project made
// from it. Each check starts from the non-default (off), because at the
// column default every copy would read true without carrying anything.

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

func TestIntegrationTemplateInlineHTML(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "InlineHTMLTemplate")
	user := testutil.SeedUser(t, q, tenant.ID, "u@inline-template.test", "pass")

	type templateBody struct {
		ID         int64 `json:"id"`
		InlineHTML *bool `json:"inline_html"`
	}
	get := func(t *testing.T, id int64) bool {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d", id), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Template templateBody `json:"template"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if resp.Data.Template.InlineHTML == nil {
			t.Fatal("GET /api/templates/{id} has no inline_html")
		}
		return *resp.Data.Template.InlineHTML
	}
	patch := func(t *testing.T, id int64, body map[string]any) {
		t.Helper()
		testutil.AssertStatus(t, testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", id), body, user.ID, tenant.ID), http.StatusOK)
	}

	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Plain strings", "settings": map[string]any{"inline_html": false},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct {
		Data templateBody `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &created)
	id := created.Data.ID

	t.Run("create_stores_off", func(t *testing.T) {
		if get(t, id) {
			t.Error("inline_html = true after create with false")
		}
	})

	t.Run("patch_round_trips_and_absent_keeps", func(t *testing.T) {
		patch(t, id, map[string]any{"inline_html": true})
		if !get(t, id) {
			t.Error("inline_html = false after PATCH true")
		}
		patch(t, id, map[string]any{"inline_html": false})
		patch(t, id, map[string]any{"auto_complete": false})
		if get(t, id) {
			t.Error("an unrelated PATCH turned inline_html back on")
		}
	})

	t.Run("duplicate_carries_it", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/templates/%d/duplicate", id), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var dup struct {
			Data templateBody `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &dup)
		if get(t, dup.Data.ID) {
			t.Error("the duplicate has inline_html on, want the source's off")
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
					InlineHTML *bool `json:"inline_html"`
				} `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &settings)
		if settings.Data.Project.InlineHTML == nil || *settings.Data.Project.InlineHTML {
			t.Errorf("project inline_html = %v, want false from the template", settings.Data.Project.InlineHTML)
		}
	})
}
