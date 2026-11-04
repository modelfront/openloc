package mcp_test

// Settings profile options through the MCP project settings and template
// tools: "Linguists can add terms" (linguist_terms, off / pending / approved).
// Each write is checked in the database and read back by the tool, starting
// from a non-default value. The older inline_html switch maps to filter
// profiles (filter_profile_integration_test.go).

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationMCPProfileSwitches(t *testing.T) {
	for _, sw := range []struct {
		key string
		// off is a non-default value, on another one; stored reads the column.
		off, on any
		stored  func(db.SettingsProfile) any
	}{
		{"linguist_terms", "off", "approved", func(p db.SettingsProfile) any { return p.LinguistTerms }},
	} {
		t.Run(sw.key, func(t *testing.T) {
			c := newConfigFixture(t)
			ctx := context.Background()
			stored := func(t *testing.T, profileID int64) any {
				t.Helper()
				p, err := c.q.GetSettingsProfile(ctx, profileID)
				if err != nil {
					t.Fatal(err)
				}
				return sw.stored(p)
			}

			t.Run("project_settings", func(t *testing.T) {
				pid := c.projectA.ID
				var out map[string]any
				if isErr, text := callTool(t, c.asManager, "openloc_update_project_settings", map[string]any{
					"project_id": pid, sw.key: sw.off,
				}, &out); isErr {
					t.Fatalf("update_project_settings: %s", text)
				}
				if v := stored(t, c.projectA.SettingsProfileID); v != sw.off {
					t.Errorf("%s stored %v, want %v", sw.key, v, sw.off)
				}
				if v := out[sw.key]; v != sw.off {
					t.Errorf("update returned %s = %v, want %v", sw.key, v, sw.off)
				}
				// Omitted keeps the stored value.
				if isErr, text := callTool(t, c.asManager, "openloc_update_project_settings", map[string]any{
					"project_id": pid, "mt_on_upload": true,
				}, nil); isErr {
					t.Fatalf("update_project_settings: %s", text)
				}
				out = nil
				if isErr, text := callTool(t, c.asManager, "openloc_get_project_settings", map[string]any{"project_id": pid}, &out); isErr {
					t.Fatalf("get_project_settings: %s", text)
				}
				if v := out[sw.key]; v != sw.off {
					t.Errorf("after an unrelated update, get returned %s = %v, want %v", sw.key, v, sw.off)
				}
			})

			t.Run("templates", func(t *testing.T) {
				c.refresh(t)
				var created map[string]any
				if isErr, text := callTool(t, c.asManager, "openloc_create_template", map[string]any{
					"name": "Switched off", sw.key: sw.off, "workflow_id": testutil.LibraryWorkflow(t, c.q, c.tenantA.ID),
				}, &created); isErr {
					t.Fatalf("create_template: %s", text)
				}
				if v := created[sw.key]; v != sw.off {
					t.Errorf("create_template returned %s = %v, want %v", sw.key, v, sw.off)
				}
				id := int64(created["template_id"].(float64))
				tmpl, err := c.q.GetTemplate(ctx, db.GetTemplateParams{ID: id, TenantID: c.tenantA.ID})
				if err != nil {
					t.Fatal(err)
				}
				if v := stored(t, tmpl.SettingsProfileID); v != sw.off {
					t.Errorf("create_template stored %s = %v, want %v", sw.key, v, sw.off)
				}
				if isErr, text := confirmedCall(t, c.asManager, "openloc_update_template", map[string]any{
					"template_id": id, sw.key: sw.on,
				}, nil); isErr {
					t.Fatalf("update_template: %s", text)
				}
				if v := stored(t, tmpl.SettingsProfileID); v != sw.on {
					t.Errorf("update_template stored %s = %v, want %v", sw.key, v, sw.on)
				}
				var got map[string]any
				if isErr, text := callTool(t, c.asManager, "openloc_get_template", map[string]any{"template_id": id}, &got); isErr {
					t.Fatalf("get_template: %s", text)
				}
				if v := got[sw.key]; v != sw.on {
					t.Errorf("get_template returned %s = %v, want %v", sw.key, v, sw.on)
				}
			})
		})
	}
}
