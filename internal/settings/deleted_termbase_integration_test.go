package settings_test

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/settings"
	"openloc/internal/testutil"
)

// A deleted term base is soft-deleted, so its assignment rows stay. Settings
// reads and profile copies must not see them: a copied ghost assignment makes
// a project created from the template fail on a term base that is gone.
func TestIntegrationDeletedTermBase_LeavesNoAssignment(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "GhostCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "ghost@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Ghost Project", "en", []string{"fr"})
	target := testutil.SeedProject(t, q, tenant.ID, "Copy Target", "en", []string{"fr"})
	kept := testutil.SeedTermBase(t, q, tenant.ID, "Kept Base", "en", "fr")
	gone := testutil.SeedTermBase(t, q, tenant.ID, "Deleted Base", "en", "fr")

	rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/terminology", project.ID), map[string]any{
		"assignments": []map[string]any{
			{"term_base_id": kept.ID, "source_language": "en", "target_language": "fr", "mode": "read", "priority": 0},
			{"term_base_id": gone.ID, "source_language": "en", "target_language": "fr", "mode": "read", "priority": 1},
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/terminology/%d", gone.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", project.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got struct {
		Data struct {
			TermAssignments []struct {
				TermBaseID int64 `json:"term_base_id"`
			} `json:"term_assignments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &got)
	if len(got.Data.TermAssignments) != 1 || got.Data.TermAssignments[0].TermBaseID != kept.ID {
		t.Fatalf("settings after delete: want only term base %d, got %+v", kept.ID, got.Data.TermAssignments)
	}

	if err := settings.CopyAssignments(testutil.Ctx(), q, project.SettingsProfileID, target.SettingsProfileID); err != nil {
		t.Fatalf("copy assignments: %v", err)
	}
	if n := testutil.CountRows(t, pool,
		`SELECT count(*) FROM settings_terminology_assignments WHERE settings_profile_id = $1 AND term_base_id = $2`,
		target.SettingsProfileID, gone.ID); n != 0 {
		t.Errorf("copy carried %d assignments of the deleted term base, want 0", n)
	}
	if n := testutil.CountRows(t, pool,
		`SELECT count(*) FROM settings_terminology_assignments WHERE settings_profile_id = $1 AND term_base_id = $2`,
		target.SettingsProfileID, kept.ID); n != 1 {
		t.Errorf("copy carried %d assignments of the kept term base, want 1", n)
	}
}
