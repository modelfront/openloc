// PATCH /api/projects/{id}/settings round-trip for the TM match penalties.
// Each case below is a distinct user-visible behaviour: a penalty
// that doesn't persist silently changes how every TM match in the project
// scores, and whether it auto-confirms.
//
// Skipped with -short.
package project_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationProjectSettings_Penalties(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PenaltySettings")
	user := testutil.SeedUser(t, q, tenant.ID, "u@penalty.test", "pass")
	ctx := testutil.Ctx()

	project := testutil.SeedProject(t, q, tenant.ID, "Penalty Project", "en", []string{"fr"})
	settingsPath := fmt.Sprintf("/api/projects/%d/settings", project.ID)

	profile := func(t *testing.T) db.SettingsProfile {
		t.Helper()
		p, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
		if err != nil {
			t.Fatalf("GetSettingsProfile: %v", err)
		}
		return p
	}

	t.Run("defaults_match_the_column_defaults", func(t *testing.T) {
		p := profile(t)
		if p.SublanguagePenaltyPct != 1 || p.NearExactPenaltyPct != 1 {
			t.Errorf("defaults = %d/%d, want 1/1", p.SublanguagePenaltyPct, p.NearExactPenaltyPct)
		}
	})

	t.Run("persists_both_penalties", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "PATCH", settingsPath, map[string]any{
			"sublanguage_penalty_pct": 5,
			"near_exact_penalty_pct":  3,
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)

		p := profile(t)
		if p.SublanguagePenaltyPct != 5 || p.NearExactPenaltyPct != 3 {
			t.Errorf("stored = %d/%d, want 5/3", p.SublanguagePenaltyPct, p.NearExactPenaltyPct)
		}
	})

	// 0 is a real setting ("these locales are equivalent"), not "unset" — it has
	// to survive a round trip that a naive omitempty/zero check would swallow.
	t.Run("zero_is_stored_not_treated_as_absent", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "PATCH", settingsPath, map[string]any{
			"sublanguage_penalty_pct": 0,
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)

		if p := profile(t); p.SublanguagePenaltyPct != 0 {
			t.Errorf("sublanguage_penalty_pct = %d, want 0", p.SublanguagePenaltyPct)
		}
	})

	// An unrelated settings save must not reset the penalties. They're
	// nil-keeps-stored for exactly this reason — a full-replace update with a
	// zero-valued struct would wipe custom values.
	t.Run("absent_fields_leave_stored_values_alone", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "PATCH", settingsPath, map[string]any{
			"sublanguage_penalty_pct": 8,
			"near_exact_penalty_pct":  4,
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)

		rec = testutil.DoRequest(t, router, "PATCH", settingsPath, map[string]any{
			"fuzzy_threshold": 0.8,
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)

		p := profile(t)
		if p.SublanguagePenaltyPct != 8 || p.NearExactPenaltyPct != 4 {
			t.Errorf("penalties = %d/%d after an unrelated save, want 8/4", p.SublanguagePenaltyPct, p.NearExactPenaltyPct)
		}
	})

	// Clamped, not rejected — the columns carry CHECK constraints, so an
	// unclamped value would surface as a 500 instead of a sensible save.
	t.Run("out_of_range_is_clamped", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "PATCH", settingsPath, map[string]any{
			"sublanguage_penalty_pct": 150,
			"near_exact_penalty_pct":  -20,
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)

		p := profile(t)
		if p.SublanguagePenaltyPct != 100 || p.NearExactPenaltyPct != 0 {
			t.Errorf("clamped = %d/%d, want 100/0", p.SublanguagePenaltyPct, p.NearExactPenaltyPct)
		}
	})

	t.Run("get_settings_returns_them", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET", settingsPath, nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)

		var resp struct {
			Data struct {
				Project struct {
					SublanguagePenaltyPct int32 `json:"sublanguage_penalty_pct"`
					NearExactPenaltyPct   int32 `json:"near_exact_penalty_pct"`
				} `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)

		p := profile(t)
		if resp.Data.Project.SublanguagePenaltyPct != p.SublanguagePenaltyPct ||
			resp.Data.Project.NearExactPenaltyPct != p.NearExactPenaltyPct {
			t.Errorf("GET returned %d/%d, stored %d/%d",
				resp.Data.Project.SublanguagePenaltyPct, resp.Data.Project.NearExactPenaltyPct,
				p.SublanguagePenaltyPct, p.NearExactPenaltyPct)
		}
	})
}
