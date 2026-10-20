package translationmemory_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A fuzzy threshold of 0 lists every candidate the lookup recalls, in the
// panel as in pre-translation: a hit scoring under 50% must show.
func TestIntegrationTMMatch_ZeroThresholdListsWeakHits(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()

	tenant := testutil.SeedTenant(t, q, "TM-Zero-Threshold")
	user := testutil.SeedUser(t, q, tenant.ID, "u@zero.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"es"})
	if _, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{ID: project.SettingsProfileID, FuzzyThreshold: 0}); err != nil {
		t.Fatalf("set threshold: %v", err)
	}
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")
	tm := testutil.SeedTM(t, q, tenant.ID, "Zero TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: tm.ID, Mode: "read", Priority: 0,
	}); err != nil {
		t.Fatalf("assign TM: %v", err)
	}
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Reset your password.", TargetText: "Restablece tu contraseña.", CreatedBy: "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	path := fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=%s", job.ID, url.QueryEscape("Please open the link we sent to reset your password."))
	rec := testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Matches []struct {
			Score float64 `json:"score"`
		} `json:"matches"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if len(resp.Matches) != 1 {
		t.Fatalf("want the weak hit listed at threshold 0, got %+v", resp.Matches)
	}
	if s := resp.Matches[0].Score; s >= 0.5 {
		t.Fatalf("fixture: score %v must be under 0.5 to prove threshold 0 is not read as 50%%", s)
	}
}
