package translation_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// step_edited marks a person changing the text during the current step:
// human writes that change the text set it, machine writes and unchanged
// confirms do not, and a clear resets it.
func TestIntegrationStepEdited_WritePaths(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	userID, tenantID := testutil.RegisterUser(t, q, "stepedited")
	proj := testutil.SeedProject(t, q, tenantID, "StepEdited", "en", []string{"de"})
	file := testutil.SeedFile(t, q, proj.ID, "f.txt", db.FileFormatTxt)
	newTr := func(src string, pos int32) db.Translation {
		seg := testutil.SeedSegment(t, q, file.ID, src, pos)
		return testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	}
	get := func(id int64) db.Translation {
		t.Helper()
		tr, err := q.GetTranslation(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	str := ptr.To[string]

	// Machine writes: MT, TM, QE/APE.
	mt := newTr("machine", 0)
	if _, err := q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{ID: mt.ID, ActiveText: str("maschine")}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateTranslationTMFuzzy(ctx, db.UpdateTranslationTMFuzzyParams{ID: mt.ID, ActiveText: str("Maschine")}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateTranslationPredict(ctx, db.UpdateTranslationPredictParams{
		ID: mt.ID, ActiveText: str("Die Maschine"), Status: db.TranslationStatusAutoApproved,
		PredictOutcome: db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeApeApproved, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if get(mt.ID).StepEdited {
		t.Error("MT, TM and APE writes are not a person's edit")
	}

	// A confirm that keeps the text is not an edit; a draft that changes it is.
	h := newTr("human", 1)
	if _, err := q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{ID: h.ID, ActiveText: str("mensch")}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: h.ID, HumanText: str("mensch"), EditedBy: &userID}); err != nil {
		t.Fatal(err)
	}
	if get(h.ID).StepEdited {
		t.Error("confirming unchanged text is not an edit")
	}
	if _, err := q.SaveTranslationDraft(ctx, db.SaveTranslationDraftParams{ID: h.ID, HumanText: str("Mensch"), EditedBy: &userID}); err != nil {
		t.Fatal(err)
	}
	if !get(h.ID).StepEdited {
		t.Error("a draft that changes the text is an edit")
	}

	// Propagation writes a person's text onto the repetitions.
	a := newTr("same", 2)
	b := newTr("same", 3)
	if _, err := q.PropagateTranslation(ctx, db.PropagateTranslationParams{
		HumanText: str("gleich"), FileID: file.ID, SourceText: "same", TargetLanguage: "de", ExcludeID: a.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if !get(b.ID).StepEdited {
		t.Error("propagated text is a person's edit")
	}

	// A clear starts the segment over.
	if _, err := q.ClearTranslation(ctx, db.ClearTranslationParams{ID: h.ID}); err != nil {
		t.Fatal(err)
	}
	if get(h.ID).StepEdited {
		t.Error("a cleared segment has no edit in this step")
	}
}
