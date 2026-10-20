package pipeline_test

import (
	"context"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// Pins that a confirm write-back to the TM leaves the segment's origin and
// tm_match_percent (the outcome badge) unchanged: writing to the TM is not
// leveraging from it. Outcome lineage only changes on an
// automation POPULATE event (Apply TM, pre-translate, MT, TM-panel insert).
//
// Anti-tautology: re-add the EmitAutomationHistory rescore block at the end of
// SaveToTM and this test goes RED (origin flips to 'tm', a 'tm' history row
// appears). Verified locally 2026-05-27.
func TestIntegrationTMWriteBack_OriginStaysSticky(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})

	user := testutil.SeedUser(t, q, tenantID, "writeback-sticky@test.com", "pass123")
	uID := user.ID

	// Fill the segment via MT first (origin='mt', tm_match_percent=NULL).
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	mtCtx := pipeline.WithTriggeredBy(ctx, &uID)
	if _, err := svc.TranslateFile(mtCtx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("MT pre-fill: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTranslationsByFile: err=%v len=%d", err, len(rows))
	}
	tr := rows[0]
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginMt {
		t.Fatalf("pre-fixture: want origin='mt' after MT phase, got valid=%v value=%v", tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}

	// Human confirms with an edit.
	confirmed := "Bonjour le monde"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        tr.ID,
		HumanText: &confirmed,
		EditedBy:  &uID,
	}); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	// Write-back to TM (what the confirm handler does).
	wbCtx := auth.WithClaims(ctx, &auth.Claims{UserID: uID, TenantID: tenantID})
	savedTMs, err := svc.SaveToTM(wbCtx, tenantID, tr.ID)
	if err != nil {
		t.Fatalf("SaveToTM: %v", err)
	}
	// Write-back still happens — the source/target pair lands in the TM.
	if len(savedTMs) == 0 || savedTMs[0] != tmID {
		t.Fatalf("expected write-back to TM %d; got %v", tmID, savedTMs)
	}

	// STICKY invariant: origin + tm_match_percent UNCHANGED by the write-back.
	tr, err = q.GetTranslation(ctx, tr.ID)
	if err != nil {
		t.Fatalf("GetTranslation: %v", err)
	}
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginMt {
		t.Errorf("post-write-back: origin must STAY 'mt' (sticky); got valid=%v value=%v",
			tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}
	if tr.TmMatchPercent != nil {
		t.Errorf("post-write-back: tm_match_percent must STAY NULL (sticky); got %v", *tr.TmMatchPercent)
	}

	// And the write-back emits NO `tm` history row on the segment — the TM's
	// own write is audited in translation_memory_history; the segment's
	// translation_history carries the `human` confirm row, not a synthetic `tm`.
	var tmHistCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM translation_history WHERE translation_id = $1 AND kind = 'tm'`,
		tr.ID).Scan(&tmHistCount); err != nil {
		t.Fatalf("count tm history: %v", err)
	}
	if tmHistCount != 0 {
		t.Errorf("write-back must not emit a 'tm' history row on the segment; got %d", tmHistCount)
	}
}
