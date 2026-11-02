package qa_test

import (
	"context"
	"fmt"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A job-wide run checks every row once even when a page of 500 ends inside
// the pieces of a split segment whose ids do not follow their order: a split
// of a piece makes rows newer than the piece after them.
func TestIntegrationQAJobRun_PagesThroughSplitPieces(t *testing.T) {
	f := newQAScale(t, "number_consistency")
	ctx := context.Background()
	// 498 rows at positions 1–498, each with a number the target drops.
	testutil.SeedSegmentsBulk(t, f.pool, f.fileID, "de", 0, 498, "Ein Satz ohne Zahl.")
	// Three pieces at position 499, made in the order sub 2, sub 0, sub 1, so
	// the 500th row of the run is sub 1 and sub 2 has the smallest id.
	for _, sub := range []int32{2, 0, 1} {
		seg := testutil.SeedLanguageSegment(t, f.q, f.fileID, "de", fmt.Sprintf("Piece %d", sub+7), 499, sub, "line:499")
		tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", db.TranslationStatusNeedsReview)
		target := "Ein Stück."
		if _, err := f.q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: tr.ID, HumanText: &target}); err != nil {
			t.Fatal(err)
		}
	}

	f.runJob(t)
	if got := len(f.findings(t)); got != 501 {
		t.Errorf("findings = %d, want 501: every row checked once", got)
	}
}
