package pipeline_test

import (
	"context"
	"fmt"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// otherTargets are the extra target languages seeded next to the one under
// test. A phase runs for one target language, so their rows must never leave
// the database.
var otherTargets = []string{"de", "es", "it"}

// The TM and MT phases of a file with four target languages read only the rows
// of the language they translate.
func TestIntegrationTranslateFile_ReadsOnlyItsTargetLanguage(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	sources := []string{"One", "Two", "Three", "Four", "Five"}
	tenantID, fileID, _ := seedTranslateContext(t, q, sources)
	segs, err := q.ListSegmentSourcesByFile(ctx, fileID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range segs {
		for _, lang := range otherTargets {
			testutil.SeedTranslation(t, q, s.ID, lang, db.TranslationStatusNeedsReview)
		}
	}

	cpool, log := testutil.CountingPool(t)
	svc := pipeline.NewService(db.New(cpool), pipeline.NewPgxTxRunner(cpool), nil)
	log.Reset()
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	for _, name := range []string{"ListTMTargetableByFile", "ListUntranslatedByFile"} {
		if log.Count(name) == 0 {
			t.Fatalf("%s did not run", name)
		}
		if got := log.Rows(name); got > int64(len(sources)) {
			t.Errorf("%s read %d rows, want at most %d (the fr rows only)", name, got, len(sources))
		}
	}
}

// The QE+APE step of a file with four target languages reads only the rows of
// the job's language.
func TestIntegrationRunAIStep_ReadsOnlyItsTargetLanguage(t *testing.T) {
	srv := usageStub(t, func(string) string { return `"approved":true,"score":95` })
	e := newAIUsageEnv(t, srv.URL)
	ctx := context.Background()

	// The env seeds one fr segment; add two more, every segment MT-filled in
	// fr and in the other languages.
	for i := 1; i <= 2; i++ {
		seg := testutil.SeedSegment(t, e.q, e.file.ID, "More text", int32(i))
		testutil.SeedTranslation(t, e.q, seg.ID, "fr", db.TranslationStatusNeedsReview)
	}
	segs, err := e.q.ListSegmentSourcesByFile(ctx, e.file.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range segs {
		for _, lang := range otherTargets {
			testutil.SeedTranslation(t, e.q, s.ID, lang, db.TranslationStatusNeedsReview)
		}
	}
	if _, err := e.pool.Exec(ctx, `
		UPDATE translations t SET active_text = 'MT text', origin = 'mt'
		FROM segments s WHERE s.id = t.segment_id AND s.file_id = $1`, e.file.ID); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}

	engine := e.engine(t, "GPT reviewer", "qe_ape", true)
	job := testutil.SeedJob(t, e.q, e.file.ID, "fr")
	routerID := testutil.SeedRouterForEngine(t, e.q, e.tenant.ID, engine.ID)
	testutil.SeedAIJobStepWithRouter(t, e.q, e.project.SettingsProfileID, job.ID, routerID)

	cpool, log := testutil.CountingPool(t)
	svc := pipeline.NewService(db.New(cpool), pipeline.NewPgxTxRunner(cpool), nil)
	if _, err := svc.ConfigureAI(testutil.TestAISecretsKey, nil); err != nil {
		t.Fatalf("ConfigureAI: %v", err)
	}
	log.Reset()
	res, err := svc.RunAIStep(ctx, e.tenant.ID, job.ID, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if res.Total != len(segs) {
		t.Fatalf("reviewed %d segments, want %d", res.Total, len(segs))
	}
	if got := log.Rows("ListPreTranslatedByFile"); got != int64(len(segs)) {
		t.Errorf("ListPreTranslatedByFile read %d rows, want %d (the fr rows only)", got, len(segs))
	}
}

// The TM phase sets the trigram threshold once per chunk of segments, not
// once per fuzzy lookup in a transaction of its own.
func TestIntegrationTranslateFile_FuzzyLookupsShareATransaction(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	sources := make([]string, 60)
	for i := range sources {
		// Unlike each other and the TM entry, so every lookup misses.
		sources[i] = fmt.Sprintf("zq%dxv kw%dpj", i*7919, i*104729)
	}
	tenantID, fileID, tmID := seedTranslateContext(t, q, sources)
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Reset your password", TargetText: "Phrase", CreatedBy: "test",
	}); err != nil {
		t.Fatal(err)
	}

	cpool, log := testutil.CountingPool(t)
	svc := pipeline.NewService(db.New(cpool), pipeline.NewPgxTxRunner(cpool), nil)
	log.Reset()
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	if c := log.Count("FuzzyLookupTM"); c < len(sources)-1 {
		t.Fatalf("FuzzyLookupTM ran %d times, want one per unmatched segment", c)
	}
	if c := log.Count("SetTrgmSimilarityThreshold"); c > 1 {
		t.Errorf("threshold set %d times for %d lookups, want once per chunk", c, len(sources))
	}
	if c := log.CountPrefix("BEGIN"); c > 5 {
		t.Errorf("%d transactions for %d fuzzy lookups, want one per chunk", c, len(sources))
	}
}
