package auth_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Removing a member releases every segment they hold in that org, and only
// there: their hold in another org stays.
func TestIntegrationRemoveMember_ReleasesTheirHolds(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "HoldLeave")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@holdleave.test", "pw", "owner")
	target := testutil.SeedUserWithRole(t, q, tenant.ID, "ana@holdleave.test", "pw", "project_manager")
	other := testutil.SeedTenant(t, q, "HoldStay")
	testutil.SeedMembership(t, q, target.ID, other.ID, "project_manager")

	holdIn := func(tenantID int64) int64 {
		p := testutil.SeedProject(t, q, tenantID, fmt.Sprintf("P%d", tenantID), "en", []string{"de"})
		file := testutil.SeedFile(t, q, p.ID, "doc.txt", db.FileFormatTxt)
		tr := testutil.SeedTranslation(t, q, testutil.SeedSegment(t, q, file.ID, "Hello", 0).ID, "de", db.TranslationStatusNeedsReview)
		job := testutil.SeedJob(t, q, file.ID, "de")
		testutil.SeedHold(t, q, tenantID, job.ID, tr.ID, target.ID)
		return tr.ID
	}
	here, there := holdIn(tenant.ID), holdIn(other.ID)

	rec := testutil.DoRequestWithRole(t, router, http.MethodDelete,
		fmt.Sprintf("/api/members/%d", target.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	held := func(translationID int64) bool {
		return testutil.CountRows(t, pool, "SELECT count(*) FROM translation_holds WHERE translation_id = $1", translationID) > 0
	}
	if held(here) {
		t.Error("the removed member still holds a segment in the org they left")
	}
	if !held(there) {
		t.Error("removal released the member's hold in another org")
	}
}
