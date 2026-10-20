package project_test

import (
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// PATCH /settings verifies qa_profile_id in the caller's tenant: a project
// must not point at another organization's QA profile.
func TestIntegrationUpdateSettingsRefusesForeignQAProfile(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()

	tenantA := testutil.SeedTenant(t, q, "QAOwnerA")
	tenantB := testutil.SeedTenant(t, q, "QAOwnerB")
	userA := testutil.SeedUser(t, q, tenantA.ID, "qa-a@test.com", "pass")
	project := testutil.SeedProject(t, q, tenantA.ID, "QA tenant", "en", []string{"fr"})
	own, err := q.CreateQAProfile(ctx, db.CreateQAProfileParams{TenantID: tenantA.ID, Name: "Ours"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := q.CreateQAProfile(ctx, db.CreateQAProfileParams{TenantID: tenantB.ID, Name: "Theirs"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/projects/" + itoa(project.ID) + "/settings"

	rec := testutil.DoRequest(t, router, "PATCH", path, map[string]any{"qa_profile_id": foreign.ID}, userA.ID, tenantA.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	profile, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.QaProfileID != nil {
		t.Fatalf("a foreign QA profile was stored: %d", *profile.QaProfileID)
	}

	rec = testutil.DoRequest(t, router, "PATCH", path, map[string]any{"qa_profile_id": own.ID}, userA.ID, tenantA.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	profile, err = q.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.QaProfileID == nil || *profile.QaProfileID != own.ID {
		t.Fatalf("own QA profile not stored: %v", profile.QaProfileID)
	}
}
