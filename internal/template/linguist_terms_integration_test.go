package template_test

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// "Linguists can add terms" (linguist_terms: off, pending or approved)
// travels like every other profile setting: a template keeps it, its duplicate
// and the projects made from it copy it, and a save sets it without an
// unrelated save resetting it. Anything but the three values is refused.
func TestIntegrationLinguistTermsSetting(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TermAddSetting")
	user := testutil.SeedUser(t, q, tenant.ID, "u@term-add.test", "pass")

	do := func(t *testing.T, method, path string, body any, want int) *struct {
		Data map[string]any `json:"data"`
	} {
		t.Helper()
		rec := testutil.DoRequest(t, router, method, path, body, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, want)
		var resp struct {
			Data map[string]any `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return &resp
	}
	value := func(t *testing.T, m any) string {
		t.Helper()
		v, ok := m.(map[string]any)["linguist_terms"].(string)
		if !ok {
			t.Fatalf("no linguist_terms in %v", m)
		}
		return v
	}
	templateValue := func(t *testing.T, id int64) string {
		return value(t, do(t, "GET", fmt.Sprintf("/api/templates/%d", id), nil, http.StatusOK).Data["template"])
	}
	projectValue := func(t *testing.T, id int64) string {
		return value(t, do(t, "GET", fmt.Sprintf("/api/projects/%d/settings", id), nil, http.StatusOK).Data["project"])
	}
	idOf := func(r *struct {
		Data map[string]any `json:"data"`
	}) int64 {
		return int64(r.Data["id"].(float64))
	}
	newProject := func(t *testing.T, body map[string]any) int64 {
		body["source_language"], body["target_languages"] = "en", []string{"fr"}
		return idOf(do(t, "POST", "/api/projects", body, http.StatusCreated))
	}

	tmpl := idOf(do(t, "POST", "/api/templates", map[string]any{
		"name": "No term adds", "settings": map[string]any{"linguist_terms": "off"},
		"workflow_id": testutil.LibraryWorkflow(t, q, tenant.ID),
	}, http.StatusCreated))
	if v := templateValue(t, tmpl); v != "off" {
		t.Fatalf("template linguist_terms = %q after create with off", v)
	}

	dup := idOf(do(t, "POST", fmt.Sprintf("/api/templates/%d/duplicate", tmpl), nil, http.StatusCreated))
	if v := templateValue(t, dup); v != "off" {
		t.Errorf("the duplicate has linguist_terms %q, want the source's off", v)
	}

	project := newProject(t, map[string]any{"name": "From template", "template_id": tmpl})
	if v := projectValue(t, project); v != "off" {
		t.Fatalf("project linguist_terms = %q, want off from the template", v)
	}

	settings := fmt.Sprintf("/api/projects/%d/settings", project)
	do(t, "PATCH", settings, map[string]any{"linguist_terms": "approved"}, http.StatusOK)
	if v := projectValue(t, project); v != "approved" {
		t.Errorf("linguist_terms = %q after PATCH approved", v)
	}
	do(t, "PATCH", settings, map[string]any{"auto_complete": false}, http.StatusOK)
	if v := projectValue(t, project); v != "approved" {
		t.Errorf("an unrelated PATCH changed linguist_terms to %q", v)
	}
	do(t, "PATCH", settings, map[string]any{"linguist_terms": "always"}, http.StatusBadRequest)

	plain := newProject(t, map[string]any{"name": "Plain", "workflow_id": testutil.LibraryWorkflow(t, q, tenant.ID)})
	if v := projectValue(t, plain); v != "pending" {
		t.Errorf("a new project has linguist_terms %q, want pending", v)
	}

	// The New project form's choice, without a template.
	chosen := newProject(t, map[string]any{
		"name": "Chosen off", "settings": map[string]any{"linguist_terms": "off"},
		"workflow_id": testutil.LibraryWorkflow(t, q, tenant.ID),
	})
	if v := projectValue(t, chosen); v != "off" {
		t.Errorf("project linguist_terms = %q, want the form's off", v)
	}

	// Editing the template saves it, and an unrelated edit keeps it.
	tmplPath := fmt.Sprintf("/api/templates/%d", tmpl)
	do(t, "PATCH", tmplPath, map[string]any{"linguist_terms": "approved"}, http.StatusOK)
	if v := templateValue(t, tmpl); v != "approved" {
		t.Errorf("template linguist_terms = %q after PATCH approved", v)
	}
	do(t, "PATCH", tmplPath, map[string]any{"auto_complete": false}, http.StatusOK)
	if v := templateValue(t, tmpl); v != "approved" {
		t.Errorf("an unrelated template PATCH changed linguist_terms to %q", v)
	}
	do(t, "PATCH", tmplPath, map[string]any{"linguist_terms": "yes"}, http.StatusBadRequest)
}
