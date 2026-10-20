package template_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Templates link, guard, detach and duplicate their TM profile link the same
// way projects do.
func TestIntegrationTemplateTMProfile(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TplCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "u@test.com", "pass")
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "T")
	profTM := testutil.SeedTM(t, q, tenant.ID, "Profile TM")
	tp := testutil.SeedTMProfile(t, q, tenant.ID, "Shared", 0.55)
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: tp.SettingsProfileID, TmID: profTM.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("assign profile TM: %v", err)
	}
	path := fmt.Sprintf("/api/templates/%d", tmpl.ID)

	rec := testutil.DoRequest(t, router, "PATCH", path, map[string]any{"tm_profile_id": tp.ID}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var got struct {
		Data struct {
			Template struct {
				FuzzyThreshold float32 `json:"fuzzy_threshold"`
			} `json:"template"`
			TMAssignments []struct {
				TmID int64 `json:"tm_id"`
			} `json:"tm_assignments"`
			TMProfile *struct {
				ID int64 `json:"id"`
			} `json:"tm_profile"`
		} `json:"data"`
	}
	rec = testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.TMProfile == nil || got.Data.TMProfile.ID != tp.ID || got.Data.Template.FuzzyThreshold != 0.55 || len(got.Data.TMAssignments) != 1 {
		t.Fatalf("linked template GET = %+v", got.Data)
	}

	// Guards.
	testutil.AssertStatus(t, testutil.DoRequest(t, router, "PATCH", path, map[string]any{"fuzzy_threshold": 0.9}, user.ID, tenant.ID), http.StatusConflict)
	testutil.AssertStatus(t, testutil.DoRequest(t, router, "PUT", path+"/tms", map[string]any{"assignments": []map[string]any{}}, user.ID, tenant.ID), http.StatusConflict)
	testutil.AssertStatus(t, testutil.DoRequest(t, router, "PATCH", path, map[string]any{"tm_profile_id": nil}, user.ID, tenant.ID), http.StatusBadRequest)

	// Duplicate carries the link.
	rec = testutil.DoRequest(t, router, "POST", path+"/duplicate", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var dup struct {
		Data struct {
			SettingsProfileID int64 `json:"settings_profile_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &dup)
	if sp, err := q.GetSettingsProfile(ctx, dup.Data.SettingsProfileID); err != nil || sp.TmProfileID == nil || *sp.TmProfileID != tp.ID {
		t.Errorf("duplicate link = %v err=%v, want %d", sp.TmProfileID, err, tp.ID)
	}

	// Detach copies and unlinks; save-as creates a profile from the copy.
	testutil.AssertStatus(t, testutil.DoRequest(t, router, "POST", path+"/tms/detach", nil, user.ID, tenant.ID), http.StatusOK)
	rec = testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.TMProfile != nil || got.Data.Template.FuzzyThreshold != 0.55 || len(got.Data.TMAssignments) != 1 {
		t.Errorf("detached template GET = %+v", got.Data)
	}
	rec = testutil.DoRequest(t, router, "POST", path+"/save-tm-profile", map[string]any{"name": "From template", "link": true}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	rec = testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.TMProfile == nil || got.Data.TMProfile.ID == tp.ID {
		t.Errorf("save-tm-profile with link must follow the NEW profile, got %+v", got.Data.TMProfile)
	}
}
