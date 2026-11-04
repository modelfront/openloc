package mcp_test

// Filter profiles through the MCP project settings and template tools: each
// write is checked in the database and read back by the tool. inline_html,
// the older switch, still picks the string formats' built-ins.

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/filterprofile"
	"openloc/internal/mcp"
	"openloc/internal/parser"
	"openloc/internal/testutil"
)

func TestIntegrationMCPFilterProfiles(t *testing.T) {
	c := newConfigFixture(t)
	ctx := context.Background()
	plainJSON := testutil.BuiltinFilterProfile(t, c.q, "builtin:json_i18n-plain@1")
	plainXLSX := testutil.BuiltinFilterProfile(t, c.q, "builtin:xlsx-plain@1")
	type settingsOut struct {
		FilterProfiles []filterprofile.Choice `json:"filter_profiles"`
	}

	t.Run("project_settings", func(t *testing.T) {
		pid := c.projectA.ID
		var out settingsOut
		if isErr, text := callTool(t, c.asManager, "openloc_update_project_settings", map[string]any{
			"project_id": pid, "filter_profile_ids": map[string]any{"json_i18n": plainJSON},
		}, &out); isErr {
			t.Fatalf("update_project_settings: %s", text)
		}
		if got := testutil.FilterChoices(t, c.q, c.projectA.SettingsProfileID)["json_i18n"]; got != plainJSON {
			t.Errorf("stored json_i18n = %d, want %d", got, plainJSON)
		}
		if got := testutil.ChoiceIDs(out.FilterProfiles)["json_i18n"]; got != plainJSON {
			t.Errorf("update returned json_i18n = %d, want %d", got, plainJSON)
		}
		// 0 goes back to the Default.
		if isErr, text := callTool(t, c.asManager, "openloc_update_project_settings", map[string]any{
			"project_id": pid, "filter_profile_ids": map[string]any{"json_i18n": 0},
		}, nil); isErr {
			t.Fatalf("update_project_settings: %s", text)
		}
		if got, ok := testutil.FilterChoices(t, c.q, c.projectA.SettingsProfileID)["json_i18n"]; ok {
			t.Errorf("json_i18n still names %d after 0", got)
		}
	})

	t.Run("inline_html_maps_to_builtins", func(t *testing.T) {
		pid := c.projectA.ID
		if isErr, text := callTool(t, c.asManager, "openloc_update_project_settings", map[string]any{
			"project_id": pid, "inline_html": false, "filter_profile_ids": map[string]any{"po": 0},
		}, nil); isErr {
			t.Fatalf("update_project_settings: %s", text)
		}
		got := testutil.FilterChoices(t, c.q, c.projectA.SettingsProfileID)
		if got["json_i18n"] != plainJSON || got["xlsx"] != plainXLSX {
			t.Errorf("inline_html false stored %v, want the plain built-ins", got)
		}
		if _, ok := got["po"]; ok {
			t.Errorf("filter_profile_ids did not win for po: %v", got)
		}
		if isErr, text := callTool(t, c.asManager, "openloc_update_project_settings", map[string]any{
			"project_id": pid, "inline_html": true,
		}, nil); isErr {
			t.Fatalf("update_project_settings: %s", text)
		}
		if got := testutil.FilterChoices(t, c.q, c.projectA.SettingsProfileID); len(got) != 0 {
			t.Errorf("inline_html true left %v, want every Default", got)
		}
	})

	t.Run("templates", func(t *testing.T) {
		c.refresh(t)
		var created struct {
			TemplateID     int64                  `json:"template_id"`
			FilterProfiles []filterprofile.Choice `json:"filter_profiles"`
		}
		if isErr, text := callTool(t, c.asManager, "openloc_create_template", map[string]any{
			"name": "Plain strings", "filter_profile_ids": map[string]any{"xlsx": plainXLSX},
			"workflow_id": testutil.LibraryWorkflow(t, c.q, c.tenantA.ID),
		}, &created); isErr {
			t.Fatalf("create_template: %s", text)
		}
		if got := testutil.ChoiceIDs(created.FilterProfiles)["xlsx"]; got != plainXLSX {
			t.Errorf("create_template returned xlsx = %d, want %d", got, plainXLSX)
		}
		tmpl, err := c.q.GetTemplate(ctx, db.GetTemplateParams{ID: created.TemplateID, TenantID: c.tenantA.ID})
		if err != nil {
			t.Fatal(err)
		}
		if got := testutil.FilterChoices(t, c.q, tmpl.SettingsProfileID)["xlsx"]; got != plainXLSX {
			t.Errorf("create_template stored xlsx = %d, want %d", got, plainXLSX)
		}
		if isErr, text := confirmedCall(t, c.asManager, "openloc_update_template", map[string]any{
			"template_id": created.TemplateID, "filter_profile_ids": map[string]any{"xlsx": 0},
		}, nil); isErr {
			t.Fatalf("update_template: %s", text)
		}
		var got settingsOut
		if isErr, text := callTool(t, c.asManager, "openloc_get_template", map[string]any{"template_id": created.TemplateID}, &got); isErr {
			t.Fatalf("get_template: %s", text)
		}
		if _, ok := testutil.ChoiceIDs(got.FilterProfiles)["xlsx"]; ok {
			t.Errorf("get_template after 0 returned %v, want no xlsx pick", got.FilterProfiles)
		}
	})

	t.Run("inline_html_on_template_tools", func(t *testing.T) {
		c.refresh(t)
		var created struct {
			TemplateID int64 `json:"template_id"`
		}
		if isErr, text := callTool(t, c.asManager, "openloc_create_template", map[string]any{
			"name": "Plain via inline_html", "inline_html": false, "workflow_id": testutil.LibraryWorkflow(t, c.q, c.tenantA.ID),
		}, &created); isErr {
			t.Fatalf("create_template: %s", text)
		}
		tmpl, err := c.q.GetTemplate(ctx, db.GetTemplateParams{ID: created.TemplateID, TenantID: c.tenantA.ID})
		if err != nil {
			t.Fatal(err)
		}
		if got := testutil.FilterChoices(t, c.q, tmpl.SettingsProfileID); got["json_i18n"] != plainJSON || got["xlsx"] != plainXLSX {
			t.Errorf("create_template inline_html false stored %v, want the plain built-ins", got)
		}
		if isErr, text := confirmedCall(t, c.asManager, "openloc_update_template", map[string]any{
			"template_id": created.TemplateID, "inline_html": true,
		}, nil); isErr {
			t.Fatalf("update_template: %s", text)
		}
		if got := testutil.FilterChoices(t, c.q, tmpl.SettingsProfileID); len(got) != 0 {
			t.Errorf("update_template inline_html true left %v, want every Default", got)
		}
	})

	t.Run("org_default", func(t *testing.T) {
		c.refresh(t)
		cfgMustRefuse(t, c.asReadOnly, "openloc_set_filter_profile_default", map[string]any{"format": "json_i18n", "profile_id": plainJSON}, "write access")
		var set mcp.FilterProfileDefaults
		if isErr, text := callTool(t, c.asManager, "openloc_set_filter_profile_default", map[string]any{
			"format": "json_i18n", "profile_id": plainJSON,
		}, &set); isErr {
			t.Fatalf("set_filter_profile_default: %s", text)
		}
		if len(set.Defaults) != 1 || set.Defaults[0].ProfileID != plainJSON {
			t.Fatalf("set_filter_profile_default returned %+v", set.Defaults)
		}
		t.Cleanup(func() {
			if err := filterprofile.SetOrgDefault(ctx, c.q, c.tenantA.ID, 0, "json_i18n", nil); err != nil {
				t.Error(err)
			}
		})
		var created struct {
			TemplateID int64 `json:"template_id"`
		}
		if isErr, text := callTool(t, c.asManager, "openloc_create_template", map[string]any{
			"name": "Defaulted", "workflow_id": testutil.LibraryWorkflow(t, c.q, c.tenantA.ID),
		}, &created); isErr {
			t.Fatalf("create_template: %s", text)
		}
		tmpl, err := c.q.GetTemplate(ctx, db.GetTemplateParams{ID: created.TemplateID, TenantID: c.tenantA.ID})
		if err != nil {
			t.Fatal(err)
		}
		if got := testutil.FilterChoices(t, c.q, tmpl.SettingsProfileID)["json_i18n"]; got != plainJSON {
			t.Errorf("a template created without picks stored json_i18n = %d, want the org default %d", got, plainJSON)
		}
	})
}

// Filter profile tools over the real protocol: every member reads; managers
// create, edit and delete, with the guarded two-call confirm; built-ins stay
// read-only; another tenant's profile is not found.
func TestIntegrationMCPFilterProfileTools(t *testing.T) {
	c := newConfigFixture(t)
	asLinguist := connect(t, c.url, writeToken(t, c.fixture, c.tenantA.ID, c.linguistA.ID))

	var list mcp.FilterProfileList
	if isErr, text := callTool(t, asLinguist, "openloc_list_filter_profiles", map[string]any{"format": "xlsx"}, &list); isErr {
		t.Fatalf("list as a linguist: %s", text)
	}
	var xlsxDefault int64
	for _, p := range list.Profiles {
		if p.Format != "xlsx" {
			t.Errorf("an xlsx list holds %+v", p)
		}
		if p.BuiltinKey == "builtin:xlsx-default@1" {
			xlsxDefault = p.ProfileID
		}
	}
	if xlsxDefault == 0 {
		t.Fatalf("no xlsx Default in %+v", list.Profiles)
	}
	cfgMustRefuse(t, c.asReadOnly, "openloc_create_filter_profile", map[string]any{"name": "x", "from_profile_id": xlsxDefault}, "write access")

	var created mcp.FilterProfileDetail
	if isErr, text := callTool(t, c.asManager, "openloc_create_filter_profile", map[string]any{
		"name": "Column B", "from_profile_id": xlsxDefault, "options": map[string]any{"xlsx": map[string]any{"columns": []string{"b"}, "target_columns": []map[string]string{{"language": "fr", "column": "c"}}}},
	}, &created); isErr {
		t.Fatalf("create: %s", text)
	}
	if created.Format != "xlsx" || created.Options.XLSX == nil || len(created.Options.XLSX.Columns) != 1 || created.Options.XLSX.Columns[0] != "B" ||
		len(created.Options.XLSX.TargetColumns) != 1 || created.Options.XLSX.TargetColumns[0] != (parser.XLSXTargetColumn{Language: "fr", Column: "C"}) {
		t.Errorf("created = %+v", created)
	}
	if isErr, _ := confirmedCall(t, c.asManager, "openloc_update_filter_profile", map[string]any{"profile_id": xlsxDefault, "name": "Mine"}, nil); !isErr {
		t.Error("updating a built-in succeeded")
	}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_filter_profile", map[string]any{"profile_id": created.ProfileID, "name": "Column B only"}, nil); isErr {
		t.Fatalf("update: %s", text)
	}
	var cur mcp.FilterProfileDetail
	if isErr, _ := callTool(t, asLinguist, "openloc_get_filter_profile", map[string]any{"profile_id": created.ProfileID}, &cur); isErr ||
		cur.Name != "Column B only" || cur.Options.XLSX == nil || len(cur.Options.XLSX.Columns) != 1 {
		t.Errorf("after a name-only update = %+v, want the new name and the same options", cur)
	}

	c.refresh(t)
	for _, tool := range []string{"openloc_get_filter_profile", "openloc_delete_filter_profile"} {
		cfgMustNotFind(t, c.asOwnerB, tool, map[string]any{"profile_id": created.ProfileID})
	}
	cfgMustNotFind(t, c.asOwnerB, "openloc_update_filter_profile", map[string]any{"profile_id": created.ProfileID, "name": "Stolen"})

	// The org default is refused at the preview, before anyone confirms.
	if err := filterprofile.SetOrgDefault(context.Background(), c.q, c.tenantA.ID, 0, "xlsx", &created.ProfileID); err != nil {
		t.Fatal(err)
	}
	cfgMustRefuse(t, c.asManager, "openloc_delete_filter_profile", map[string]any{"profile_id": created.ProfileID}, "organization's default")
	if err := filterprofile.SetOrgDefault(context.Background(), c.q, c.tenantA.ID, 0, "xlsx", nil); err != nil {
		t.Fatal(err)
	}

	if isErr, text := confirmedCall(t, c.asManager, "openloc_delete_filter_profile", map[string]any{"profile_id": created.ProfileID}, nil); isErr {
		t.Fatalf("delete: %s", text)
	}
	if !c.softDeleted(t, "filter_profiles", created.ProfileID) {
		t.Error("the deleted profile is gone for good, want a soft delete")
	}
	cfgMustNotFind(t, c.asManager, "openloc_get_filter_profile", map[string]any{"profile_id": created.ProfileID})
}
