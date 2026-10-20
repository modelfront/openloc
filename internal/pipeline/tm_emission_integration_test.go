package pipeline_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// TestIntegrationTMPhase_CreatedByAttribution exercises TM-phase provenance:
// when pipeline.WithTriggeredBy is set on ctx (as the async worker does
// after unwrapping PipelinePayload.TriggeredBy), the TM phase stamps
// translation_history.created_by with that user. Nil context stays NULL.
//
// Anti-tautology: drop the TriggeredByFromContext lookup from
// applyExactMatch's emission opts and this test goes RED. Verified
// locally 2026-05-26.
func TestIntegrationTMPhase_CreatedByAttribution(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
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

	// Seed a real user we can attribute to. The actual ID doesn't need
	// to come from auth — the worker layer obtains it from
	// PipelinePayload.TriggeredBy, which is the uploader (already in DB).
	user := testutil.SeedUser(t, q, tenantID, "history-attribution-tm@test.com", "pass123")
	uID := user.ID

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	attributedCtx := pipeline.WithTriggeredBy(ctx, &uID)
	if _, err := svc.TranslateFile(attributedCtx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTranslationsByFile: err=%v len=%d", err, len(rows))
	}
	tr := rows[0]

	var hCreatedBy *int64
	if err := pool.QueryRow(ctx, `
		SELECT created_by FROM translation_history
		WHERE translation_id = $1 AND kind = 'tm'
		ORDER BY created_at DESC LIMIT 1`, tr.ID).Scan(&hCreatedBy); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if hCreatedBy == nil || *hCreatedBy != uID {
		t.Errorf("history.created_by: want %d (from ctx), got %v", uID, hCreatedBy)
	}
}

// The TM phase (exact + fuzzy) must emit a `tm` history row AND update the
// denormalized lineage on translations (origin='tm', tm_match_percent set). The
// emission goes through the EmitAutomationHistory helper, which wraps the
// pair in a transaction so the columns can't drift.
//
// Anti-tautology RED proof for either subtest: comment out the
// EmitAutomationHistory call inside applyExactMatch / applyFuzzyMatch and
// re-run — `history rows: want 1` flips to `got 0` and `origin: want
// 'tm'` flips to NULL. Verified locally 2026-05-26.

func TestIntegrationTMPhase_EmitsHistoryAndUpdatesLineage_Exact(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})
	tmEntry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	// Find the translation that should now be TM-matched.
	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 translation, got %d", len(rows))
	}
	tr := rows[0]

	// 1) translations.origin (new column) updated to 'tm'.
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginTm {
		t.Errorf("translations.origin: want 'tm', got valid=%v value=%v", tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}

	// 2) translations.tm_match_percent updated to 100.
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent != 100 {
		t.Errorf("translations.tm_match_percent: want 100, got %v", tr.TmMatchPercent)
	}

	// 3) Exactly one translation_history row with kind='tm' for this translation.
	var historyCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM translation_history
		WHERE translation_id = $1 AND kind = 'tm'`, tr.ID).Scan(&historyCount); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyCount != 1 {
		t.Errorf("translation_history rows for kind=tm: want 1, got %d", historyCount)
	}

	// 4) History row carries the expected provenance + text.
	var (
		hText           *string
		hTmID           *int64
		hTmEntryID      *int64
		hTmMatchPercent *int16
	)
	if err := pool.QueryRow(ctx, `
		SELECT text, tm_id, tm_entry_id, tm_match_percent
		FROM translation_history
		WHERE translation_id = $1 AND kind = 'tm'
		ORDER BY created_at DESC LIMIT 1`, tr.ID).Scan(&hText, &hTmID, &hTmEntryID, &hTmMatchPercent); err != nil {
		t.Fatalf("scan history row: %v", err)
	}
	if hText == nil || *hText != "Bonjour le monde" {
		t.Errorf("history.text: want 'Bonjour le monde', got %v", hText)
	}
	if hTmID == nil || *hTmID != tmID {
		t.Errorf("history.tm_id: want %d, got %v", tmID, hTmID)
	}
	if hTmEntryID == nil || *hTmEntryID != tmEntry.ID {
		t.Errorf("history.tm_entry_id: want %d, got %v", tmEntry.ID, hTmEntryID)
	}
	if hTmMatchPercent == nil || *hTmMatchPercent != 100 {
		t.Errorf("history.tm_match_percent: want 100, got %v", hTmMatchPercent)
	}
}

func TestIntegrationTMPhase_EmitsHistoryAndUpdatesLineage_Fuzzy(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Source "Hello world there" vs TM "Hello world": Levenshtein 1 - 6/17 ≈ 0.65,
	// so the threshold sits below it for a guaranteed fuzzy (not exact) hit.
	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world there"}, 0.6)
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
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
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 translation, got %d", len(rows))
	}
	tr := rows[0]

	if !tr.Origin.Valid {
		t.Fatalf("fuzzy TM hit was not applied (origin NULL)")
	}

	// 1) origin = 'tm'
	if tr.Origin.TranslationOrigin != db.TranslationOriginTm {
		t.Errorf("translations.origin: want 'tm', got %v", tr.Origin.TranslationOrigin)
	}

	// 2) tm_match_percent is the fuzzy score, not an exact 100
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent < 60 || *tr.TmMatchPercent >= 100 {
		t.Errorf("translations.tm_match_percent: want in [60,100), got %v", tr.TmMatchPercent)
	}

	// 3) exactly one tm history row
	var historyCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM translation_history
		WHERE translation_id = $1 AND kind = 'tm'`, tr.ID).Scan(&historyCount); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyCount != 1 {
		t.Errorf("translation_history rows for kind=tm: want 1, got %d", historyCount)
	}
}

// Anti-tautology: re-deriving score from hit.Score in applyFuzzyMatch
// flips this RED (percent jumps to 100, the pg_trgm value for the
// punctuation-different pair).
func TestIntegrationTMPhase_FuzzyPercentMatchesScorerNotPgTrgm(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// "Hello world." vs "Hello world": pg_trgm = 1.0, Lev = 1 - 1/12 ≈ 0.917.
	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world."}, 0.6)
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
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
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTranslationsByFile: err=%v len=%d", err, len(rows))
	}
	tr := rows[0]

	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginTm {
		t.Fatalf("translations.origin: want 'tm', got valid=%v %v", tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}
	if tr.TmMatchPercent == nil {
		t.Fatal("translations.tm_match_percent: want non-nil")
	}
	if *tr.TmMatchPercent >= 100 {
		t.Errorf("translations.tm_match_percent: want < 100 (pg_trgm regression), got %d", *tr.TmMatchPercent)
	}
	if *tr.TmMatchPercent < 80 {
		t.Errorf("translations.tm_match_percent: want >= 80 for 1-char diff on 12-char string, got %d", *tr.TmMatchPercent)
	}

	// translations and history must agree — single source of truth.
	var historyPercent *int16
	if err := pool.QueryRow(ctx, `
		SELECT tm_match_percent FROM translation_history
		WHERE translation_id = $1 AND kind = 'tm'
		ORDER BY created_at DESC LIMIT 1`, tr.ID).Scan(&historyPercent); err != nil {
		t.Fatalf("scan history percent: %v", err)
	}
	if historyPercent == nil || *historyPercent != *tr.TmMatchPercent {
		t.Errorf("history.tm_match_percent (%v) != translations.tm_match_percent (%d)",
			historyPercent, *tr.TmMatchPercent)
	}
}
