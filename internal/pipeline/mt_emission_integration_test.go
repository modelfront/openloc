package pipeline_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// The MT phase must emit an `mt` history row per filled segment AND update the
// denormalized lineage (origin='mt'). The emission goes through the same
// EmitAutomationHistory helper as TM (see tm_emission_integration_test.go)
// but with kind='mt', mt_engine_id populated from the resolved engine, and
// tm_match_percent left NULL (MT carries no fuzzy %).
//
// Anti-tautology RED proof: comment out the EmitAutomationHistory call
// inside saveMTChunk in internal/pipeline/translate.go and rerun — the
// origin / history assertions flip to NULL / 0 rows. Verified locally
// 2026-05-26.

func TestIntegrationMTPhase_EmitsHistoryAndUpdatesLineage(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Seed: project + file + one segment, Mock MT engine assigned, no TM
	// entries (so TM phase skips and MT actually fires).
	tenantID, fileID, _ := seedTranslateContext(t, q, []string{"Hello world"})

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("TranslateFile (MT-only): %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 translation, got %d", len(rows))
	}
	tr := rows[0]

	// 1) translations.origin = 'mt'.
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginMt {
		t.Errorf("translations.origin: want 'mt', got valid=%v value=%v", tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}

	// 2) translations.tm_match_percent stays NULL — MT carries no TM percent.
	if tr.TmMatchPercent != nil {
		t.Errorf("translations.tm_match_percent: want NULL, got %v", *tr.TmMatchPercent)
	}

	// 3) Exactly one mt-kind history row.
	var historyCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM translation_history
		WHERE translation_id = $1 AND kind = 'mt'`, tr.ID).Scan(&historyCount); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyCount != 1 {
		t.Errorf("translation_history rows for kind=mt: want 1, got %d", historyCount)
	}

	// 4) History row carries text + mt_engine_id (FK to the mock engine) +
	//    NULL tm_match_percent.
	var (
		hText           *string
		hMtEngineID     *int64
		hTmMatchPercent *int16
	)
	if err := pool.QueryRow(ctx, `
		SELECT text, mt_engine_id, tm_match_percent
		FROM translation_history
		WHERE translation_id = $1 AND kind = 'mt'
		ORDER BY created_at DESC LIMIT 1`, tr.ID).Scan(&hText, &hMtEngineID, &hTmMatchPercent); err != nil {
		t.Fatalf("scan history row: %v", err)
	}
	if hText == nil || *hText == "" {
		t.Errorf("history.text: want non-empty MT output, got %v", hText)
	}
	if hMtEngineID == nil {
		t.Errorf("history.mt_engine_id: want non-NULL (FK to mt_engines), got NULL")
	}
	if hTmMatchPercent != nil {
		t.Errorf("history.tm_match_percent: want NULL for MT kind, got %v", *hTmMatchPercent)
	}
}
