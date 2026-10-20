package job_test

import (
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type taskCustomerResp struct {
	Data []struct {
		JobID        int64   `json:"job_id"`
		CustomerName *string `json:"customer_name"`
	} `json:"data"`
}

// The Tasks page shows each row's customer. Customer is a label,
// so the row has to resolve through the project's pick —
// projects.customer_id is no longer written by anything.
//
// Covers both task scopes: a linguist's own list and the
// owner's everybody view.
func TestTasksCarryCustomerFromLabel(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "LabelCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "linguist@test.com", "pass", "linguist")

	project := testutil.SeedProject(t, q, tenant.ID, "Labelled", "en", []string{"de"})
	testutil.SeedProjectSystemLabel(t, q, tenant.ID, project.ID, "customer", "Acme Legal")
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &linguist.ID)

	// A second project with no pick at all, so "unset" stays distinguishable
	// from "set but unresolved" — a broken join would make both read empty.
	bare := testutil.SeedProject(t, q, tenant.ID, "Bare", "en", []string{"fr"})
	bareFile := testutil.SeedFile(t, q, bare.ID, "g.txt", db.FileFormatJsonI18n)
	bareJob := testutil.SeedJob(t, q, bareFile.ID, "fr")
	testutil.SeedJobStep(t, q, bare.SettingsProfileID, bareJob.ID, &linguist.ID)

	for _, tc := range []struct {
		name   string
		userID int64
		role   string
	}{
		{"own tasks", linguist.ID, "linguist"},
		{"everybody's tasks", owner.ID, "owner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := testutil.DoRequestWithRole(t, router, "GET", "/api/tasks", nil, tc.userID, tenant.ID, tc.role)
			testutil.AssertStatus(t, rec, http.StatusOK)
			var tasks taskCustomerResp
			testutil.DecodeJSON(t, rec, &tasks)

			byJob := map[int64]*string{}
			for _, row := range tasks.Data {
				byJob[row.JobID] = row.CustomerName
			}
			got, ok := byJob[job.ID]
			if !ok {
				t.Fatalf("job %d missing from tasks: %+v", job.ID, tasks.Data)
			}
			if got == nil || *got != "Acme Legal" {
				t.Errorf("customer_name = %v, want %q — the Customer label's value", got, "Acme Legal")
			}
			if bareGot, ok := byJob[bareJob.ID]; !ok {
				t.Fatalf("job %d missing from tasks: %+v", bareJob.ID, tasks.Data)
			} else if bareGot != nil && *bareGot != "" {
				t.Errorf("customer_name = %q for a project with no pick, want unset", *bareGot)
			}
		})
	}
}
