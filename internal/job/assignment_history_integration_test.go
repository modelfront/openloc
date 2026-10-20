package job_test

import (
	"fmt"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Assigning a job or reassigning a step names the assignee in the project
// History by their display name, or by their email when they have no name.
// Both people go through both endpoints, so a site that wrote the email in
// place of the name fails on the named user.
func TestIntegrationAssignmentHistoryNamesTheAssignee(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AssignNamesCo")
	owner := testutil.SeedUser(t, q, tenant.ID, "pm@assign-names.test", "pass")
	named := testutil.SeedUserWithRole(t, q, tenant.ID, "jane@assign-names.test", "pass", "linguist")
	nameless := testutil.SeedUserWithRole(t, q, tenant.ID, "nameless@assign-names.test", "pass", "linguist")
	testutil.SetUserName(t, q, named.ID, "Jane Linguist")
	testutil.SetUserName(t, q, nameless.ID, "")

	project := testutil.SeedProject(t, q, tenant.ID, "Assign Names", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "guide.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	assignment := testutil.SeedWorkflow(t, q, project.SettingsProfileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translation", Status: "in_progress"})[0]

	jobPath := fmt.Sprintf("/api/jobs/%d/assign", job.ID)
	stepPath := fmt.Sprintf("/api/jobs/%d/workflow/steps/%d/assign", job.ID, assignment)
	seen := 0
	for _, call := range []struct {
		path, eventType, wantName string
		userID                    int64
	}{
		{jobPath, "job.assigned", "Jane Linguist", named.ID},
		{jobPath, "job.assigned", "nameless@assign-names.test", nameless.ID},
		{stepPath, "workflow.step_assigned", "Jane Linguist", named.ID},
		{stepPath, "workflow.step_assigned", "nameless@assign-names.test", nameless.ID},
	} {
		rec := testutil.DoRequest(t, router, "POST", call.path, map[string]any{"user_id": call.userID}, owner.ID, tenant.ID)
		if rec.Code >= 300 {
			t.Fatalf("POST %s: %d %s", call.path, rec.Code, rec.Body.String())
		}

		// Newest first: the call's own event is the first one.
		events := testutil.ProjectEvents(t, q, project.ID, tenant.ID, "")
		if len(events) != seen+1 {
			t.Fatalf("POST %s for user %d: %d events, want %d (one per call)", call.path, call.userID, len(events), seen+1)
		}
		seen = len(events)
		d := events[0].Data
		if events[0].EventType != call.eventType || d["assignee_id"] != float64(call.userID) || d["assignee_name"] != call.wantName {
			t.Errorf("POST %s for user %d: %s with assignee %v named %v, want %s named %q",
				call.path, call.userID, events[0].EventType, d["assignee_id"], d["assignee_name"], call.eventType, call.wantName)
		}
	}
}
