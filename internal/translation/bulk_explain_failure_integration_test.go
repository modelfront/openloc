package translation_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/qa"
	"openloc/internal/segmentbulk"
	"openloc/internal/testutil"
	"openloc/internal/translation"
)

// Once a bulk confirm has committed, a failure to explain why one row was not
// written does not turn the whole confirm into an error: the written rows are
// reported as applied and the unexplained one as a conflict.
func TestIntegrationConfirmSelection_ExplainFailureKeepsTheCommittedRows(t *testing.T) {
	f := newConfirmFixture(t)
	seg := testutil.SeedSegment(t, f.q, f.job.FileID, "Good evening", 20)
	other := testutil.SeedTranslation(t, f.q, seg.ID, "es", db.TranslationStatusNeedsReview).ID
	f.setText(t, other, "Buenas tardes")
	failing := db.New(testutil.FailingPool(t, "GetLiveHolder"))
	q := &editingQuerier{Querier: failing, pool: f.pool, target: f.draft, t: t}
	svc := translation.NewService(q, f.pool, nil, qa.NewService(f.q))

	res, err := svc.ConfirmSelection(context.Background(), f.job, f.tenantID, f.userID, []int64{f.draft, other})
	if err != nil {
		t.Fatalf("ConfirmSelection: %v (the other row was already committed)", err)
	}
	if len(res.Applied) != 1 || res.Applied[0] != other {
		t.Errorf("applied = %v, want [%d]", res.Applied, other)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != (segmentbulk.Skip{ID: f.draft, Reason: segmentbulk.Conflict}) {
		t.Errorf("skipped = %v, want %d as conflict", res.Skipped, f.draft)
	}
}
