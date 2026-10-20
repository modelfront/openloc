package mcp_test

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type cfgTemplateOut struct {
	TemplateID      int64    `json:"template_id"`
	Name            string   `json:"name"`
	ProjectName     string   `json:"project_name"`
	SourceLanguage  string   `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	QAProfileID     int64    `json:"qa_profile_id"`
	MTRouterID      int64    `json:"mt_router_id"`
	MtOnUpload      bool     `json:"mt_on_upload"`
	FuzzyThreshold  float32  `json:"fuzzy_threshold"`
	WorkflowSteps   []string `json:"workflow_steps"`
	ProjectsCreated int64    `json:"projects_created"`
}

type cfgQAProfileOut struct {
	QAProfileID int64  `json:"qa_profile_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Rules       []struct {
		CheckType string         `json:"check_type"`
		Enabled   bool           `json:"enabled"`
		Severity  string         `json:"severity"`
		Config    map[string]any `json:"config"`
	} `json:"rules"`
	UsedBy int64 `json:"used_by"`
}

func TestIntegrationMCPConfigTemplates(t *testing.T) {
	c := newConfigFixture(t)
	ctx := context.Background()
	engine := testutil.SeedMTEngine(t, c.q, c.tenantA.ID, "Cfg tpl engine", "mock")
	routerID := testutil.SeedMTRouter(t, c.q, c.tenantA.ID, "Cfg tpl router", &engine.ID, nil)

	// A project manager manages templates.
	var created cfgTemplateOut
	if isErr, text := callTool(t, c.asManager, "openloc_create_template", map[string]any{
		"name": "Cfg legal", "source_language": "en", "target_languages": []string{"de", "fr"},
		"mt_router_id": routerID, "mt_on_upload": false,
	}, &created); isErr {
		t.Fatalf("create_template: %s", text)
	}
	if created.Name != "Cfg legal" || created.ProjectName != "Cfg legal" || created.MTRouterID != routerID ||
		created.MtOnUpload || len(created.TargetLanguages) != 2 || len(created.WorkflowSteps) != 2 {
		t.Errorf("create_template = %+v; want the defaults filled and Translation/Review steps", created)
	}
	cfgMustRefuse(t, c.asManager, "openloc_create_template", map[string]any{"name": " "}, "name is required")
	cfgMustRefuse(t, c.asManager, "openloc_create_template", map[string]any{"name": "Bad", "source_language": "en", "target_languages": []string{"en"}}, "same as source")
	cfgMustRefuse(t, c.asReadOnly, "openloc_create_template", map[string]any{"name": "RO"}, "write access")

	var list struct {
		Templates []cfgTemplateOut `json:"templates"`
		HasMore   bool             `json:"has_more"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_list_templates", map[string]any{"query": "legal"}, &list); isErr {
		t.Fatalf("list_templates: %s", text)
	}
	if len(list.Templates) != 1 || list.Templates[0].TemplateID != created.TemplateID {
		t.Errorf("list_templates = %+v", list)
	}

	var dup cfgTemplateOut
	if isErr, text := callTool(t, c.asManager, "openloc_duplicate_template", map[string]any{"template_id": created.TemplateID}, &dup); isErr {
		t.Fatalf("duplicate_template: %s", text)
	}
	if dup.Name != "Copy of Cfg legal" || dup.MTRouterID != routerID || dup.TemplateID == created.TemplateID {
		t.Errorf("duplicate = %+v", dup)
	}

	// A project made from the template shows up in the preview, and does not
	// change when the template does.
	if _, err := c.q.SetProjectTemplate(ctx, db.SetProjectTemplateParams{ID: c.projectA.ID, TemplateID: &created.TemplateID, TenantID: c.tenantA.ID}); err != nil {
		t.Fatalf("link project to template: %v", err)
	}
	update := map[string]any{"template_id": created.TemplateID, "name": "Cfg legal v2", "mt_router_id": 0, "fuzzy_threshold": 0.9}
	c.refresh(t)
	g := cfgPreviewOnly(t, c.asManager, "openloc_update_template", update)
	if !strings.Contains(g.Preview.Summary, "Cfg legal") || !cfgEffectsContain(g, "1 project") {
		t.Errorf("template preview should name it and count its project: %+v", g.Preview)
	}
	if cur, _ := c.q.GetTemplate(ctx, db.GetTemplateParams{ID: created.TemplateID, TenantID: c.tenantA.ID}); cur.Name != "Cfg legal" {
		t.Fatalf("the preview renamed the template: %+v", cur)
	}
	var updated cfgTemplateOut
	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_template", update, &updated); isErr {
		t.Fatalf("update_template: %s", text)
	}
	if updated.Name != "Cfg legal v2" || updated.MTRouterID != 0 || updated.FuzzyThreshold < 0.89 || updated.ProjectsCreated != 1 {
		t.Errorf("after update = %+v; 0 must clear the router", updated)
	}
	if len(updated.TargetLanguages) != 2 || updated.SourceLanguage != "en" {
		t.Errorf("omitted fields must keep their value: %+v", updated)
	}

	// Scoring belongs to a linked TM profile, and the refusal says where to go.
	tmp := testutil.SeedTMProfile(t, c.q, c.tenantA.ID, "Cfg TM profile", 0.8)
	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_template", map[string]any{"template_id": created.TemplateID, "tm_profile_id": tmp.ID}, nil); isErr {
		t.Fatalf("link TM profile: %s", text)
	}
	cfgMustRefuse(t, c.asManager, "openloc_update_template", map[string]any{"template_id": created.TemplateID, "fuzzy_threshold": 0.5}, "follows a TM profile")

	c.refresh(t)
	cfgMustNotFind(t, c.asOwnerB, "openloc_get_template", map[string]any{"template_id": created.TemplateID})
	cfgMustNotFind(t, c.asOwnerB, "openloc_update_template", map[string]any{"template_id": created.TemplateID, "name": "stolen"})
	cfgMustNotFind(t, c.asOwnerB, "openloc_duplicate_template", map[string]any{"template_id": created.TemplateID})

	// Tenant B's new template may not name any of tenant A's ids, one field per
	// call; tenant A's template takes all five. The wording comes from the
	// settings checks the REST form shares. The empty list below shows nothing
	// was created for tenant B.
	customer, err := c.q.CreateCustomer(ctx, db.CreateCustomerParams{TenantID: c.tenantA.ID, Name: "Cfg customer"})
	if err != nil {
		t.Fatal(err)
	}
	qaProfile, err := c.q.CreateQAProfile(ctx, db.CreateQAProfileParams{TenantID: c.tenantA.ID, Name: "Cfg tpl QA"})
	if err != nil {
		t.Fatal(err)
	}
	step := testutil.SeedStepDefinition(t, c.q, c.tenantA.ID, "Cfg tpl step", "human", "linguist", nil)
	var flow struct {
		WorkflowID int64 `json:"workflow_id"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_create_workflow", map[string]any{"name": "Cfg tpl flow", "step_definition_ids": []int64{step.ID}}, &flow); isErr {
		t.Fatalf("create_workflow: %s", text)
	}
	own := map[string]any{"name": "Cfg all own"}
	for field, foreign := range map[string]struct {
		id   int64
		want string
	}{
		"customer_id":   {customer.ID, "customer not found in this tenant"},
		"workflow_id":   {flow.WorkflowID, "workflow not found in this tenant"},
		"mt_router_id":  {routerID, "MT router not found in this tenant"},
		"qa_profile_id": {qaProfile.ID, "QA profile not found in this tenant"},
		"tm_profile_id": {tmp.ID, "TM profile not found in this tenant"},
	} {
		cfgMustRefuse(t, c.asOwnerB, "openloc_create_template", map[string]any{"name": "Cfg stolen " + field, field: foreign.id}, foreign.want)
		own[field] = foreign.id
	}
	var ownTpl cfgTemplateOut
	if isErr, text := callTool(t, c.asManager, "openloc_create_template", own, &ownTpl); isErr || ownTpl.MTRouterID != routerID || ownTpl.QAProfileID != qaProfile.ID {
		t.Errorf("create_template with tenant A's own ids: isErr=%v %q %+v", isErr, text, ownTpl)
	}
	var otherList struct {
		Templates []cfgTemplateOut `json:"templates"`
	}
	if isErr, text := callTool(t, c.asOwnerB, "openloc_list_templates", map[string]any{}, &otherList); isErr || len(otherList.Templates) != 0 {
		t.Errorf("another tenant listed templates: %s %+v", text, otherList)
	}
}

func TestIntegrationMCPConfigQAProfiles(t *testing.T) {
	c := newConfigFixture(t)
	ctx := context.Background()

	var p cfgQAProfileOut
	if isErr, text := callTool(t, c.asManager, "openloc_create_qa_profile", map[string]any{
		"name": "Cfg strict", "description": "for legal",
		"rules": []map[string]any{{"check_type": "tag_checks"}, {"check_type": "punctuation", "severity": "warning", "enabled": false}},
	}, &p); isErr {
		t.Fatalf("create_qa_profile: %s", text)
	}
	if p.Name != "Cfg strict" || len(p.Rules) != 2 {
		t.Fatalf("create_qa_profile = %+v", p)
	}
	rules, _ := c.q.ListQARules(ctx, p.QAProfileID)
	if len(rules) != 2 || rules[0].CheckType != "punctuation" || rules[0].Enabled || rules[0].Severity != "warning" || !rules[1].Enabled || rules[1].Severity != "error" {
		t.Errorf("stored rules = %+v; defaults are enabled + error", rules)
	}
	// A bad rule rolls the whole create back.
	cfgMustRefuse(t, c.asManager, "openloc_create_qa_profile", map[string]any{
		"name": "Cfg broken", "rules": []map[string]any{{"check_type": "tag_checks"}, {"check_type": "spelling"}},
	}, "rule 2: invalid check_type")
	var list struct {
		Profiles []cfgQAProfileOut `json:"profiles"`
	}
	if isErr, text := callTool(t, c.asManager, "openloc_list_qa_profiles", map[string]any{}, &list); isErr || len(list.Profiles) != 1 {
		t.Fatalf("list_qa_profiles after a refused create: %s %+v", text, list)
	}

	// The template uses the profile: the preview counts it and the delete is refused.
	tpl := testutil.SeedTemplate(t, c.q, c.tenantA.ID, "Cfg QA template")
	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_template", map[string]any{"template_id": tpl.ID, "qa_profile_id": p.QAProfileID}, nil); isErr {
		t.Fatalf("attach QA profile: %s", text)
	}
	upsert := map[string]any{"qa_profile_id": p.QAProfileID, "rules": []map[string]any{
		{"check_type": "punctuation", "enabled": true, "severity": "info"},
		{"check_type": "number_consistency", "config": map[string]any{"ignore_dates": true}},
	}}
	c.refresh(t)
	g := cfgPreviewOnly(t, c.asManager, "openloc_upsert_qa_rules", upsert)
	if !strings.Contains(g.Preview.Summary, "2 rules") || !cfgEffectsContain(g, "1 settings profile") {
		t.Errorf("rules preview = %+v", g.Preview)
	}
	if rules, _ := c.q.ListQARules(ctx, p.QAProfileID); len(rules) != 2 {
		t.Fatalf("the preview wrote rules: %+v", rules)
	}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_upsert_qa_rules", upsert, &p); isErr {
		t.Fatalf("upsert_qa_rules: %s", text)
	}
	byType := map[string]db.QaRule{}
	rules, _ = c.q.ListQARules(ctx, p.QAProfileID)
	for _, r := range rules {
		byType[r.CheckType] = r
	}
	if len(rules) != 3 || !byType["punctuation"].Enabled || byType["punctuation"].Severity != "info" ||
		!strings.Contains(string(byType["number_consistency"].Config), "ignore_dates") || !byType["tag_checks"].Enabled {
		t.Errorf("rules after upsert = %+v; unlisted rules stay", rules)
	}
	cfgMustRefuse(t, c.asManager, "openloc_upsert_qa_rules", map[string]any{"qa_profile_id": p.QAProfileID, "rules": []map[string]any{
		{"check_type": "punctuation"}, {"check_type": "punctuation"},
	}}, "listed twice")

	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_qa_profile", map[string]any{"qa_profile_id": p.QAProfileID, "name": "Cfg strict v2"}, &p); isErr || p.Name != "Cfg strict v2" || p.UsedBy != 1 {
		t.Fatalf("update_qa_profile: %s %+v", text, p)
	}
	cfgMustRefuse(t, c.asManager, "openloc_update_qa_profile", map[string]any{"qa_profile_id": p.QAProfileID, "name": ""}, "name is required")
	cfgMustRefuse(t, c.asManager, "openloc_delete_qa_profile", map[string]any{"qa_profile_id": p.QAProfileID}, "use this QA profile")

	c.refresh(t)
	cfgMustNotFind(t, c.asOwnerB, "openloc_get_qa_profile", map[string]any{"qa_profile_id": p.QAProfileID})
	// The same call shape tenant A confirmed above.
	cfgMustNotFind(t, c.asOwnerB, "openloc_update_qa_profile", map[string]any{"qa_profile_id": p.QAProfileID, "name": "Cfg stolen"})
	if cur, err := c.q.GetQAProfile(ctx, db.GetQAProfileParams{ID: p.QAProfileID, TenantID: c.tenantA.ID}); err != nil || cur.Name != "Cfg strict v2" {
		t.Errorf("tenant B's update renamed tenant A's QA profile: %+v (err %v)", cur, err)
	}
	cfgMustNotFind(t, c.asOwnerB, "openloc_upsert_qa_rules", upsert)
	cfgMustNotFind(t, c.asOwnerB, "openloc_delete_qa_profile", map[string]any{"qa_profile_id": p.QAProfileID})
	cfgMustRefuse(t, c.asReadOnly, "openloc_upsert_qa_rules", upsert, "write access")

	// Detached, it deletes softly and leaves the list.
	if isErr, text := confirmedCall(t, c.asManager, "openloc_update_template", map[string]any{"template_id": tpl.ID, "qa_profile_id": 0}, nil); isErr {
		t.Fatalf("detach QA profile: %s", text)
	}
	cfgPreviewOnly(t, c.asManager, "openloc_delete_qa_profile", map[string]any{"qa_profile_id": p.QAProfileID})
	if c.softDeleted(t, "qa_profiles", p.QAProfileID) {
		t.Fatal("the delete preview deleted the profile")
	}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_delete_qa_profile", map[string]any{"qa_profile_id": p.QAProfileID}, nil); isErr {
		t.Fatalf("delete_qa_profile: %s", text)
	}
	if !c.softDeleted(t, "qa_profiles", p.QAProfileID) {
		t.Error("the profile should remain with deleted_at set")
	}
	cfgMustNotFind(t, c.asManager, "openloc_get_qa_profile", map[string]any{"qa_profile_id": p.QAProfileID})
}
