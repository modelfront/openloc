// Job and segment tools called by tenant B with tenant A's job, step, segment
// or comment id. Each call is refused as not found and leaves tenant A's rows
// as they were; tenant A's own call with the same ids goes through, so each
// refusal is the tenant check and not a malformed call.
package mcp_test

import (
	"context"
	"strings"
	"testing"

	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

type foreignCall struct {
	tool string
	args map[string]any
}

// mustNotFindAll runs every call and asserts the opaque not-found for each.
func mustNotFindAll(t *testing.T, s sessionT, calls []foreignCall) {
	t.Helper()
	for _, c := range calls {
		isErr, text := callTool(t, s, c.tool, c.args, nil)
		if !isErr {
			t.Errorf("%s %v reached another tenant's data", c.tool, c.args)
			continue
		}
		assertOpaqueNotFound(t, text)
	}
}

func TestIntegrationMCPJobToolsRejectOtherTenantIDs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	stepID := testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID, nil)
	engine := testutil.SeedMTEngine(t, f.q, f.tenantA.ID, "Tenant A MT", "mock")
	testutil.SeedMTRouterForProfile(t, f.q, f.tenantA.ID, f.projectA.SettingsProfileID, &engine.ID, nil)
	first, _ := firstTranslation(t, connect(t, f.url, f.tokenA), f.jobA.ID)
	jobBefore := readJob(t, f)

	// Tenant B names its own member and tenant B's own job where it can, so
	// only the check on tenant A's id can refuse the call.
	deadline := "2030-01-02T15:00:00Z"
	assign := map[string]any{"job_id": f.jobA.ID, "user_id": f.ownerB.ID}
	stepDeadline := map[string]any{"job_id": f.jobA.ID, "step_assignment_id": stepID, "deadline": deadline}
	asB := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	mustNotFindAll(t, asB, []foreignCall{
		{"openloc_apply_mt", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_rerun_ai_step", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_assign_job", assign},
		{"openloc_set_step_deadline", stepDeadline},
		{"openloc_set_step_deadline", map[string]any{"job_id": f.jobB.ID, "step_assignment_id": stepID, "deadline": deadline}},
	})

	steps, err := f.q.ListJobStepAssignments(ctx, f.jobA.ID)
	if err != nil || len(steps) != 1 || steps[0].AssignedTo != nil || steps[0].Deadline.Valid {
		t.Fatalf("tenant A's step changed: %+v (err %v)", steps, err)
	}
	if tr := readTranslation(t, f, f.tenantA.ID, first); tr.ActiveText != nil {
		t.Errorf("tenant B's apply_mt filled tenant A's segment: %q", *tr.ActiveText)
	}
	if after := readJob(t, f); after.Status != jobBefore.Status || after.CurrentStep != jobBefore.CurrentStep ||
		!after.UpdatedAt.Time.Equal(jobBefore.UpdatedAt.Time) {
		t.Errorf("tenant B's calls touched tenant A's job: %+v, was %+v", after, jobBefore)
	}

	asA := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	if isErr, text := callTool(t, asA, "openloc_apply_mt", map[string]any{"job_id": f.jobA.ID}, nil); isErr {
		t.Fatalf("apply_mt as tenant A: %s", text)
	}
	if tr := readTranslation(t, f, f.tenantA.ID, first); ptr.Deref(tr.ActiveText) == "" {
		t.Error("tenant A's apply_mt left the segment empty")
	}
	// The job's step is a human one, so tenant A's rerun is refused by the run
	// itself, past the job lookup that refused tenant B.
	if isErr, text := callTool(t, asA, "openloc_rerun_ai_step", map[string]any{"job_id": f.jobA.ID}, nil); !isErr || !strings.Contains(text, "no AI router to run") {
		t.Errorf("rerun_ai_step as tenant A: isErr=%v %q, want the run's own refusal", isErr, text)
	}
	assign["user_id"] = f.linguistA.ID
	for _, c := range []foreignCall{{"openloc_assign_job", assign}, {"openloc_set_step_deadline", stepDeadline}} {
		if isErr, text := callTool(t, asA, c.tool, c.args, nil); isErr {
			t.Fatalf("%s as tenant A: %s", c.tool, text)
		}
	}
	steps, err = f.q.ListJobStepAssignments(ctx, f.jobA.ID)
	if err != nil || len(steps) != 1 || ptr.Deref(steps[0].AssignedTo) != f.linguistA.ID || !steps[0].Deadline.Valid {
		t.Errorf("tenant A's own assign and deadline did not land: %+v (err %v)", steps, err)
	}
}

func TestIntegrationMCPSegmentToolsRejectOtherTenantIDs(t *testing.T) {
	f := newFixture(t)
	asA := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	first, version := firstTranslation(t, asA, f.jobA.ID)
	if isErr, text := callTool(t, asA, "openloc_save_draft",
		map[string]any{"translation_id": first, "text": "Hallo", "expected_updated_at": version}, nil); isErr {
		t.Fatalf("save_draft: %s", text)
	}
	var comment struct {
		CommentID int64 `json:"comment_id"`
	}
	if isErr, text := callTool(t, asA, "openloc_add_comment",
		map[string]any{"translation_id": first, "content": "Tenant A note"}, &comment); isErr {
		t.Fatalf("add_comment: %s", text)
	}
	ownSegment, _ := firstTranslation(t, connect(t, f.url, f.tokenB), f.jobB.ID)

	// The last call names tenant B's own segment, so only the check that the
	// comment belongs to that segment can refuse it.
	clearArgs := map[string]any{"translation_id": first}
	deleteComment := map[string]any{"translation_id": first, "comment_id": comment.CommentID}
	asB := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	mustNotFindAll(t, asB, []foreignCall{
		{"openloc_clear_segment", clearArgs},
		{"openloc_delete_comment", deleteComment},
		{"openloc_delete_comment", map[string]any{"translation_id": ownSegment, "comment_id": comment.CommentID}},
	})

	if tr := readTranslation(t, f, f.tenantA.ID, first); ptr.Deref(tr.ActiveText) != "Hallo" {
		t.Errorf("tenant A's segment changed: %q", ptr.Deref(tr.ActiveText))
	}
	var deleted bool
	if err := f.pool.QueryRow(context.Background(),
		"SELECT deleted_at IS NOT NULL FROM segment_comments WHERE id = $1", comment.CommentID).Scan(&deleted); err != nil || deleted {
		t.Errorf("tenant A's comment was deleted (err %v)", err)
	}

	cfgPreviewOnly(t, asA, "openloc_clear_segment", clearArgs)
	cfgPreviewOnly(t, asA, "openloc_delete_comment", deleteComment)
}
