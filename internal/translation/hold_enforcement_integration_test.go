package translation_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/hold"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// While a user holds a segment, every single-segment write by anyone else is
// refused with 409 segment_held naming the holder, and the row stays as it
// was. "Use mine" re-sends the text with no expected version, so it is refused
// too.
func TestIntegrationHold_OtherUsersSingleWritesRefused(t *testing.T) {
	f := newHoldFixture(t)
	f.hold(t, f.held, f.holder)
	before := f.get(t, f.held)

	writes := []struct {
		name, method, suffix string
		body                 map[string]any
	}{
		{"confirm", http.MethodPut, "", map[string]any{"human_text": "Sichern", "expected_updated_at": before.UpdatedAt.Time}},
		{"confirm, use mine", http.MethodPut, "", map[string]any{"human_text": "Sichern"}},
		{"draft", http.MethodPut, "/draft", map[string]any{"human_text": "Sichern", "expected_updated_at": before.UpdatedAt.Time}},
		{"draft, use mine", http.MethodPut, "/draft", map[string]any{"human_text": "Sichern"}},
		{"clear", http.MethodPost, "/clear", nil},
	}
	for _, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			wantHeld(t, f.do(t, f.other, w.method, f.trPath(f.held, w.suffix), w.body), f.holder)
			f.assertUnchanged(t, before)
		})
	}
}

// The holder's own writes pass their hold: draft, confirm and Use mine.
func TestIntegrationHold_HolderWritesPass(t *testing.T) {
	f := newHoldFixture(t)
	f.hold(t, f.held, f.holder)

	cur := f.get(t, f.held)
	rec := f.do(t, f.holder, http.MethodPut, f.trPath(f.held, "/draft"),
		map[string]any{"human_text": "Sichern", "expected_updated_at": cur.UpdatedAt.Time})
	testutil.AssertStatus(t, rec, http.StatusOK)

	cur = f.get(t, f.held)
	rec = f.do(t, f.holder, http.MethodPut, f.trPath(f.held, ""),
		map[string]any{"human_text": "Sichern", "expected_updated_at": cur.UpdatedAt.Time})
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = f.do(t, f.holder, http.MethodPut, f.trPath(f.held, "/draft"), map[string]any{"human_text": "Speichern!"})
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = f.do(t, f.holder, http.MethodPut, f.trPath(f.held, ""), map[string]any{"human_text": "Speichern!"})
	testutil.AssertStatus(t, rec, http.StatusOK)

	if got := f.get(t, f.held); ptr.Deref(got.ActiveText) != "Speichern!" || got.Status != db.TranslationStatusApproved {
		t.Fatalf("holder's confirm = %q %s, want Speichern! approved", ptr.Deref(got.ActiveText), got.Status)
	}
}

// An expired hold blocks nothing.
func TestIntegrationHold_ExpiredHoldBlocksNothing(t *testing.T) {
	f := newHoldFixture(t)
	f.hold(t, f.held, f.holder)
	f.expire(t, f.held)

	testutil.AssertStatus(t, f.do(t, f.other, http.MethodPut, f.trPath(f.held, "/draft"), map[string]any{"human_text": "Sichern"}), http.StatusOK)
	testutil.AssertStatus(t, f.do(t, f.other, http.MethodPut, f.trPath(f.held, ""), map[string]any{"human_text": "Sichern"}), http.StatusOK)
	testutil.AssertStatus(t, f.do(t, f.other, http.MethodPost, f.trPath(f.held, "/clear"), nil), http.StatusOK)
}

// A write with no actor (automation such as the TM exact-match auto-confirm)
// is blocked by any live hold; the same statement as the holder passes.
func TestIntegrationHold_WriteWithoutActorBlockedByAnyHold(t *testing.T) {
	f := newHoldFixture(t)
	ctx := context.Background()
	f.hold(t, f.held, f.holder)
	before := f.get(t, f.held)
	text := "Sichern"

	_, err := f.q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: f.held.ID, HumanText: &text})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("confirm with no actor: err = %v, want no rows", err)
	}
	_, err = f.q.SaveTranslationDraft(ctx, db.SaveTranslationDraftParams{ID: f.held.ID, HumanText: &text})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("draft with no actor: err = %v, want no rows", err)
	}
	_, err = f.q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: f.held.ID, HumanText: &text, EditedBy: &f.other.ID})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("confirm as another user: err = %v, want no rows", err)
	}
	_, err = f.q.ClearTranslation(ctx, db.ClearTranslationParams{ID: f.held.ID})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("clear with no actor: err = %v, want no rows", err)
	}
	sibs, err := f.q.PropagateTranslation(ctx, db.PropagateTranslationParams{
		HumanText: &text, FileID: f.fileID, SourceText: "Save", TargetLanguage: "de", ExcludeID: f.twin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, sib := range sibs {
		if sib.ID == f.held.ID {
			t.Fatal("propagation with no actor wrote the held sibling")
		}
	}
	f.assertUnchanged(t, before)

	if _, err := f.q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: f.held.ID, HumanText: &text, EditedBy: &f.holder.ID}); err != nil {
		t.Fatalf("confirm as the holder: %v", err)
	}
	// Another row, held by nobody, takes a write with no actor.
	if _, err := f.q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: f.free.ID, HumanText: &text}); err != nil {
		t.Fatalf("confirm of a free row with no actor: %v", err)
	}
}

// A non-admin lock refuses a segment another user holds; an admin's lock and
// the holder's own lock go through.
func TestIntegrationHold_LockByNonAdminRefused(t *testing.T) {
	f := newHoldFixture(t)
	f.hold(t, f.held, f.holder)
	f.hold(t, f.free, f.holder)
	before := f.get(t, f.held)

	wantHeld(t, f.do(t, f.other, http.MethodPost, f.trPath(f.held, "/lock"), nil), f.holder)
	f.assertUnchanged(t, before)

	testutil.AssertStatus(t, f.do(t, f.admin, http.MethodPost, f.trPath(f.held, "/lock"), nil), http.StatusOK)
	if !f.get(t, f.held).Locked {
		t.Error("admin's lock did not land")
	}
	testutil.AssertStatus(t, f.do(t, f.holder, http.MethodPost, f.trPath(f.free, "/lock"), nil), http.StatusOK)
	if !f.get(t, f.free).Locked {
		t.Error("holder's own lock did not land")
	}
}

// The holder lookup is tenant-scoped: another org never sees the hold.
func TestIntegrationHold_TenantScoped(t *testing.T) {
	f := newHoldFixture(t)
	ctx := context.Background()
	f.hold(t, f.held, f.holder)
	otherTenant := testutil.SeedTenant(t, f.q, "HoldElsewhere").ID

	if _, err := f.q.GetLiveHolder(ctx, db.GetLiveHolderParams{TranslationID: f.held.ID, TenantID: otherTenant}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("holder seen from another org: err = %v", err)
	}
	if h, err := hold.HeldByOther(ctx, f.q, otherTenant, f.held.ID, f.other.ID); err != nil || h != nil {
		t.Fatalf("HeldByOther from another org = %v, %v; want nil", h, err)
	}
	if h, err := hold.HeldByOther(ctx, f.q, f.tenant, f.held.ID, f.other.ID); err != nil || h == nil || h.HolderID != f.holder.ID {
		t.Fatalf("HeldByOther in the org = %v, %v; want the holder", h, err)
	}
}
