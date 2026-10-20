package pipeline_test

import (
	"context"
	"reflect"
	"testing"

	"openloc/internal/pipeline"
	"openloc/internal/settings"
	"openloc/internal/testutil"
)

// A full pre-translation reports what it filled, for the project history:
// exact TM matches (confirmed automatically), fuzzy ones (left for review —
// including a byte-identical source that pays the sublanguage penalty), the
// memories that gave them (not one assigned but matching nothing), and the
// segments machine translation filled, with the engine's own name rather than
// its type.
func TestIntegrationTranslateFile_OutcomeCountsWhatEachPhaseFilled(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContextLangs(t, q, []string{
		"Hello world",  // exact, same locales
		"Good morning", // same text on base codes: penalised, so fuzzy
		"The quick brown fox jumps over the lazy dog", // fuzzy
		"Completely unrelated text here",              // MT
		"Another sentence nobody stored",              // MT
	}, "en-us", "fr-fr")

	for _, e := range []struct{ src, tgt, srcLang, tgtLang string }{
		{"Hello world", "Bonjour le monde", "en-us", "fr-fr"},
		{"Good morning", "Bonjour", "en", "fr"},
		{"The quick brown fox jumped over the lazy dog", "Le renard brun rapide a sauté", "en-us", "fr-fr"},
	} {
		testutil.SeedTMEntry(t, q, tmID, e.srcLang, e.tgtLang, e.src, e.tgt)
	}

	// A second memory, assigned after the first, that matches nothing.
	unused := testutil.SeedTM(t, q, tenantID, "Unused TM")
	testutil.SeedTMAssignment(t, q, settingsProfileIDForFile(t, q, tenantID, fileID), unused.ID, "read", 2)

	// The router's engine is named by the organization; the History shows that
	// name, not the engine type.
	engine := testutil.SeedMTEngine(t, q, tenantID, "Acme MT", "mock")
	router := testutil.SeedMTRouter(t, q, tenantID, "Acme router", &engine.ID, nil)
	testutil.SetProfileMTRouter(t, q, settingsProfileIDForFile(t, q, tenantID, fileID), &router)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	outcome, err := svc.TranslateFile(ctx, tenantID, fileID, "en-us", "fr-fr", nil, pipeline.FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	want := pipeline.TranslateOutcome{TMExact: 1, TMFuzzy: 2, TMNames: []string{"Test TM"}, MTTranslated: 2, MTEngine: "Acme MT"}
	if !reflect.DeepEqual(outcome, want) {
		t.Errorf("outcome = %+v, want %+v", outcome, want)
	}

	// A second run: the confirmed exact match is no longer TM-targetable, the
	// two fuzzy rows are (still pre-translated) and are filled again, and MT
	// has nothing left.
	again, err := svc.TranslateFile(ctx, tenantID, fileID, "en-us", "fr-fr", nil, pipeline.FullPipeline)
	if err != nil {
		t.Fatalf("second TranslateFile: %v", err)
	}
	wantAgain := pipeline.TranslateOutcome{TMFuzzy: 2, TMNames: []string{"Test TM"}, MTSkipReason: pipeline.PhaseSkipNoSegments}
	if !reflect.DeepEqual(again, wantAgain) {
		t.Errorf("second run outcome = %+v, want %+v", again, wantAgain)
	}
}

// With two memories that each fill a segment, the outcome names both in the
// project's priority order, and credits each with its own match. Under
// match_percent the second memory's exact match is found before the first
// memory's fuzzy one, so the names do not follow the order of filling.
func TestIntegrationTranslateFile_OutcomeNamesEveryMemoryInPriorityOrder(t *testing.T) {
	for _, order := range []string{settings.TMLookupOrderMatchPercent, settings.TMLookupOrderTMOrder} {
		t.Run(order, func(t *testing.T) {
			pool, q, cleanup := testutil.SetupTestDB(t)
			defer cleanup()
			ctx := context.Background()

			tenantID, fileID, first := seedTranslateContextLangs(t, q, []string{
				"Hello world", // exact, only in the second memory
				"The quick brown fox jumps over the lazy dog", // fuzzy, only in the first
			}, "en-us", "fr-fr")
			profileID := settingsProfileIDForFile(t, q, tenantID, fileID)
			testutil.SetTMLookupOrder(t, q, profileID, order)
			second := testutil.SeedTM(t, q, tenantID, "Client TM")
			testutil.SeedTMAssignment(t, q, profileID, second.ID, "read", 2)
			for _, e := range []struct {
				tmID     int64
				src, tgt string
			}{
				{first, "The quick brown fox jumped over the lazy dog", "Le renard brun rapide a sauté"},
				{second.ID, "Hello world", "Bonjour le monde"},
			} {
				testutil.SeedTMEntry(t, q, e.tmID, "en-us", "fr-fr", e.src, e.tgt)
			}

			svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
			outcome, err := svc.TranslateFile(ctx, tenantID, fileID, "en-us", "fr-fr", nil, pipeline.TranslatePhases{UseTM: true})
			if err != nil {
				t.Fatalf("TranslateFile: %v", err)
			}
			want := pipeline.TranslateOutcome{TMExact: 1, TMFuzzy: 1, TMNames: []string{"Test TM", "Client TM"}}
			if !reflect.DeepEqual(outcome, want) {
				t.Errorf("outcome = %+v, want %+v", outcome, want)
			}
		})
	}
}

// An LLM translation engine is named the same way: the AI engine's own name.
func TestIntegrationTranslateFile_OutcomeNamesTheAIEngine(t *testing.T) {
	srv := usageStub(t, func(string) string { return `"target":"Bonjour le monde"` })
	e := newAIUsageEnv(t, srv.URL)
	engine := e.engine(t, "GPT translator", "mt", false)
	testutil.SeedMTRouterForProfile(t, e.q, e.tenant.ID, e.project.SettingsProfileID, nil, &engine.ID)

	outcome, err := e.svc.TranslateFile(context.Background(), e.tenant.ID, e.file.ID, "en", "fr", nil,
		pipeline.TranslatePhases{UseMT: true})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	if outcome.MTTranslated != 1 || outcome.MTEngine != "GPT translator" {
		t.Errorf("outcome = %+v, want 1 segment with GPT translator", outcome)
	}
}
