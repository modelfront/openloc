package pipeline_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// One TM write of a shared segment in two languages stores each language's
// own neighbours: German split the row before it, so its previous neighbour
// is the second piece, while French still has the imported row.
func TestIntegrationTMWriteBack_ContextPerLanguage(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "LanguagesCorp")
	tm := testutil.SeedTM(t, q, tenant.ID, "Languages TM")
	project := testutil.SeedProject(t, q, tenant.ID, "Languages", "en", []string{"de", "fr"})
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}
	file := testutil.SeedFile(t, q, project.ID, "doc.json", db.FileFormatJsonI18n)
	alpha := testutil.SeedSegment(t, q, file.ID, "Alpha", 0)
	beta := testutil.SeedSegment(t, q, file.ID, "Beta", 1)

	// French keeps the imported rows; German split Alpha into two pieces.
	testutil.SeedTranslation(t, q, alpha.ID, "fr", db.TranslationStatusNeedsReview)
	frBeta := testutil.SeedTranslation(t, q, beta.ID, "fr", db.TranslationStatusNeedsReview)
	for i, piece := range []string{"Al", "pha"} {
		seg := testutil.SeedLanguageSegment(t, q, file.ID, "de", piece, 0, int32(i), "alpha")
		testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	}
	deBeta := testutil.SeedTranslation(t, q, beta.ID, "de", db.TranslationStatusNeedsReview)

	for id, text := range map[int64]string{deBeta.ID: "BetaDE", frBeta.ID: "BetaFR"} {
		tgt := text
		if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: id, HumanText: &tgt}); err != nil {
			t.Fatal(err)
		}
	}
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if n, err := svc.SaveTranslationsToTM(ctx, tenant.ID, []int64{deBeta.ID, frBeta.ID}, pipeline.TMReasonCATConfirm); err != nil || n != 2 {
		t.Fatalf("SaveTranslationsToTM: %d writes, %v; want 2", n, err)
	}

	for lang, prev := range map[string]string{"de": "pha", "fr": "Alpha"} {
		entry, err := q.LookupTM(ctx, db.LookupTMParams{TmID: tm.ID, SourceLanguage: "en", TargetLanguage: lang, SourceText: "Beta"})
		if err != nil {
			t.Fatalf("LookupTM %s: %v", lang, err)
		}
		if entry.PrevSourceHash == nil || *entry.PrevSourceHash != md5hex(prev) {
			t.Errorf("%s Beta prev_source_hash = %v, want md5(%q) %s", lang, entry.PrevSourceHash, prev, md5hex(prev))
		}
	}
}
