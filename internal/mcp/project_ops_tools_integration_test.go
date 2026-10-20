package mcp_test

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Project tools over the real
// protocol: each asserts the database, not just the response.

type projectSettingsOut struct {
	ProjectID      int64   `json:"project_id"`
	FuzzyThreshold float32 `json:"fuzzy_threshold"`
	MTOnUpload     bool    `json:"mt_on_upload"`
	QAProfileID    *int64  `json:"qa_profile_id"`
	TMs            []struct {
		TMID int64  `json:"tm_id"`
		Mode string `json:"mode"`
	} `json:"tms"`
	TermBases []struct {
		TermBaseID int64 `json:"term_base_id"`
	} `json:"term_bases"`
	WorkflowSteps []struct {
		Name             string `json:"name"`
		StepDefinitionID *int64 `json:"step_definition_id"`
	} `json:"workflow_steps"`
	Labels []struct {
		LabelName string `json:"label_name"`
		ValueID   int64  `json:"value_id"`
	} `json:"labels"`
}

func TestIntegrationMCPProjectSettingsTools(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	pid := f.projectA.ID

	tm := testutil.SeedTM(t, f.q, f.tenantA.ID, "Main TM")
	foreignTM := testutil.SeedTM(t, f.q, f.tenantB.ID, "Their TM")
	tb := testutil.SeedTermBase(t, f.q, f.tenantA.ID, "Glossary", "en", "de")
	ownQA, err := f.q.CreateQAProfile(ctx, db.CreateQAProfileParams{TenantID: f.tenantA.ID, Name: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	foreignQA, err := f.q.CreateQAProfile(ctx, db.CreateQAProfileParams{TenantID: f.tenantB.ID, Name: "Theirs"})
	if err != nil {
		t.Fatal(err)
	}

	// TMs: full replace, tenant-checked.
	var tms struct {
		TMs []struct {
			TMID int64 `json:"tm_id"`
		} `json:"tms"`
	}
	if isErr, text := callTool(t, s, "openloc_set_project_tms", map[string]any{
		"project_id": pid, "tms": []map[string]any{{"tm_id": tm.ID, "mode": "read"}},
	}, &tms); isErr {
		t.Fatalf("set_project_tms: %s", text)
	}
	stored, err := f.q.ListSettingsTMAssignments(ctx, f.projectA.SettingsProfileID)
	if err != nil || len(stored) != 1 || stored[0].TmID != tm.ID || stored[0].Mode != "read" {
		t.Fatalf("TM assignment not stored: %+v (%v)", stored, err)
	}
	if isErr, text := callTool(t, s, "openloc_set_project_tms", map[string]any{
		"project_id": pid, "tms": []map[string]any{{"tm_id": foreignTM.ID}},
	}, nil); !isErr || !strings.Contains(text, "not found") {
		t.Fatalf("a foreign TM was accepted: %v %s", isErr, text)
	}
	if stored, _ := f.q.ListSettingsTMAssignments(ctx, f.projectA.SettingsProfileID); len(stored) != 1 || stored[0].TmID != tm.ID {
		t.Fatalf("the refused replace changed the list: %+v", stored)
	}

	if isErr, text := callTool(t, s, "openloc_set_project_term_bases", map[string]any{
		"project_id": pid, "term_bases": []map[string]any{{"term_base_id": tb.ID, "source_language": "EN", "target_language": "de"}},
	}, nil); isErr {
		t.Fatalf("set_project_term_bases: %s", text)
	}
	terms, err := f.q.ListSettingsTermAssignments(ctx, f.projectA.SettingsProfileID)
	if err != nil || len(terms) != 1 || terms[0].TermBaseID != tb.ID || terms[0].SourceLanguage != "en" {
		t.Fatalf("term base not stored (language normalized): %+v (%v)", terms, err)
	}

	// Scalars + QA profile; a foreign QA profile is refused.
	var settings projectSettingsOut
	if isErr, text := callTool(t, s, "openloc_update_project_settings", map[string]any{
		"project_id": pid, "mt_on_upload": true, "fuzzy_threshold": 0.8, "qa_profile_id": ownQA.ID,
	}, &settings); isErr {
		t.Fatalf("update_project_settings: %s", text)
	}
	profile, err := f.q.GetSettingsProfile(ctx, f.projectA.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.MtOnUpload || profile.FuzzyThreshold < 0.79 || profile.QaProfileID == nil || *profile.QaProfileID != ownQA.ID {
		t.Fatalf("settings not stored: %+v", profile)
	}
	if len(settings.TMs) != 1 || len(settings.TermBases) != 1 || !settings.MTOnUpload {
		t.Errorf("update should return the full settings: %+v", settings)
	}
	if isErr, text := callTool(t, s, "openloc_update_project_settings", map[string]any{
		"project_id": pid, "qa_profile_id": foreignQA.ID,
	}, nil); !isErr || !strings.Contains(text, "QA profile not found") {
		t.Fatalf("a foreign QA profile was accepted: %v %s", isErr, text)
	}
	if isErr, text := callTool(t, s, "openloc_update_project_settings", map[string]any{
		"project_id": pid, "qa_profile_id": 0,
	}, nil); isErr {
		t.Fatalf("clear QA profile: %s", text)
	}
	if profile, _ := f.q.GetSettingsProfile(ctx, f.projectA.SettingsProfileID); profile.QaProfileID != nil {
		t.Errorf("qa_profile_id 0 should clear, got %v", *profile.QaProfileID)
	}

	// Cross-tenant project: the same opaque not-found for reads and writes.
	for tool, args := range map[string]map[string]any{
		"openloc_get_project_settings":    {"project_id": f.projectB.ID},
		"openloc_update_project_settings": {"project_id": f.projectB.ID, "mt_on_upload": true},
		"openloc_set_project_tms":         {"project_id": f.projectB.ID, "tms": []map[string]any{}},
	} {
		isErr, text := callTool(t, s, tool, args, nil)
		if !isErr {
			t.Errorf("%s reached tenant B's project", tool)
			continue
		}
		assertOpaqueNotFound(t, text)
	}

	// A read-only token reads but cannot write.
	ro := connect(t, f.url, f.tokenA)
	if isErr, text := callTool(t, ro, "openloc_get_project_settings", map[string]any{"project_id": pid}, &settings); isErr {
		t.Fatalf("read with a read token: %s", text)
	}
	if isErr, _ := callTool(t, ro, "openloc_update_project_settings", map[string]any{"project_id": pid, "mt_on_upload": false}, nil); !isErr {
		t.Error("a read-only token changed settings")
	}
	if profile, _ := f.q.GetSettingsProfile(ctx, f.projectA.SettingsProfileID); !profile.MtOnUpload {
		t.Error("the refused write still landed")
	}

	// A project manager has the same surface as REST.
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm@test.com", "pass", "project_manager")
	pms := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))
	if isErr, text := callTool(t, pms, "openloc_update_project_settings", map[string]any{"project_id": pid, "mt_on_upload": false}, nil); isErr {
		t.Fatalf("project manager refused: %s", text)
	}
}

func TestIntegrationMCPUpdateProjectAndHistory(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	if isErr, text := callTool(t, s, "openloc_update_project", map[string]any{
		"project_id": f.projectA.ID, "name": "  Renamed  ", "status": "on_hold", "deadline": "2026-12-01T17:00:00Z",
	}, nil); isErr {
		t.Fatalf("update_project: %s", text)
	}
	p, err := f.q.GetProject(ctx, db.GetProjectParams{ID: f.projectA.ID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Renamed" || p.Status != db.ProjectStatusOnHold || !p.Deadline.Valid {
		t.Fatalf("update not stored: %+v", p)
	}
	if isErr, text := callTool(t, s, "openloc_update_project", map[string]any{"project_id": f.projectA.ID, "deadline": ""}, nil); isErr {
		t.Fatalf("clear deadline: %s", text)
	}
	if p, _ := f.q.GetProject(ctx, db.GetProjectParams{ID: f.projectA.ID, TenantID: f.tenantA.ID}); p.Deadline.Valid || p.Name != "Renamed" {
		t.Fatalf("empty deadline should clear it and keep the rest: %+v", p)
	}
	for _, bad := range []map[string]any{
		{"status": "draft"}, {"status": "archived"}, {"name": " "}, {"deadline": "next week"},
	} {
		bad["project_id"] = f.projectA.ID
		if isErr, _ := callTool(t, s, "openloc_update_project", bad, nil); !isErr {
			t.Errorf("accepted %v", bad)
		}
	}
	draft := seedDraftProject(t, f.q, f.tenantA.ID, "Draft")
	if isErr, text := callTool(t, s, "openloc_update_project", map[string]any{"project_id": draft.ID, "status": "in_progress"}, nil); !isErr || !strings.Contains(text, "publish") {
		t.Errorf("a draft changed status without publishing: %v %s", isErr, text)
	}
	if isErr, text := callTool(t, s, "openloc_update_project", map[string]any{"project_id": f.projectB.ID, "name": "x"}, nil); !isErr {
		t.Error("renamed tenant B's project")
	} else {
		assertOpaqueNotFound(t, text)
	}

	// The change is in the history, with who made it.
	var events struct {
		Events []struct {
			EventType string         `json:"event_type"`
			ActorName string         `json:"actor_name"`
			Details   map[string]any `json:"details"`
		} `json:"events"`
	}
	if isErr, text := callTool(t, s, "openloc_list_project_events", map[string]any{"project_id": f.projectA.ID, "type": "project.metadata"}, &events); isErr {
		t.Fatalf("list_project_events: %s", text)
	}
	if len(events.Events) != 2 || events.Events[0].EventType != "project.metadata_updated" || events.Events[0].Details["changes"] == nil {
		t.Fatalf("want the two metadata_updated events, newest first: %+v", events.Events)
	}
	if isErr, text := callTool(t, s, "openloc_list_project_events", map[string]any{"project_id": f.projectB.ID}, nil); !isErr {
		t.Error("listed tenant B's history")
	} else {
		assertOpaqueNotFound(t, text)
	}
}

// seedDraftProject creates a project in the wizard's draft state.
func seedDraftProject(t *testing.T, q db.Querier, tenantID int64, name string) db.Project {
	t.Helper()
	ctx := context.Background()
	sp, err := q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{TenantID: tenantID, FuzzyThreshold: 0.75})
	if err != nil {
		t.Fatal(err)
	}
	p, err := q.CreateProject(ctx, db.CreateProjectParams{
		Name: name, SourceLanguage: "en", TargetLanguages: []string{"de"},
		TenantID: tenantID, SettingsProfileID: sp.ID, Status: db.ProjectStatusDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIntegrationMCPProjectWorkflowTools(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	proofread := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Proofread", "human", "", nil)
	edit := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Edit", "human", "", nil)
	wf, err := f.q.CreateWorkflow(ctx, db.CreateWorkflowParams{TenantID: f.tenantA.ID, Name: "Proof only"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.CreateWorkflowDefinitionStep(ctx, db.CreateWorkflowDefinitionStepParams{
		WorkflowID: wf.ID, StepDefinitionID: proofread.ID, StepOrder: 0,
	}); err != nil {
		t.Fatal(err)
	}
	steps := func() []db.WorkflowStep {
		rows, err := f.q.ListSettingsWorkflowSteps(ctx, f.projectA.SettingsProfileID)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	before := steps()

	// Preview: nothing changes, and it names the open job.
	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_apply_workflow", map[string]any{"project_id": f.projectA.ID, "workflow_id": wf.ID}, &g); isErr {
		t.Fatalf("apply_workflow preview: %s", text)
	}
	if g.Preview == nil || !strings.Contains(g.Preview.Summary, "Proof only") || len(g.Preview.Effects) == 0 ||
		!strings.Contains(g.Preview.Effects[0], "1 job is in progress") {
		t.Fatalf("preview should name the workflow and the open job: %+v", g.Preview)
	}
	if got := steps(); len(got) != len(before) {
		t.Fatalf("the preview changed the steps: %d → %d", len(before), len(got))
	}
	var applied struct {
		WorkflowName string `json:"workflow_name"`
		Steps        []struct {
			Name string `json:"name"`
		} `json:"steps"`
	}
	if isErr, text := confirmedCall(t, s, "openloc_apply_workflow", map[string]any{"project_id": f.projectA.ID, "workflow_id": wf.ID}, &applied); isErr {
		t.Fatalf("apply_workflow confirm: %s", text)
	}
	if got := steps(); len(got) != 1 || got[0].Name != "Proofread" || applied.WorkflowName != "Proof only" {
		t.Fatalf("workflow not applied: %+v / %+v", got, applied)
	}

	foreign, err := f.q.CreateWorkflow(ctx, db.CreateWorkflowParams{TenantID: f.tenantB.ID, Name: "Theirs"})
	if err != nil {
		t.Fatal(err)
	}
	if isErr, text := callTool(t, s, "openloc_apply_workflow", map[string]any{"project_id": f.projectA.ID, "workflow_id": foreign.ID}, nil); !isErr || !strings.Contains(text, "workflow not found") {
		t.Fatalf("a foreign workflow previewed: %v %s", isErr, text)
	}

	// Explicit steps: two library steps, first step first.
	stepsArg := []map[string]any{{"step_definition_id": edit.ID}, {"step_definition_id": proofread.ID, "default_assignee": f.ownerA.ID}}
	if isErr, text := callTool(t, s, "openloc_set_project_workflow_steps", map[string]any{"project_id": f.projectA.ID, "steps": stepsArg}, &g); isErr {
		t.Fatalf("set steps preview: %s", text)
	}
	if got := steps(); len(got) != 1 {
		t.Fatalf("the preview changed the steps: %+v", got)
	}
	if isErr, text := confirmedCall(t, s, "openloc_set_project_workflow_steps", map[string]any{"project_id": f.projectA.ID, "steps": stepsArg}, nil); isErr {
		t.Fatalf("set steps confirm: %s", text)
	}
	got := steps()
	if len(got) != 2 || got[0].Name != "Edit" || got[1].Name != "Proofread" || got[1].DefaultAssignee == nil || *got[1].DefaultAssignee != f.ownerA.ID {
		t.Fatalf("steps not replaced: %+v", got)
	}
	foreignStep := testutil.SeedStepDefinition(t, f.q, f.tenantB.ID, "Theirs", "human", "", nil)
	if isErr, _ := callTool(t, s, "openloc_set_project_workflow_steps", map[string]any{
		"project_id": f.projectA.ID, "steps": []map[string]any{{"step_definition_id": foreignStep.ID}},
	}, nil); !isErr {
		t.Error("a foreign step definition previewed")
	}
}

func TestIntegrationMCPClearAndPretranslateProject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	if _, err := f.pool.Exec(ctx, `
		UPDATE translations SET active_text = 'Hallo' WHERE segment_id IN (
			SELECT s.id FROM segments s JOIN files fi ON fi.id = s.file_id WHERE fi.project_id = $1)`, f.projectA.ID); err != nil {
		t.Fatal(err)
	}
	filled := func() int {
		var n int
		if err := f.pool.QueryRow(ctx, `
			SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id
			JOIN files fi ON fi.id = s.file_id WHERE fi.project_id = $1 AND t.active_text IS NOT NULL`, f.projectA.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The preview counts what the clear would blank: never a locked row, and a
	// confirmed one only with scope "all".
	setRow := func(sql string) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, sql+` WHERE id = (
			SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id
			JOIN files fi ON fi.id = s.file_id WHERE fi.project_id = $1 AND t.status = 'needs_review' AND NOT t.locked
			ORDER BY t.id LIMIT 1)`, f.projectA.ID); err != nil {
			t.Fatal(err)
		}
	}
	setRow(`UPDATE translations SET status = 'approved'`)
	setRow(`UPDATE translations SET locked = true`)
	for scope, want := range map[string]string{"non_confirmed": "1 segment translation is", "all": "2 segment translations"} {
		var pg guardedOut
		if isErr, text := callTool(t, s, "openloc_clear_project_translations", map[string]any{"project_id": f.projectA.ID, "scope": scope}, &pg); isErr {
			t.Fatalf("clear preview %s: %s", scope, text)
		}
		if pg.Preview == nil || !strings.Contains(pg.Preview.Effects[0], want) {
			t.Errorf("scope %s preview = %+v, want %q", scope, pg.Preview, want)
		}
	}
	if _, err := f.pool.Exec(ctx, `UPDATE translations SET status = 'needs_review', locked = false WHERE segment_id IN (
		SELECT s.id FROM segments s JOIN files fi ON fi.id = s.file_id WHERE fi.project_id = $1)`, f.projectA.ID); err != nil {
		t.Fatal(err)
	}

	args := map[string]any{"project_id": f.projectA.ID, "scope": "all"}
	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_clear_project_translations", args, &g); isErr {
		t.Fatalf("clear preview: %s", text)
	}
	if g.Preview == nil || !strings.Contains(g.Preview.Effects[0], "3 segment translations") {
		t.Fatalf("preview should give the exact count: %+v", g.Preview)
	}
	if n := filled(); n != 3 {
		t.Fatalf("the preview cleared something: %d left", n)
	}
	var cleared struct {
		Cleared int64 `json:"cleared"`
	}
	if isErr, text := confirmedCall(t, s, "openloc_clear_project_translations", args, &cleared); isErr {
		t.Fatalf("clear confirm: %s", text)
	}
	if cleared.Cleared != 3 || filled() != 0 {
		t.Fatalf("clear: reported %d, %d still filled", cleared.Cleared, filled())
	}
	if isErr, text := callTool(t, s, "openloc_clear_project_translations", map[string]any{"project_id": f.projectA.ID, "scope": "some"}, nil); !isErr || !strings.Contains(text, "scope") {
		t.Errorf("a bad scope previewed: %v %s", isErr, text)
	}
	if isErr, text := callTool(t, s, "openloc_clear_project_translations", map[string]any{"project_id": f.projectB.ID, "scope": "all"}, nil); !isErr {
		t.Error("previewed a clear of tenant B's project")
	} else {
		assertOpaqueNotFound(t, text)
	}
	// A job past its first step refuses the clear, in the preview already.
	proofread := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Proofread", "human", "", nil)
	edit := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Edit", "human", "", nil)
	if isErr, text := confirmedCall(t, s, "openloc_set_project_workflow_steps", map[string]any{
		"project_id": f.projectA.ID, "steps": []map[string]any{{"step_definition_id": edit.ID}, {"step_definition_id": proofread.ID}},
	}, nil); isErr {
		t.Fatalf("set steps: %s", text)
	}
	wfSteps, err := f.q.ListSettingsWorkflowSteps(ctx, f.projectA.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: f.jobA.ID, WorkflowStepID: wfSteps[1].ID, Status: "in_progress",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: f.jobA.ID, CurrentStepID: &a.ID}); err != nil {
		t.Fatal(err)
	}
	if isErr, text := callTool(t, s, "openloc_clear_project_translations", args, nil); !isErr || !strings.Contains(text, "past the first workflow step") {
		t.Errorf("clear past the first step previewed: %v %s", isErr, text)
	}

	var pre struct {
		Queued  int `json:"queued"`
		Skipped int `json:"skipped"`
		Total   int `json:"total"`
	}
	if isErr, text := callTool(t, s, "openloc_pretranslate_project", map[string]any{"project_id": f.projectA.ID}, &pre); isErr {
		t.Fatalf("pretranslate_project: %s", text)
	}
	if pre.Total != 1 || pre.Queued != 1 || pre.Skipped != 0 {
		t.Fatalf("pretranslate counts: %+v", pre)
	}
	if isErr, text := callTool(t, s, "openloc_pretranslate_project", map[string]any{"project_id": f.projectB.ID}, nil); !isErr {
		t.Error("pre-translated tenant B's project")
	} else {
		assertOpaqueNotFound(t, text)
	}
	if isErr, _ := callTool(t, connect(t, f.url, f.tokenA), "openloc_pretranslate_project", map[string]any{"project_id": f.projectA.ID}, nil); !isErr {
		t.Error("a read-only token started pre-translation")
	}
}
