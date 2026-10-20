package file_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// GET /api/files/{fileID}/download — the source-file option in the Download
// menu.
//
// This route deliberately lives on the file group rather than the manager-gated
// project group, so the access boundary is what these tests pin: the tenant
// gate, and the linguist gate that lets an assigned vendor pull the source
// document from the CAT editor while keeping unassigned ones out.
//
// The byte-for-byte round trip is covered where a storage stub can be injected:
// TestIntegration_ProjectDownload_OriginalReturnsUploadedBytes in
// internal/pipeline. The shared test router runs with Storage: nil, so the
// happy path here stops at the storage guard (503) — which is exactly the
// signal that the request cleared ownership, tenancy and assignment.

func seedDownloadableFile(t *testing.T, q db.Querier, tenantID int64, projectName string) (db.Project, db.File) {
	t.Helper()
	project := testutil.SeedProject(t, q, tenantID, projectName, "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "brief.txt", db.FileFormatTxt)
	return project, file
}

// TestIntegrationDownloadSourceCrossTenantBlocked — a file id from another
// tenant must 404, never leak the document.
func TestIntegrationDownloadSourceCrossTenantBlocked(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	owner := testutil.SeedTenant(t, q, "OwnerCorp")
	_, file := seedDownloadableFile(t, q, owner.ID, "OwnerProject")

	intruder := testutil.SeedTenant(t, q, "IntruderCorp")
	intruderUser := testutil.SeedUser(t, q, intruder.ID, "intruder@test.com", "pass123")

	rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/files/%d/download", file.ID), nil,
		intruderUser.ID, intruder.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant source download, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestIntegrationDownloadSourceLinguistGate — vendors may pull the source of a
// file they're assigned to, and only that one. This is the whole reason the
// route isn't on the project group.
func TestIntegrationDownloadSourceLinguistGate(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "LingCorp")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@test.com", "pass", "linguist")

	assignedProject, assignedFile := seedDownloadableFile(t, q, tenant.ID, "Assigned")
	assignedJob := testutil.SeedJob(t, q, assignedFile.ID, "es")
	testutil.SeedJobStep(t, q, assignedProject.SettingsProfileID, assignedJob.ID, &linguist.ID)

	// A second file in the same tenant that nobody assigned to them.
	_, foreignFile := seedDownloadableFile(t, q, tenant.ID, "NotTheirs")

	// Assigned → clears every gate and reaches the handler. Storage is nil in
	// the test router, so the guard's 503 is the "got all the way through" signal.
	rec := testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/files/%d/download", assignedFile.ID), nil, linguist.ID, tenant.ID, "linguist")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("assigned linguist must reach the download handler, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unassigned → blocked by RequireLinguistFileAccess before anything is read.
	rec = testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/files/%d/download", foreignFile.ID), nil, linguist.ID, tenant.ID, "linguist")
	if rec.Code != http.StatusForbidden {
		t.Errorf("unassigned linguist must be blocked, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestIntegrationDownloadSourceManagerReachesHandler — managers need no
// assignment. Same 503-means-cleared-the-gates reasoning as above.
func TestIntegrationDownloadSourceManagerReachesHandler(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "MgrCorp")
	admin := testutil.SeedUserWithRole(t, q, tenant.ID, "admin@test.com", "pass", "admin")
	_, file := seedDownloadableFile(t, q, tenant.ID, "MgrProject")

	rec := testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/files/%d/download", file.ID), nil, admin.ID, tenant.ID, "admin")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("admin must reach the download handler, got %d: %s", rec.Code, rec.Body.String())
	}
}
