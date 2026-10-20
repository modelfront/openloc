package pipeline_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// holdAfterFill has another user open the segment right after the TM fill
// lands, before the exact-match confirm: a race no run can rule out.
type holdAfterFill struct {
	db.Querier
	hold func(id int64)
}

func (q holdAfterFill) UpdateTranslationTMFuzzy(ctx context.Context, arg db.UpdateTranslationTMFuzzyParams) (db.Translation, error) {
	t, err := q.Querier.UpdateTranslationTMFuzzy(ctx, arg)
	if err == nil {
		q.hold(arg.ID)
	}
	return t, err
}

// A fill whose confirm another user's hold refuses keeps its TM provenance:
// the row stays unconfirmed with origin, match percent and a tm history row,
// not TM text that looks typed by nobody.
func TestIntegrationTranslateFile_HoldBetweenTMFillAndConfirmKeepsProvenance(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Hello world", TargetText: "Bonjour le monde", CreatedBy: "test",
	}); err != nil {
		t.Fatal(err)
	}
	job := testutil.SeedJob(t, q, fileID, "fr")
	other := testutil.SeedUserWithRole(t, q, tenantID, "ben@hold-race.test", "pass", "project_manager")
	wrapped := holdAfterFill{Querier: q, hold: func(id int64) {
		if _, err := q.AcquireHold(ctx, db.AcquireHoldParams{
			TranslationID: id, TenantID: tenantID, JobID: job.ID, UserID: other.ID, TabID: "tab-1", TtlSeconds: 90,
		}); err != nil {
			t.Errorf("acquire hold: %v", err)
		}
	}}

	svc := pipeline.NewService(wrapped, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d, err %v", len(rows), err)
	}
	got := rows[0]
	if got.ActiveText == nil || *got.ActiveText != "Bonjour le monde" || got.Status != db.TranslationStatusNeedsReview {
		t.Fatalf("row = %v / %s, want the TM fill left unconfirmed", got.ActiveText, got.Status)
	}
	if !got.Origin.Valid || got.Origin.TranslationOrigin != db.TranslationOriginTm || got.TmMatchPercent == nil || *got.TmMatchPercent != 100 {
		t.Errorf("provenance = origin %v, percent %v; want tm, 100", got.Origin, got.TmMatchPercent)
	}
	if n := testutil.CountRows(t, pool, "SELECT count(*) FROM translation_history WHERE translation_id = $1 AND kind = 'tm'", got.ID); n != 1 {
		t.Errorf("tm history rows = %d, want 1", n)
	}
}
