package translationmemory_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/settings"
	"openloc/internal/testutil"
)

// The editor's Matches panel lists matches in the order the project's TM
// lookup order picks them. Fixture: the first TM holds a
// fuzzy match, the second an exact one.
func TestIntegrationTMMatch_FollowsLookupOrder(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()

	tenant := testutil.SeedTenant(t, q, "TM-Order-Test")
	user := testutil.SeedUser(t, q, tenant.ID, "test@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Order Project", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")
	first := testutil.SeedTM(t, q, tenant.ID, "First TM")
	second := testutil.SeedTM(t, q, tenant.ID, "Second TM")
	for i, tm := range []db.TmCollection{first, second} {
		if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
			SettingsProfileID: project.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: int32(i),
		}); err != nil {
			t.Fatalf("CreateSettingsTMAssignment: %v", err)
		}
	}
	for _, e := range []struct {
		tmID     int64
		src, tgt string
	}{
		{first.ID, "Reset your password!", "Fuzzy from first"},
		{second.ID, "Reset your password", "Exact from second"},
	} {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: e.tmID, SourceLanguage: "en", TargetLanguage: "es",
			SourceText: e.src, TargetText: e.tgt, CreatedBy: "test",
		}); err != nil {
			t.Fatalf("InsertTM: %v", err)
		}
	}

	targets := func() []string {
		t.Helper()
		path := fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=%s", job.ID, url.QueryEscape("Reset your password"))
		rec := testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Matches []struct {
				TargetText string `json:"target_text"`
			} `json:"matches"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		out := make([]string, len(resp.Matches))
		for i, m := range resp.Matches {
			out[i] = m.TargetText
		}
		return out
	}

	t.Run("match_percent sorts by score", func(t *testing.T) {
		testutil.SetTMLookupOrder(t, q, project.SettingsProfileID, settings.TMLookupOrderMatchPercent)
		got := targets()
		if len(got) != 2 || got[0] != "Exact from second" || got[1] != "Fuzzy from first" {
			t.Errorf("expected [exact, fuzzy], got %v", got)
		}
	})
	t.Run("tm_order sorts by TM position first", func(t *testing.T) {
		testutil.SetTMLookupOrder(t, q, project.SettingsProfileID, settings.TMLookupOrderTMOrder)
		got := targets()
		if len(got) != 2 || got[0] != "Fuzzy from first" || got[1] != "Exact from second" {
			t.Errorf("expected [first TM's fuzzy, second TM's exact], got %v", got)
		}
	})
}
