package mcp_test

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// save_draft follows the editor's task rule without its reopen: a tool never
// puts a completed task back in progress, even for an owner, and an errored
// task takes no draft. The segment stays untouched either way.
func TestIntegrationMCPSaveDraftRespectsTheCurrentTask(t *testing.T) {
	cases := []struct {
		name    string
		status  string // status of the job's single task
		failJob bool
		want    string // substring of the refusal
	}{
		{name: "completed task", status: "completed", want: "completed"},
		{name: "errored task", status: "in_progress", failJob: true, want: "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			testutil.SeedWorkflow(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID,
				testutil.WorkflowStepSpec{Name: "Translate", Assignee: &f.ownerA.ID, Status: c.status})
			if c.failJob {
				msg := "provider 503"
				if err := f.q.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: f.jobA.ID, ErrorMessage: &msg}); err != nil {
					t.Fatal(err)
				}
			}
			s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
			translationID, _ := firstTranslation(t, s, f.jobA.ID)

			isErr, text := callTool(t, s, "openloc_save_draft", map[string]any{
				"translation_id": translationID, "text": "should not land",
			}, nil)
			if !isErr || !strings.Contains(strings.ToLower(text), c.want) {
				t.Fatalf("save_draft = (isErr=%v, %q), want a refusal mentioning %q", isErr, text, c.want)
			}
			got, err := f.q.GetTranslationTenant(ctx, db.GetTranslationTenantParams{ID: translationID, TenantID: f.tenantA.ID})
			if err != nil {
				t.Fatal(err)
			}
			if got.ActiveText != nil && *got.ActiveText == "should not land" {
				t.Error("a refused draft was written")
			}
			steps, err := f.q.ListJobStepAssignments(ctx, f.jobA.ID)
			if err != nil {
				t.Fatal(err)
			}
			if steps[0].Status != c.status {
				t.Errorf("task status = %q, want it left at %q", steps[0].Status, c.status)
			}
		})
	}
}

// The write tools built on the shared services (here the batch draft) refuse
// a completed task too, even for an owner: every tool call is marked as a
// caller that never reopens, whatever the service passes.
func TestIntegrationMCPServiceWritesNeverReopen(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	testutil.SeedWorkflow(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID,
		testutil.WorkflowStepSpec{Name: "Translate", Assignee: &f.ownerA.ID, Status: "completed"})
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	translationID, _ := firstTranslation(t, s, f.jobA.ID)

	isErr, text := callTool(t, s, "openloc_save_drafts_batch", map[string]any{
		"job_id": f.jobA.ID, "drafts": []map[string]any{{"translation_id": translationID, "text": "should not land"}},
	}, nil)
	if !isErr || !strings.Contains(strings.ToLower(text), "completed") {
		t.Fatalf("save_drafts_batch = (isErr=%v, %q), want a completed-task refusal", isErr, text)
	}
	steps, err := f.q.ListJobStepAssignments(ctx, f.jobA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if steps[0].Status != "completed" {
		t.Errorf("task status = %q, a tool must not reopen it", steps[0].Status)
	}
}
