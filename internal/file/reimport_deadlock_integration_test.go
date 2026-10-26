package file_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/parsers/common"
	"openloc/internal/testutil"
)

// TestIntegrationReimportXLIFF_QueuesBehindStepChange pins that a re-import
// that meets a step change on its job waits for it instead of deadlocking
// with its recount, then lands every row.
func TestIntegrationReimportXLIFF_QueuesBehindStepChange(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	rows := []reimportFixtureRow{
		{Key: "u1", SourceText: "One", Status: db.TranslationStatusNeedsReview},
		{Key: "u2", SourceText: "Two", Status: db.TranslationStatusNeedsReview},
		{Key: "u3", SourceText: "Three", Status: db.TranslationStatusNeedsReview},
	}
	fx := seedReimportFixture(t, pool, q, "ReimportDeadlock", "Deadlock", []string{"fr"}, "fr", rows)
	job := testutil.SeedJob(t, q, fx.FileID, "fr")
	testutil.SeedWorkflow(t, q, fx.SettingsProfileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translation", Status: "in_progress", Assignee: &fx.AdminID})

	incoming := []common.Segment{
		{Key: "u1", Text: "One", Target: "Un"},
		{Key: "u2", Text: "Two", Target: "Deux"},
		{Key: "u3", Text: "Three", Target: "Trois"},
	}
	var res *file.ReimportXLIFFResult
	testutil.ExpectQueuedBehindStepChange(t, pool, job.ID, fx.TranslationByKey["u3"], func() error {
		var err error
		res, err = file.ReimportXLIFF(ctx, pool, file.ReimportXLIFFRequest{
			FileID: fx.FileID, TargetLanguage: "fr", IncomingSegments: incoming,
			EditedBy: fx.AdminID, ProjectID: fx.ProjectID, TenantID: fx.TenantID, FileName: "test.xliff",
		})
		return err
	})
	if !res.Applied || res.Updated != 3 {
		t.Fatalf("result: applied %v, updated %d; want applied, 3", res.Applied, res.Updated)
	}
	for key, want := range map[string]string{"u1": "Un", "u2": "Deux", "u3": "Trois"} {
		if got := loadTranslation(t, pool, fx.TranslationByKey[key]).ActiveText; got != want {
			t.Errorf("%s: active_text %q, want %q", key, got, want)
		}
	}
}
