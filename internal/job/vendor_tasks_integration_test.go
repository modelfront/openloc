package job_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type taskListResp struct {
	Data []struct {
		JobID     int64 `json:"job_id"`
		ProjectID int64 `json:"project_id"`
	} `json:"data"`
}

// TestTasksEndpointAndVendorAccess covers the Tasks page query + the vendor
// lockdown in one router-level flow: a vendor sees only the jobs assigned to
// them, is blocked from the management surface, and can open only their own
// assigned job.
func TestTasksEndpointAndVendorAccess(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "VendorCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	vendor := testutil.SeedUserWithRole(t, q, tenant.ID, "vendor@test.com", "pass", "linguist")

	project := testutil.SeedProject(t, q, tenant.ID, "Proj", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatJsonI18n)
	assignedJob := testutil.SeedJob(t, q, file.ID, "de")
	otherJob := testutil.SeedJob(t, q, file.ID, "fr")
	testutil.SeedJobStep(t, q, project.SettingsProfileID, assignedJob.ID, &vendor.ID)
	testutil.SeedJobStep(t, q, project.SettingsProfileID, otherJob.ID, &owner.ID)

	// Vendor's tasks = only the job assigned to them.
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/tasks", nil, vendor.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var tasks taskListResp
	testutil.DecodeJSON(t, rec, &tasks)
	if len(tasks.Data) != 1 || tasks.Data[0].JobID != assignedJob.ID {
		t.Fatalf("vendor tasks: expected [%d], got %+v", assignedJob.ID, tasks.Data)
	}

	// Vendor is blocked (403) from the whole management surface.
	for _, path := range []string{
		"/api/projects", "/api/tms", "/api/customers",
		"/api/terminology", "/api/templates", "/api/qa-profiles",
		fmt.Sprintf("/api/projects/%d/jobs", project.ID),
	} {
		rec := testutil.DoRequestWithRole(t, router, "GET", path, nil, vendor.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
	}

	// Vendor can open their assigned job, but NOT an unassigned one.
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/jobs/%d", assignedJob.ID), nil, vendor.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/jobs/%d", otherJob.ID), nil, vendor.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Owner is an admin-tier role, so it sees EVERYONE's tasks.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/projects", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/tasks", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &tasks)
	if len(tasks.Data) != 2 {
		t.Fatalf("owner (admin-tier) should see everyone's tasks (2), got %+v", tasks.Data)
	}
}

// TestAssignJob covers the PM "Assign" action: only admins may assign; a
// successful assign surfaces the job on the assignee's Tasks page; and a
// cross-tenant assignee is rejected.
func TestAssignJob(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "AssignCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	vendor := testutil.SeedUserWithRole(t, q, tenant.ID, "vendor@test.com", "pass", "linguist")
	linguist2 := testutil.SeedUserWithRole(t, q, tenant.ID, "ling2@test.com", "pass", "linguist")

	project := testutil.SeedProject(t, q, tenant.ID, "Proj", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &owner.ID)

	assignPath := fmt.Sprintf("/api/jobs/%d/assign", job.ID)

	// A non-manager (linguist) cannot assign.
	rec := testutil.DoRequestWithRole(t, router, "POST", assignPath, map[string]int64{"user_id": vendor.ID}, linguist2.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Owner assigns the job to the vendor.
	rec = testutil.DoRequestWithRole(t, router, "POST", assignPath, map[string]int64{"user_id": vendor.ID}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// The vendor now sees the job on their Tasks page.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/tasks", nil, vendor.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var tasks taskListResp
	testutil.DecodeJSON(t, rec, &tasks)
	if len(tasks.Data) != 1 || tasks.Data[0].JobID != job.ID {
		t.Fatalf("expected vendor to see assigned job %d, got %+v", job.ID, tasks.Data)
	}

	// A user from another tenant cannot be assigned.
	otherTenant := testutil.SeedTenant(t, q, "OtherCorp")
	stranger := testutil.SeedUser(t, q, otherTenant.ID, "stranger@test.com", "pass")
	rec = testutil.DoRequestWithRole(t, router, "POST", assignPath, map[string]int64{"user_id": stranger.ID}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}
