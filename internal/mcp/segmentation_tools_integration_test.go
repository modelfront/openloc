package mcp_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"openloc/internal/mcp"
	"openloc/internal/segmentation"
	"openloc/internal/segmentationapi"
	"openloc/internal/testutil"
)

// Segmentation profile tools over the real protocol: every member reads,
// previews and exports; managers create, edit and delete, with the guarded
// two-call confirm; built-ins stay read-only; another tenant's profile is not
// found; a project created with a profile names it in its settings.
func TestIntegrationMCPSegmentationProfiles(t *testing.T) {
	c := newConfigFixture(t)
	asLinguist := connect(t, c.url, writeToken(t, c.fixture, c.tenantA.ID, c.linguistA.ID))

	var list mcp.SegmentationProfileList
	if isErr, text := callTool(t, asLinguist, "openloc_list_segmentation_profiles", map[string]any{"source_language": "en-GB"}, &list); isErr {
		t.Fatalf("list as a linguist: %s", text)
	}
	var english int64
	for _, s := range list.Profiles {
		if s.BuiltinKey == "builtin:en@1" {
			english = s.ProfileID
			if !s.Suggested {
				t.Errorf("the English built-in is not the suggested profile for en-GB: %+v", s)
			}
		}
		if slices.Contains(s.SourceLanguages, "de") {
			t.Errorf("an en-GB list holds %+v", s)
		}
	}
	if english == 0 {
		t.Fatalf("no English built-in in %+v", list.Profiles)
	}

	var preview mcp.SegmentationPreview
	if isErr, text := callTool(t, asLinguist, "openloc_preview_segmentation", map[string]any{
		"profile_id": english, "text": "Dr. Smith came. He left.",
	}, &preview); isErr {
		t.Fatalf("preview: %s", text)
	}
	if strings.Join(preview.Segments, "|") != "Dr. Smith came.|He left." || len(preview.Decisions) == 0 ||
		preview.Decisions[0].Source != "abbreviation" || preview.Decisions[0].Word != "Dr" {
		t.Errorf("preview = %+v", preview)
	}
	// A linguist's server has no write tools (testdata/tools_linguist.golden);
	// a read-only key never writes.
	cfgMustRefuse(t, c.asReadOnly, "openloc_create_segmentation_profile", map[string]any{"name": "x", "source_languages": []string{"en"}, "from_profile_id": english}, "write access")

	srx, err := os.ReadFile("../segmentation/testdata/srx/cascade.srx")
	if err != nil {
		t.Fatal(err)
	}
	var imp segmentation.SRXImport
	if isErr, text := callTool(t, c.asManager, "openloc_read_segmentation_srx", map[string]any{"srx": string(srx)}, &imp); isErr || len(imp.Drafts) != 3 {
		t.Fatalf("read SRX: %s %+v", text, imp)
	}
	if !strings.Contains(strings.Join(imp.Drafts[0].Warnings, " "), "look-behind is not supported") {
		t.Errorf("SRX warnings = %q", imp.Drafts[0].Warnings)
	}
	cfgMustRefuse(t, c.asManager, "openloc_read_segmentation_srx", map[string]any{"srx": "<srx"}, "not valid SRX")

	var profile mcp.SegmentationProfileDetail
	if isErr, text := callTool(t, c.asManager, "openloc_create_segmentation_profile", map[string]any{
		"name": "Legal English", "source_languages": []string{"en"}, "from_profile_id": english,
	}, &profile); isErr || profile.Builtin || profile.BasedOnProfileID == nil || *profile.BasedOnProfileID != english || profile.Abbreviations == 0 {
		t.Fatalf("create from the built-in: %s %+v", text, profile)
	}
	cfgMustRefuse(t, c.asManager, "openloc_create_segmentation_profile", map[string]any{
		"name": "Bad", "source_languages": []string{"en"},
		"definition": map[string]any{"rules": []map[string]any{{"before": "(?=x)", "after": "", "break": true}}},
	}, "look-ahead is not supported")

	// A guarded update: the first call changes nothing.
	update := map[string]any{"profile_id": profile.ProfileID, "name": "Legal English v2"}
	g := cfgPreviewOnly(t, c.asManager, "openloc_update_segmentation_profile", update)
	if !cfgEffectsContain(g, "keep their segments") {
		t.Errorf("update preview = %+v", g.Preview)
	}
	var cur mcp.SegmentationProfileDetail
	if isErr, _ := callTool(t, c.asManager, "openloc_get_segmentation_profile", map[string]any{"profile_id": profile.ProfileID}, &cur); isErr || cur.Name != "Legal English" {
		t.Errorf("after the preview the name is %q, want unchanged", cur.Name)
	}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_segmentation_profile", update, &profile); isErr || profile.Name != "Legal English v2" || profile.Abbreviations == 0 {
		t.Fatalf("update: %s %+v", text, profile)
	}
	cfgMustRefuse(t, c.asManager, "openloc_update_segmentation_profile", map[string]any{"profile_id": english, "name": "Mine"}, "read-only")
	// A project created with the profile names it; get_project_settings
	// returns it.
	var created mcp.CreateProjectResult
	if isErr, text := callTool(t, c.asManager, "openloc_create_project", map[string]any{
		"name": "Legal", "source_language": "en-GB", "target_languages": []string{"de"}, "segmentation_profile_id": profile.ProfileID,
		"workflow_id": testutil.LibraryWorkflow(t, c.q, c.tenantA.ID),
	}, &created); isErr {
		t.Fatalf("create project with the profile: %s", text)
	}
	var settings mcp.ProjectSettingsOut
	if isErr, text := callTool(t, c.asManager, "openloc_get_project_settings", map[string]any{"project_id": created.ProjectID}, &settings); isErr ||
		settings.SegmentationProfileID == nil || *settings.SegmentationProfileID != profile.ProfileID || settings.SegmentationProfileName != "Legal English v2" {
		t.Errorf("project settings: %s %+v", text, settings)
	}

	var out mcp.SegmentationSRX
	if isErr, text := callTool(t, asLinguist, "openloc_export_segmentation_srx", map[string]any{"profile_id": profile.ProfileID}, &out); isErr ||
		out.FileName != "Legal English v2.srx" || !strings.Contains(out.SRX, `languagerulename="Legal English v2"`) {
		t.Errorf("export: %s %+v", text, out.FileName)
	}

	c.refresh(t)
	for _, tool := range []string{"openloc_get_segmentation_profile", "openloc_export_segmentation_srx", "openloc_delete_segmentation_profile"} {
		cfgMustNotFind(t, c.asOwnerB, tool, map[string]any{"profile_id": profile.ProfileID})
	}
	cfgMustNotFind(t, c.asOwnerB, "openloc_update_segmentation_profile", map[string]any{"profile_id": profile.ProfileID, "name": "Stolen"})
	cfgMustNotFind(t, c.asOwnerB, "openloc_preview_segmentation", map[string]any{"profile_id": profile.ProfileID, "text": "One. Two."})
	if isErr, _ := callTool(t, c.asManager, "openloc_get_segmentation_profile", map[string]any{"profile_id": profile.ProfileID}, &cur); isErr || cur.Name != "Legal English v2" {
		t.Errorf("tenant B's calls changed tenant A's profile: %+v", cur)
	}

	g = cfgPreviewOnly(t, c.asManager, "openloc_delete_segmentation_profile", map[string]any{"profile_id": profile.ProfileID})
	if !cfgEffectsContain(g, "1 project and 0 templates use it") {
		t.Errorf("delete preview of a profile a project uses = %+v", g.Preview)
	}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_delete_segmentation_profile", map[string]any{"profile_id": profile.ProfileID}, nil); isErr {
		t.Fatalf("delete: %s", text)
	}
	if !c.softDeleted(t, "segmentation_profiles", profile.ProfileID) {
		t.Error("the deleted profile is gone for good, want a soft delete")
	}
	cfgMustNotFind(t, c.asManager, "openloc_get_segmentation_profile", map[string]any{"profile_id": profile.ProfileID})
}

// A project created through MCP names the profile that suits its language,
// with or without a template: an English template's profile gives way to the
// German built-in for a German project.
func TestIntegrationMCPCreateProject_SegmentationProfileSuitsTheLanguage(t *testing.T) {
	c := newConfigFixture(t)
	ctx := context.Background()
	svc := segmentationapi.NewService(c.q, c.pool)
	rs := segmentation.RuleSet{Rules: []segmentation.Rule{{Before: `\.`, After: `\s`, Break: true}}}
	english, err := svc.Create(ctx, c.tenantA.ID, 0, segmentationapi.Input{Name: "English house", SourceLanguages: []string{"en"}, Definition: &rs})
	if err != nil {
		t.Fatal(err)
	}
	german, err := segmentationapi.Suggest(ctx, c.q, "de")
	if err != nil {
		t.Fatal(err)
	}
	var tmpl mcp.TemplateDetail
	if isErr, text := callTool(t, c.asManager, "openloc_create_template", map[string]any{
		"name": "English", "source_language": "en", "segmentation_profile_id": english.ID,
	}, &tmpl); isErr || tmpl.SegmentationProfileID != english.ID {
		t.Fatalf("create template: %s %+v", text, tmpl)
	}
	wf := testutil.LibraryWorkflow(t, c.q, c.tenantA.ID)
	for name, args := range map[string]map[string]any{
		"from the English template": {"name": "From template", "source_language": "de", "target_languages": []string{"fr"}, "template_id": tmpl.TemplateID, "workflow_id": wf},
		"without a template":        {"name": "Plain", "source_language": "de", "target_languages": []string{"fr"}, "workflow_id": wf},
	} {
		var created mcp.CreateProjectResult
		if isErr, text := callTool(t, c.asManager, "openloc_create_project", args, &created); isErr {
			t.Fatalf("%s: %s", name, text)
		}
		var settings mcp.ProjectSettingsOut
		if isErr, text := callTool(t, c.asManager, "openloc_get_project_settings", map[string]any{"project_id": created.ProjectID}, &settings); isErr {
			t.Fatalf("%s settings: %s", name, text)
		}
		if settings.SegmentationProfileID == nil || *settings.SegmentationProfileID != german {
			t.Errorf("%s: profile = %v, want the German built-in %d", name, settings.SegmentationProfileID, german)
		}
	}
}
