package pipeline_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
	"openloc/internal/timing"
)

// TestIntegrationTranslateFile_RecordsStageTimings proves the whole
// stage-timing chain works from the pipeline's side: a recorder installed
// on the context by the caller (in production, the asynq handler's trackTask)
// is found by the spans buried in runTMPhase and runMTPhase, several call
// layers down.
//
// This is the failure mode worth a test. Every span is a no-op when the context
// carries no recorder — deliberately, so instrumented code stays callable from
// HTTP handlers and tests — which means a future refactor that passes a fresh
// context somewhere in the middle silently records nothing, builds fine, and
// breaks no other test.
//
// Anti-tautology RED proof: drop the `timing.Start` call from buildNeighborHashes
// and the tm.context_match assertion fails; swap the pipeline's ctx for
// context.Background() anywhere above the phases and all of them fail.
// Verified locally 2026-08-18.
func TestIntegrationTranslateFile_RecordsStageTimings(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world", "Goodbye world"})

	// One exact TM hit so the TM phase does real work (lookup + apply); the
	// other segment falls through to the mock MT engine.
	if _, err := q.InsertTM(context.Background(), db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	ctx, rec := timing.WithRecorder(context.Background())
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	stages := rec.Stages()
	if len(stages) == 0 {
		t.Fatal("no stages recorded — the pipeline's spans never found the context recorder")
	}

	// Stages that a TM+MT run over two segments must always exercise.
	for _, slug := range []string{
		timing.StageTMContextMatch,
		timing.StageTMExactLookup,
		timing.StageTMApply,
		timing.StageMTEngineCall,
		timing.StageMTDBSave,
	} {
		s, ok := stages[slug]
		if !ok {
			t.Errorf("stage %q not recorded; got %v", slug, keysOf(stages))
			continue
		}
		if s.N < 1 {
			t.Errorf("stage %q recorded with n=%d, want >= 1", slug, s.N)
		}
		if s.Err != 0 {
			t.Errorf("stage %q recorded %d errors on a clean run", slug, s.Err)
		}
	}

	// The MT span must name the engine it used — the meta is what makes
	// "is this engine slow for this tenant" answerable later.
	if got := stages[timing.StageMTEngineCall].Meta["engine"]; got == "" {
		t.Error("mt.engine_call recorded no engine in meta")
	}
}

func keysOf(m map[string]timing.Stage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
