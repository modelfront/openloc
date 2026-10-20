package project_test

import (
	"context"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

func TestIntegrationProjectCreate_WizardCarriesPenalties(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "WizardPenaltyCorp")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "test@test.com", "pass", "project_manager")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/projects", map[string]any{
		"name": "Wizard penalties", "source_language": "en", "target_languages": []string{"fr"},
		"settings": map[string]any{"sublanguage_penalty_pct": 7, "near_exact_penalty_pct": 3},
	}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct {
		Data struct {
			SettingsProfileID int64 `json:"settings_profile_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &created)
	sp, err := q.GetSettingsProfile(ctx, created.Data.SettingsProfileID)
	if err != nil {
		t.Fatalf("GetSettingsProfile: %v", err)
	}
	if sp.SublanguagePenaltyPct != 7 || sp.NearExactPenaltyPct != 3 {
		t.Errorf("wizard penalties stored as %d/%d, want 7/3", sp.SublanguagePenaltyPct, sp.NearExactPenaltyPct)
	}
}
