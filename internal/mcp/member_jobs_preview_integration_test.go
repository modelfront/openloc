package mcp_test

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The remove-member preview counts the jobs of the member it names, not of
// whichever member the list happens to start with.
func TestIntegrationMCPRemoveMemberPreviewCountsThatMembersJobs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	idle := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "aaa-idle@test.com", "pass", "linguist")
	busy := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "zzz-busy@test.com", "pass", "linguist")
	for id, name := range map[int64]string{idle.ID: "aaa idle", busy.ID: "zzz busy"} {
		if _, err := f.pool.Exec(ctx, `UPDATE users SET name = $2 WHERE id = $1`, id, name); err != nil {
			t.Fatal(err)
		}
	}
	step, err := f.q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: f.projectA.SettingsProfileID, Name: "Translate", StepOrder: 1, RoleRequired: "linguist",
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssignStep(t, f.q, f.jobA.ID, step.ID, testutil.WorkflowStepSpec{Assignee: &busy.ID, Status: "in_progress"})

	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	effects := func(userID int64) string {
		t.Helper()
		var g guardedOut
		if isErr, text := callTool(t, owner, "openloc_remove_member", map[string]any{"user_id": userID}, &g); isErr {
			t.Fatalf("preview: %s", text)
		}
		return strings.Join(g.Preview.Effects, " ")
	}
	if got := effects(busy.ID); !strings.Contains(got, "1 job(s)") {
		t.Errorf("busy member's preview = %q, want their 1 job", got)
	}
	if got := effects(idle.ID); strings.Contains(got, "job(s)") {
		t.Errorf("idle member's preview = %q, want no job effect", got)
	}
}
