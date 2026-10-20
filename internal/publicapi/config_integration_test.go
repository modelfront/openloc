package publicapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/publicapi"
	"openloc/internal/testutil"
)

func TestIntegrationV1_TemplatesAndWorkflows(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Library")
	c := f.client
	pm := func() string { return f.key(t, "project_manager") }

	rec := c.do(http.MethodGet, "/api/v1/templates", nil, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.Template](t, rec); len(got) != 1 {
		t.Fatalf("templates = %+v", got)
	}
	tpl := fmt.Sprintf("/api/v1/templates/%d", f.ids["templateID"])
	rec = c.do(http.MethodGet, tpl, nil, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.Template](t, rec); got.ID != f.ids["templateID"] || got.Name != "Template" {
		t.Fatalf("template = %+v", got)
	}
	rec = c.do(http.MethodPost, tpl+"/duplicate", nil, pm())
	wantStatus(t, rec, http.StatusCreated)
	dup := testutil.DecodeData[publicapi.Template](t, rec)
	if dup.Name != "Copy of Template" {
		t.Fatalf("duplicate = %+v", dup)
	}
	rec = c.do(http.MethodDelete, fmt.Sprintf("/api/v1/templates/%d", dup.ID), nil, pm())
	wantStatus(t, rec, http.StatusNoContent)

	rec = c.do(http.MethodPost, "/api/v1/workflow-steps", map[string]any{"name": "Proofread", "type": "human"}, pm())
	wantStatus(t, rec, http.StatusCreated)
	step := testutil.DecodeData[publicapi.StepDefinition](t, rec)
	rec = c.do(http.MethodPost, "/api/v1/workflow-steps", map[string]any{"name": "Proofread", "type": "human"}, pm())
	wantStatus(t, rec, http.StatusConflict)
	rec = c.do(http.MethodPost, "/api/v1/workflow-steps", map[string]any{"name": "Robot", "type": "ai"}, pm())
	wantStatus(t, rec, http.StatusBadRequest)

	stepPath := fmt.Sprintf("/api/v1/workflow-steps/%d", f.ids["stepDefinitionID"])
	rec = c.do(http.MethodPut, stepPath, map[string]any{"name": "Translate and check", "type": "human"}, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.StepDefinition](t, rec); got.Name != "Translate and check" || got.Type != "human" {
		t.Fatalf("replaced step = %+v", got)
	}
	rec = c.do(http.MethodGet, stepPath, nil, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.StepDefinition](t, rec); got.Name != "Translate and check" {
		t.Fatalf("step after replace = %+v", got)
	}
	rec = c.do(http.MethodGet, "/api/v1/workflow-steps", nil, pm())
	wantStatus(t, rec, http.StatusOK)
	names := map[string]bool{}
	for _, s := range testutil.DecodeData[[]publicapi.StepDefinition](t, rec) {
		names[s.Name] = true
	}
	if !names["Translate and check"] || !names["Proofread"] || !names["Review"] {
		t.Fatalf("steps = %v", names)
	}

	rec = c.do(http.MethodPost, "/api/v1/workflows", map[string]any{"name": "Two-step", "steps": []map[string]any{
		{"step_definition_id": f.ids["stepDefinitionID"], "default_assignee_id": f.users["linguist"].ID},
		{"step_definition_id": step.ID},
	}}, pm())
	wantStatus(t, rec, http.StatusCreated)
	wf := testutil.DecodeData[publicapi.Workflow](t, rec)
	if len(wf.Steps) != 2 || wf.Steps[1].Name != "Proofread" || *wf.Steps[0].DefaultAssignee != f.users["linguist"].ID {
		t.Fatalf("workflow = %+v", wf)
	}
	wfPath := fmt.Sprintf("/api/v1/workflows/%d", wf.ID)
	rec = c.do(http.MethodGet, wfPath, nil, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.Workflow](t, rec); got.ID != wf.ID || got.Name != "Two-step" || len(got.Steps) != 2 {
		t.Fatalf("workflow detail = %+v", got)
	}
	rec = c.do(http.MethodPut, wfPath, map[string]any{"name": "One-step", "steps": []map[string]any{{"step_definition_id": step.ID}}}, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.Workflow](t, rec); got.Name != "One-step" || len(got.Steps) != 1 {
		t.Fatalf("replaced = %+v", got)
	}

	// A step a workflow uses cannot be deleted.
	rec = c.do(http.MethodDelete, fmt.Sprintf("/api/v1/workflow-steps/%d", step.ID), nil, pm())
	wantStatus(t, rec, http.StatusConflict)
	rec = c.do(http.MethodDelete, wfPath, nil, pm())
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodDelete, fmt.Sprintf("/api/v1/workflow-steps/%d", step.ID), nil, pm())
	wantStatus(t, rec, http.StatusNoContent)

	rec = c.do(http.MethodGet, "/api/v1/workflows", nil, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.Workflow](t, rec); len(got) != 1 || len(got[0].Steps) != 1 {
		t.Fatalf("workflows = %+v", got)
	}

	// A new project copies a library workflow: its jobs start on the
	// workflow's first step.
	rec = c.do(http.MethodPost, "/api/v1/projects", map[string]any{"name": "From library", "source_language": "en", "target_languages": []string{"de"},
		"workflow_id": f.ids["workflowID"]}, pm())
	wantStatus(t, rec, http.StatusCreated)
	p := testutil.DecodeData[publicapi.Project](t, rec)
	rec = c.upload(fmt.Sprintf("/api/v1/projects/%d/files", p.ID), "a.txt", "One.\n", pm())
	wantStatus(t, rec, http.StatusCreated)
	rec = c.do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/jobs", p.ID), nil, pm())
	wantStatus(t, rec, http.StatusOK)
	jobs := testutil.DecodeData[[]publicapi.Job](t, rec)
	if len(jobs) != 1 || jobs[0].WorkflowStep == nil || jobs[0].WorkflowStep.Name != "Review" {
		t.Fatalf("job of a project on the Standard workflow = %+v", jobs)
	}
}

func TestIntegrationV1_Labels(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Labels")
	c := f.client
	pm := func() string { return f.key(t, "project_manager") }

	rec := c.do(http.MethodGet, "/api/v1/labels", nil, pm())
	wantStatus(t, rec, http.StatusOK)
	var customer *publicapi.Label
	for _, l := range testutil.DecodeData[[]publicapi.Label](t, rec) {
		if l.Key != nil && *l.Key == "customer" {
			customer = &l
		}
	}
	if customer == nil {
		t.Fatal("the built-in Customer label is not listed with key customer")
	}

	rec = c.do(http.MethodPost, fmt.Sprintf("/api/v1/labels/%d/values", customer.ID), map[string]any{"value": "Acme"}, pm())
	wantStatus(t, rec, http.StatusCreated)
	acme := testutil.DecodeData[publicapi.LabelValue](t, rec)

	proj := f.path("/projects/{projectID}/labels")
	rec = c.do(http.MethodPut, proj, map[string]any{"value_ids": []int64{acme.ID, f.ids["valueID"]}}, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.ProjectLabel](t, rec); len(got) != 2 {
		t.Fatalf("project labels = %+v", got)
	}
	rec = c.do(http.MethodGet, proj, nil, pm())
	wantStatus(t, rec, http.StatusOK)

	// A built-in label is never deleted.
	rec = c.do(http.MethodDelete, fmt.Sprintf("/api/v1/labels/%d", customer.ID), nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusConflict)
	// A value under the wrong label is not found.
	rec = c.do(http.MethodDelete, fmt.Sprintf("/api/v1/labels/%d/values/%d", f.ids["labelID"], acme.ID), nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusNotFound)
	// A used value is deleted and leaves the project.
	rec = c.do(http.MethodDelete, fmt.Sprintf("/api/v1/labels/%d/values/%d", customer.ID, acme.ID), nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodGet, proj, nil, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.ProjectLabel](t, rec); len(got) != 1 || got[0].ValueID != f.ids["valueID"] {
		t.Fatalf("project labels after deleting a used value = %+v", got)
	}

	rec = c.do(http.MethodPost, "/api/v1/labels", map[string]any{"name": "Region", "values": []string{"EU", "US"}}, pm())
	wantStatus(t, rec, http.StatusCreated)
	region := testutil.DecodeData[publicapi.Label](t, rec)
	if region.Name != "Region" || len(region.Values) != 2 || region.Key != nil {
		t.Fatalf("created label = %+v", region)
	}
	rec = c.do(http.MethodDelete, fmt.Sprintf("/api/v1/labels/%d", region.ID), nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodGet, "/api/v1/labels", nil, pm())
	wantStatus(t, rec, http.StatusOK)
	for _, l := range testutil.DecodeData[[]publicapi.Label](t, rec) {
		if l.ID == region.ID {
			t.Fatalf("deleted label is still listed: %+v", l)
		}
	}

	// The Customer label holds the customer; a project has no customer field.
	rec = c.do(http.MethodPatch, f.path("/projects/{projectID}"), map[string]any{"customer_id": 1}, pm())
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestIntegrationV1_MembersAndInvites(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Team")
	c := f.client

	rec := c.do(http.MethodGet, "/api/v1/members", nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.Member](t, rec); len(got) != 5 {
		t.Fatalf("members = %+v", got)
	}

	target := fmt.Sprintf("/api/v1/members/%d", f.ids["userID"])
	rec = c.do(http.MethodPatch, target, map[string]any{"role": "project_manager"}, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.Member](t, rec); got.Role != "project_manager" {
		t.Fatalf("member = %+v", got)
	}
	// Only an owner may make an owner.
	rec = c.do(http.MethodPatch, target, map[string]any{"role": "owner"}, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusForbidden)

	// A project manager may invite linguists only.
	rec = c.do(http.MethodPost, "/api/v1/invites", map[string]any{"email": "admin-to-be@example.com", "role": "admin"}, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusForbidden)
	rec = c.do(http.MethodPost, "/api/v1/invites", map[string]any{"email": "vendor@example.com"}, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusCreated)
	inv := testutil.DecodeData[publicapi.Invite](t, rec)
	if inv.Role != "linguist" || inv.Status != "pending" {
		t.Fatalf("invite = %+v", inv)
	}

	// The join link never leaves the server.
	rec = c.do(http.MethodGet, "/api/v1/invites", nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	var raw struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, i := range raw.Data {
		if _, ok := i["token"]; ok {
			t.Fatalf("invite list exposes the token: %v", i)
		}
	}

	rec = c.do(http.MethodDelete, fmt.Sprintf("/api/v1/invites/%d", inv.ID), nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodDelete, target, nil, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusNoContent)
}

func TestIntegrationV1_AIConfiguration(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "AIConfig")
	c := f.client
	admin := func() string { return f.key(t, "admin") }

	rec := c.do(http.MethodPost, "/api/v1/ai/prompts", map[string]any{"name": "Strict review", "mode": "qe_ape", "prompt_text": "Review the translation strictly.",
		"language_additions": []map[string]any{{"target_languages": []string{"de"}, "text": "Use Sie."}}}, admin())
	wantStatus(t, rec, http.StatusCreated)
	p := testutil.DecodeData[publicapi.AIPrompt](t, rec)
	if p.Mode != "qe_ape" || len(p.LanguageAdditions) != 1 {
		t.Fatalf("prompt = %+v", p)
	}
	path := fmt.Sprintf("/api/v1/ai/prompts/%d", p.ID)

	rec = c.do(http.MethodPut, path, map[string]any{"name": "Strict review", "mode": "qe", "prompt_text": "x"}, admin())
	wantStatus(t, rec, http.StatusBadRequest) // the mode is fixed
	rec = c.do(http.MethodPut, path, map[string]any{"name": "Strict review 2", "mode": "qe_ape", "prompt_text": "Review it."}, admin())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.AIPrompt](t, rec); got.Name != "Strict review 2" || len(got.LanguageAdditions) != 0 {
		t.Fatalf("replaced prompt = %+v", got)
	}

	rec = c.do(http.MethodGet, "/api/v1/ai/prompts", nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	rec = c.do(http.MethodPost, "/api/v1/ai/prompts", map[string]any{"name": "PM prompt", "mode": "qe", "prompt_text": "x"}, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusForbidden)

	rec = c.do(http.MethodDelete, path, nil, admin())
	wantStatus(t, rec, http.StatusNoContent)

	rec = c.do(http.MethodGet, "/api/v1/ai/engines", nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.AIEngine](t, rec); len(got) != 1 || got[0].ID != f.ids["engineID"] {
		t.Fatalf("engines = %+v", got)
	}
	// An engine needs a real connection.
	rec = c.do(http.MethodPost, "/api/v1/ai/engines", map[string]any{"name": "No key", "token_id": 999999}, admin())
	wantStatus(t, rec, http.StatusBadRequest)
	rec = c.do(http.MethodPost, "/api/v1/ai/engines", map[string]any{"name": "Native review", "token_id": f.ids["tokenID"], "model": testutil.MockExtensionID}, admin())
	wantStatus(t, rec, http.StatusCreated)
	e := testutil.DecodeData[publicapi.AIEngine](t, rec)
	if e.Name != "Native review" || e.Purpose != "qe_ape" || len(e.Rows) != 1 || e.Rows[0].TokenID == nil || *e.Rows[0].TokenID != f.ids["tokenID"] {
		t.Fatalf("engine = %+v", e)
	}
	rec = c.do(http.MethodPut, fmt.Sprintf("/api/v1/ai/engines/%d", e.ID), map[string]any{"name": "Native review 2", "enabled": false, "token_id": f.ids["tokenID"], "model": testutil.MockExtensionID}, admin())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.AIEngine](t, rec); got.ID != e.ID || got.Name != "Native review 2" || got.Enabled || len(got.Rows) != 1 {
		t.Fatalf("replaced engine = %+v", got)
	}

	rec = c.do(http.MethodDelete, fmt.Sprintf("/api/v1/ai/engines/%d", f.ids["engineID"]), nil, admin())
	wantStatus(t, rec, http.StatusNoContent)
}
