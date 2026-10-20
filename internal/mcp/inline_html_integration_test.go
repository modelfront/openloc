package mcp_test

// "Strings contain HTML" (inline_html) through the MCP project settings and
// template tools: each write is checked in the database and read back by the
// tool, starting from the non-default (off).

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

func TestIntegrationMCPInlineHTML(t *testing.T) {
	c := newConfigFixture(t)
	ctx := context.Background()
	stored := func(t *testing.T, profileID int64) bool {
		t.Helper()
		p, err := c.q.GetSettingsProfile(ctx, profileID)
		if err != nil {
			t.Fatal(err)
		}
		return p.InlineHtml
	}
	type settingsOut struct {
		InlineHTML *bool `json:"inline_html"`
	}

	t.Run("project_settings", func(t *testing.T) {
		pid := c.projectA.ID
		var out settingsOut
		if isErr, text := callTool(t, c.asManager, "openloc_update_project_settings", map[string]any{
			"project_id": pid, "inline_html": false,
		}, &out); isErr {
			t.Fatalf("update_project_settings: %s", text)
		}
		if stored(t, c.projectA.SettingsProfileID) {
			t.Error("inline_html not stored off")
		}
		if out.InlineHTML == nil || *out.InlineHTML {
			t.Errorf("update returned inline_html = %v, want false", out.InlineHTML)
		}
		// Omitted keeps the stored value.
		if isErr, text := callTool(t, c.asManager, "openloc_update_project_settings", map[string]any{
			"project_id": pid, "mt_on_upload": true,
		}, nil); isErr {
			t.Fatalf("update_project_settings: %s", text)
		}
		out = settingsOut{}
		if isErr, text := callTool(t, c.asManager, "openloc_get_project_settings", map[string]any{"project_id": pid}, &out); isErr {
			t.Fatalf("get_project_settings: %s", text)
		}
		if out.InlineHTML == nil || *out.InlineHTML {
			t.Errorf("after an unrelated update, get returned inline_html = %v, want false", out.InlineHTML)
		}
	})

	t.Run("templates", func(t *testing.T) {
		c.refresh(t)
		var created struct {
			TemplateID int64 `json:"template_id"`
			InlineHTML *bool `json:"inline_html"`
		}
		if isErr, text := callTool(t, c.asManager, "openloc_create_template", map[string]any{
			"name": "Plain strings", "inline_html": false,
		}, &created); isErr {
			t.Fatalf("create_template: %s", text)
		}
		if created.InlineHTML == nil || *created.InlineHTML {
			t.Errorf("create_template returned inline_html = %v, want false", created.InlineHTML)
		}
		tmpl, err := c.q.GetTemplate(ctx, db.GetTemplateParams{ID: created.TemplateID, TenantID: c.tenantA.ID})
		if err != nil {
			t.Fatal(err)
		}
		if stored(t, tmpl.SettingsProfileID) {
			t.Error("create_template stored inline_html on")
		}
		if isErr, text := confirmedCall(t, c.asManager, "openloc_update_template", map[string]any{
			"template_id": created.TemplateID, "inline_html": true,
		}, nil); isErr {
			t.Fatalf("update_template: %s", text)
		}
		if !stored(t, tmpl.SettingsProfileID) {
			t.Error("update_template did not turn inline_html on")
		}
		var got settingsOut
		if isErr, text := callTool(t, c.asManager, "openloc_get_template", map[string]any{"template_id": created.TemplateID}, &got); isErr {
			t.Fatalf("get_template: %s", text)
		}
		if got.InlineHTML == nil || !*got.InlineHTML {
			t.Errorf("get_template returned inline_html = %v, want true", got.InlineHTML)
		}
	})
}
