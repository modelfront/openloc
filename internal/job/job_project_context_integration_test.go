package job_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Linguists are barred from /api/projects by design (RequireManager — asserted in
// TestTasksEndpointAndVendorAccess), so the CAT editor cannot get project context
// from the project list the way managers do. It has to come off the job the
// linguist is allowed to open.
//
// Without project_id + source_language here, the editor renders with a null
// project and degrades silently for linguists: concordance search has no
// project_id to scope the TM lookup so it no-ops with no error, and every
// SegmentRow loses its source language.
func TestJobDetailCarriesProjectContextForLinguists(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "ContextCorp")
	vendor := testutil.SeedUserWithRole(t, q, tenant.ID, "vendor@test.com", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Nordics Q3", "en-us", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &vendor.ID)

	var resp struct {
		Data struct {
			ProjectID      int64  `json:"project_id"`
			ProjectName    string `json:"project_name"`
			SourceLanguage string `json:"source_language"`
		} `json:"data"`
	}

	rec := testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/jobs/%d", job.ID), nil, vendor.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &resp)

	if resp.Data.ProjectID != project.ID {
		t.Errorf("project_id: want %d, got %d — concordance cannot scope its TM lookup without this", project.ID, resp.Data.ProjectID)
	}
	if resp.Data.SourceLanguage != "en-us" {
		t.Errorf("source_language: want %q, got %q — concordance would search the wrong language pair", "en-us", resp.Data.SourceLanguage)
	}
	if resp.Data.ProjectName != "Nordics Q3" {
		t.Errorf("project_name: want %q, got %q", "Nordics Q3", resp.Data.ProjectName)
	}

	// The same context must reach a manager unchanged — the editor reads one
	// field regardless of role, so a manager-only or linguist-only shape would
	// just move the bug.
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	rec = testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/jobs/%d", job.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.ProjectID != project.ID || resp.Data.SourceLanguage != "en-us" {
		t.Errorf("owner got project_id=%d source_language=%q, want %d/%q",
			resp.Data.ProjectID, resp.Data.SourceLanguage, project.ID, "en-us")
	}
}

// Cross-tenant guard: the new project read must stay tenant-scoped. A job id from
// another tenant has to 404 before it can leak that tenant's project name.
func TestJobDetailProjectContextIsTenantScoped(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	victim := testutil.SeedTenant(t, q, "VictimCorp")
	victimProject := testutil.SeedProject(t, q, victim.ID, "Secret Roadmap", "en", []string{"de"})
	victimFile := testutil.SeedFile(t, q, victimProject.ID, "f.txt", db.FileFormatJsonI18n)
	victimJob := testutil.SeedJob(t, q, victimFile.ID, "de")

	attackerTenant := testutil.SeedTenant(t, q, "AttackerCorp")
	attacker := testutil.SeedUser(t, q, attackerTenant.ID, "attacker@test.com", "pass")

	rec := testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/jobs/%d", victimJob.ID), nil, attacker.ID, attackerTenant.ID, "owner")
	if rec.Code == http.StatusOK {
		t.Fatalf("cross-tenant job read returned 200; body: %s", rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "Secret Roadmap") {
		t.Fatalf("cross-tenant response leaked the project name: %s", body)
	}
}
