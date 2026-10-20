package translation_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/qa"
	"openloc/internal/segmentbulk"
	"openloc/internal/testutil"
	"openloc/internal/translation"
)

// editingQuerier stands in for a colleague who saves a segment at the worst
// moment: right after a bulk action read it and right before it writes. It
// bumps the row's updated_at from inside the read the write path makes just
// before the write.
type editingQuerier struct {
	db.Querier
	pool   *pgxpool.Pool
	target int64
	t      *testing.T
}

func (q *editingQuerier) touch(ctx context.Context, id int64) {
	if id != q.target {
		return
	}
	if _, err := q.pool.Exec(ctx, "UPDATE translations SET updated_at = now() + interval '1 second' WHERE id = $1", id); err != nil {
		q.t.Fatal(err)
	}
}

// Bulk confirm reads the history baselines right before its write.
func (q *editingQuerier) ListLatestHistoryTexts(ctx context.Context, ids []int64) ([]db.ListLatestHistoryTextsRow, error) {
	for _, id := range ids {
		q.touch(ctx, id)
	}
	return q.Querier.ListLatestHistoryTexts(ctx, ids)
}

// BatchDraft (find & replace) re-reads each row right before its write.
func (q *editingQuerier) GetTranslationInFileTenant(ctx context.Context, arg db.GetTranslationInFileTenantParams) (db.Translation, error) {
	q.touch(ctx, arg.ID)
	return q.Querier.GetTranslationInFileTenant(ctx, arg)
}

// A row edited between bulk confirm's read and its write is skipped as a
// conflict and keeps its draft; the others are confirmed.
func TestIntegrationConfirmSelection_ConcurrentEditIsAConflict(t *testing.T) {
	f := newConfirmFixture(t)
	seg := testutil.SeedSegment(t, f.q, f.job.FileID, "Good evening", 20)
	other := testutil.SeedTranslation(t, f.q, seg.ID, "es", db.TranslationStatusNeedsReview).ID
	f.setText(t, other, "Buenas tardes")
	q := &editingQuerier{Querier: f.q, pool: f.pool, target: f.draft, t: t}
	svc := translation.NewService(q, f.pool, nil, qa.NewService(f.q))

	res, err := svc.ConfirmSelection(context.Background(), f.job, f.tenantID, f.userID, []int64{f.draft, other})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 || res.Applied[0] != other {
		t.Errorf("applied = %v, want [%d]", res.Applied, other)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != (segmentbulk.Skip{ID: f.draft, Reason: segmentbulk.Conflict}) {
		t.Errorf("skipped = %v, want %d as conflict", res.Skipped, f.draft)
	}
	if st, _ := f.status(t, f.draft); st != db.TranslationStatusNeedsReview {
		t.Errorf("edited row status = %s, want needs_review (not overwritten)", st)
	}
}

// A row edited between Replace All's read and its write is counted as a
// conflict and keeps its text.
func TestIntegrationFindReplace_ConcurrentEditIsAConflict(t *testing.T) {
	f := newFRFixture(t)
	edited := f.row(t, f.fileID, "Hello one", nil, db.TranslationStatusNeedsReview)
	plain := f.row(t, f.fileID, "Hello two", nil, db.TranslationStatusNeedsReview)
	q := &editingQuerier{Querier: f.q, pool: f.pool, target: edited, t: t}
	svc := translation.NewService(q, f.pool, nil, nil)

	ctx := auth.WithClaims(context.Background(), &auth.Claims{TenantID: f.tenantID, UserID: f.userID, Role: auth.RoleOwner})
	res, err := svc.FindReplace(ctx, f.tenantID, f.userID, f.job, translation.FindReplaceParams{
		Find: "Hello", Replace: "Hola", CaseSensitive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Segments != 1 || res.Conflicts != 1 {
		t.Errorf("result = %+v, want 1 segment written and 1 conflict", res)
	}
	if got := f.read(t, edited); *got.text != "Hello one" {
		t.Errorf("edited row overwritten: %q", *got.text)
	}
	if got := f.read(t, plain); *got.text != "Hola two" {
		t.Errorf("other row = %q, want Hola two", *got.text)
	}
}
