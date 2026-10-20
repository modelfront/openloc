package flowtest

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationDeleteQAProfileBlockedWhenReferenced verifies that deleting a QA profile
// returns 409 Conflict when it's referenced by a project's settings profile.
func TestIntegrationDeleteQAProfileBlockedWhenReferenced(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "CascadeCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "cascade@test.com", "pass")

	// Create QA profile
	rec := testutil.DoRequest(t, router, "POST", "/api/qa-profiles",
		map[string]any{"name": "Test QA", "description": "test"},
		user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var qaResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &qaResp)
	qaID := qaResp.Data.ID

	// Create project and assign the QA profile to its settings
	proj := testutil.SeedProject(t, q, tenant.ID, "RefProject", "en", []string{"fr"})
	_, _ = q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID:             proj.SettingsProfileID,
		FuzzyThreshold: 0.75,
		MtOnUpload:     false,
		TmOnUpload:     false,
		QaProfileID:    &qaID,
	})

	// Try to delete the QA profile — should return 409 Conflict
	rec = testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/qa-profiles/%d", qaID), nil, user.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Verify QA profile still exists
	_, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: qaID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("QA profile should still exist after blocked delete: %v", err)
	}
}

// TestIntegrationDeleteTMBlockedWhileAssigned verifies that deleting a TM a
// project is assigned to returns 409 and keeps the TM.
func TestIntegrationDeleteTMBlockedWhileAssigned(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "TMCascadeCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "tmcasc@test.com", "pass")

	// Create TM
	tm := testutil.SeedTM(t, q, tenant.ID, "Referenced TM")

	// Create project and assign the TM
	proj := testutil.SeedProject(t, q, tenant.ID, "TMRefProject", "en", []string{"fr"})
	_, _ = q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: proj.SettingsProfileID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          1,
	})

	// Try to delete the TM — should return 409 Conflict
	rec := testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/tms/%d", tm.ID), nil, user.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Verify TM still exists
	_, err := q.GetTMCollection(ctx, db.GetTMCollectionParams{ID: tm.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("TM should still exist after blocked delete: %v", err)
	}
}
