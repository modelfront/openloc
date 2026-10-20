package job_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Apply TM and Apply MT record what they filled in the project history, the
// same counts a pre-translation records: exact and fuzzy TM matches, and the
// segments MT translated with the engine's name.
func TestIntegrationApplyPhases_HistoryEventCarriesCounts(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ApplyCountsCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@applycounts.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Apply Counts", "en", []string{"de"})

	tm := testutil.SeedTM(t, q, tenant.ID, "Counts TM")
	testutil.SeedTMAssignment(t, q, project.SettingsProfileID, tm.ID, "read_write", 1)
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Hello world", TargetText: "Hallo Welt", CreatedBy: "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}
	engine := testutil.SeedMTEngine(t, q, tenant.ID, "Acme MT", "mock")
	testutil.SeedMTRouterForProfile(t, q, tenant.ID, project.SettingsProfileID, &engine.ID, nil)

	file := testutil.SeedFile(t, q, project.ID, "counts.txt", db.FileFormatTxt)
	for i, src := range []string{"Hello world", "Nothing stored for this one", "Nor for this one"} {
		seg := testutil.SeedSegment(t, q, file.ID, src, int32(i))
		testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	}
	job := testutil.SeedJob(t, q, file.ID, "de")

	for _, step := range []string{"tm-apply", "mt-apply"} {
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/%s", job.ID, step), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
	}

	details := map[string]map[string]any{}
	for _, ev := range testutil.ProjectEvents(t, q, project.ID, tenant.ID, "") {
		details[ev.EventType] = ev.Data
	}

	tmRun := details["job.tm_apply_started"]
	if tmRun["tm_exact"] != float64(1) || tmRun["tm_fuzzy"] != float64(0) {
		t.Errorf("Apply TM event = %v, want tm_exact 1 and tm_fuzzy 0", tmRun)
	}
	if names, _ := tmRun["tm_names"].([]any); len(names) != 1 || names[0] != "Counts TM" {
		t.Errorf("Apply TM event tm_names = %v, want [Counts TM]", tmRun["tm_names"])
	}
	if _, present := tmRun["mt_translated"]; present {
		t.Errorf("Apply TM ran no MT, but its event carries mt_translated: %v", tmRun)
	}
	mtRun := details["job.mt_apply_started"]
	if mtRun["mt_translated"] != float64(2) || mtRun["mt_engine"] != "Acme MT" {
		t.Errorf("Apply MT event = %v, want mt_translated 2 with Acme MT", mtRun)
	}
	if _, present := mtRun["tm_exact"]; present {
		t.Errorf("Apply MT ran no TM, but its event carries tm_exact: %v", mtRun)
	}
}
