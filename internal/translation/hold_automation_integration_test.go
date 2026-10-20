package translation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The TM and MT fills and the exact-match confirm refuse a row another user
// holds in the statement itself. Apply TM/MT also leave held rows out of the
// rows they read; these pin a hold taken after that read.
func TestIntegrationHold_AutomationWritesRefuseAHeldRow(t *testing.T) {
	f := newHoldFixture(t)
	ctx := context.Background()
	seg := testutil.SeedSegment(t, f.q, f.fileID, "Close", 4)
	empty := testutil.SeedTranslation(t, f.q, seg.ID, "de", db.TranslationStatusNeedsReview)
	f.hold(t, empty, f.holder)

	text := "Schließen"
	for _, c := range []struct {
		name  string
		actor *int64
	}{{"another user's run", &f.other.ID}, {"a run with no user", nil}} {
		name, actor := c.name, c.actor
		if _, err := f.q.UpdateTranslationTMFuzzy(ctx, db.UpdateTranslationTMFuzzyParams{ID: empty.ID, ActiveText: &text, ActorID: actor}); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("TM fill, %s: err = %v, want pgx.ErrNoRows", name, err)
		}
		if _, err := f.q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{ID: empty.ID, ActiveText: &text, ActorID: actor}); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("MT fill, %s: err = %v, want pgx.ErrNoRows", name, err)
		}
		if _, err := f.q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: empty.ID, HumanText: &text, ActorID: actor}); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("TM confirm, %s: err = %v, want pgx.ErrNoRows", name, err)
		}
	}
	f.assertUnchanged(t, empty)

	// The holder's own run fills and confirms, and the confirm stays a
	// pipeline action: no human editor recorded.
	if _, err := f.q.UpdateTranslationTMFuzzy(ctx, db.UpdateTranslationTMFuzzyParams{ID: empty.ID, ActiveText: &text, ActorID: &f.holder.ID}); err != nil {
		t.Fatalf("TM fill, the holder's run: %v", err)
	}
	got, err := f.q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: empty.ID, HumanText: &text, ActorID: &f.holder.ID})
	if err != nil {
		t.Fatalf("TM confirm, the holder's run: %v", err)
	}
	if got.Status != db.TranslationStatusApproved || got.EditedBy != nil {
		t.Errorf("confirmed row: status %s, edited_by %v; want approved, nil", got.Status, got.EditedBy)
	}
}

// The re-import write refuses a row another user holds and reports zero rows.
func TestIntegrationHold_ReimportWriteRefusesAHeldRow(t *testing.T) {
	f := newHoldFixture(t)
	f.hold(t, f.held, f.holder)
	text := "Importiert"
	n, err := f.q.UpdateTranslationFromReimport(context.Background(), db.UpdateTranslationFromReimportParams{
		ID: f.held.ID, HumanText: &text, EditedBy: &f.other.ID,
	})
	if err != nil || n != 0 {
		t.Errorf("re-import write on a held row: rows %d, err %v; want 0, nil", n, err)
	}
	f.assertUnchanged(t, f.held)

	if n, err := f.q.UpdateTranslationFromReimport(context.Background(), db.UpdateTranslationFromReimportParams{
		ID: f.held.ID, HumanText: &text, EditedBy: &f.holder.ID,
	}); err != nil || n != 1 {
		t.Errorf("re-import write by the holder: rows %d, err %v; want 1, nil", n, err)
	}
}
