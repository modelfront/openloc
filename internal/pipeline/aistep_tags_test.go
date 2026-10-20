package pipeline

import (
	"context"
	"strings"
	"testing"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/inlinetag"
	"openloc/pkg/extsdk"
)

// Inline tags on the QE+APE path, end to end through RunAIStep against a fake
// extension: the source and target go out as chip labels, and the postedit
// comes back through decode into BOTH active_text and active_rich_text.
//
// The rich column is the point. The editor (SegmentRow) and the export both
// prefer active_rich_text, so a postedit written only to active_text never
// reaches the reviewer or the delivered file.
func TestRunAIStep_TaggedSegmentRoundTrip(t *testing.T) {
	approved := true
	// The model splits the pair around the second tag — legal, and exactly
	// what a positional projection could never produce.
	postedit := `<1>Um zu aktualisieren, </1><2>Aktualisieren</2><1> anklicken.</1>`
	pred := &fakeExtPredictor{ape: true, verdict: func(extsdk.PredictRow) extsdk.PredictResult {
		return extsdk.PredictResult{Postedit: &postedit, PosteditApproved: &approved}
	}}

	srcRich := `<g id="1" ctype="italic">To update, click </g><g id="2" ctype="bold">Refresh</g>`
	mtPlain := "Zum Aktualisieren Aktualisieren klicken"
	mtRich := `<g id="1" ctype="italic">Zum Aktualisieren </g><g id="2" ctype="bold">Aktualisieren</g>`

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{{
		ID: 100, SegmentID: 1, TargetLanguage: "de",
		SourceText: "To update, click Refresh", SourceRichText: &srcRich,
		ActiveText: &mtPlain, ActiveRichText: &mtRich,
	}}

	svc, _ := newExtStepService(mq, pred)
	if _, err := svc.RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}

	// 1. The extension saw the tags, as chips rather than raw XLIFF.
	if len(pred.calls) != 1 || len(pred.calls[0]) != 1 {
		t.Fatalf("expected 1 predict row, got %v", pred.calls)
	}
	sent := pred.calls[0][0]
	if sent.Source != `<1>To update, click </1><2>Refresh</2>` {
		t.Errorf("source sent to the extension = %q, want chip-encoded source", sent.Source)
	}
	if sent.Target != `<1>Zum Aktualisieren </1><2>Aktualisieren</2>` {
		t.Errorf("target sent to the extension = %q, want chip-encoded target", sent.Target)
	}

	// 2. The postedit decoded back into both columns.
	if len(mq.updatePredictCalls) != 1 {
		t.Fatalf("expected 1 predict update, got %d", len(mq.updatePredictCalls))
	}
	got := mq.updatePredictCalls[0]
	wantRich := `<g id="1" ctype="italic">Um zu aktualisieren, </g>` +
		`<g id="2" ctype="bold">Aktualisieren</g><g id="1" ctype="italic"> anklicken.</g>`
	if got.ActiveRichText == nil || *got.ActiveRichText != wantRich {
		t.Errorf("active_rich_text:\n got %v\nwant %q", got.ActiveRichText, wantRich)
	}
	wantPlain := "Um zu aktualisieren, Aktualisieren anklicken."
	if got.ActiveText == nil || *got.ActiveText != wantPlain {
		t.Errorf("active_text:\n got %v\nwant %q", got.ActiveText, wantPlain)
	}

	// 3. The history row carries the rich form too, or restoring this version
	//    later drops the tags.
	if len(mq.insertHistoryCalls) != 1 {
		t.Fatalf("expected 1 ape history row, got %d", len(mq.insertHistoryCalls))
	}
	if h := mq.insertHistoryCalls[0]; h.RichText == nil || *h.RichText != wantRich {
		t.Errorf("ape history rich_text: got %v, want %q", h.RichText, wantRich)
	}
}

// A chip the model invented cannot be spotted downstream — a stray `{9}` is
// indistinguishable from a real placeholder to the tag QA rule — so the segment
// gets a comment naming it.
func TestRunAIStep_UnknownTagBecomesComment(t *testing.T) {
	approved := true
	postedit := `<1>Fett</1> und {9}`
	pred := &fakeExtPredictor{ape: true, verdict: func(extsdk.PredictRow) extsdk.PredictResult {
		return extsdk.PredictResult{Postedit: &postedit, PosteditApproved: &approved}
	}}

	srcRich := `<g id="1" ctype="bold">Bold</g> and more`
	mtPlain := "Fett und mehr"

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{{
		ID: 100, SegmentID: 1, TargetLanguage: "de",
		SourceText: "Bold and more", SourceRichText: &srcRich, ActiveText: &mtPlain,
	}}

	svc, _ := newExtStepService(mq, pred)
	if _, err := svc.RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}

	if len(mq.createAICommentCalls) != 1 {
		t.Fatalf("expected 1 AI comment naming the bad tag, got %d", len(mq.createAICommentCalls))
	}
	if c := mq.createAICommentCalls[0].Content; !strings.Contains(c, "{9}") {
		t.Errorf("comment must name the unresolved chip, got %q", c)
	}
}

// Each row's postedit decodes against its own tag table. An extension may
// answer in any order; matching by position would decode one segment's chips
// with another's markup.
func TestRunAIStep_ReorderedResponseDecodesByID(t *testing.T) {
	srcA := `<g id="1" ctype="bold">Save</g> now`
	srcB := `<x id="1"/> Open`
	mtA, mtB := "Jetzt speichern", "Öffnen"
	approved := true
	pred := &fakeExtPredictor{ape: true, respond: func(rows []extsdk.PredictRow) extsdk.PredictResponse {
		peA, peB := `<1>Speichern</1> jetzt`, `{1} Öffnen`
		// Answer in reverse order.
		return extsdk.PredictResponse{Rows: []extsdk.PredictResult{
			{ID: rows[1].ID, Postedit: &peB, PosteditApproved: &approved},
			{ID: rows[0].ID, Postedit: &peA, PosteditApproved: &approved},
		}}
	}}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Save now", SourceRichText: &srcA, ActiveText: &mtA},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "Open", SourceRichText: &srcB, ActiveText: &mtB},
	}

	svc, _ := newExtStepService(mq, pred)
	if _, err := svc.RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	got := map[int64]string{}
	for _, c := range mq.updatePredictCalls {
		if c.ActiveRichText != nil {
			got[c.ID] = *c.ActiveRichText
		}
	}
	if want := `<g id="1" ctype="bold">Speichern</g> jetzt`; got[100] != want {
		t.Errorf("row 100 rich text = %q, want %q", got[100], want)
	}
	if want := `<x id="1"/> Öffnen`; got[101] != want {
		t.Errorf("row 101 rich text = %q, want %q", got[101], want)
	}
}

// A tag-placement fix moves only the markup: the words are identical before and
// after, so the tag-stripped forms match. Comparing those alone discards exactly
// the correction this feature exists to make.
func TestMapLLMPredictResult_AppliesTagOnlyFix(t *testing.T) {
	srcRich := `<g id="1" ctype="italic">To update, click </g><g id="2" ctype="bold">Refresh</g>`
	// What MT produced: one pair widened to swallow the other.
	currentRich := `<g id="1" ctype="italic">Um zu aktualisieren, <g id="2" ctype="bold">Aktualisieren</g> anklicken.</g>`
	currentPlain := stripInlineTags(currentRich)

	tbl := inlinetag.NewTable()
	inlinetag.EncodeWith(tbl, srcRich)
	inlinetag.EncodeWith(tbl, currentRich)

	approved := true
	score := 95.0
	// What APE returns: the same words, the pair split around the bold run.
	postedit := `<1>Um zu aktualisieren, </1><2>Aktualisieren</2><1> anklicken.</1>`

	got := mapLLMPredictResult(ai.PredictResult{
		ID: "1", Approved: false, Postedit: &postedit,
		PosteditApproved: &approved, PosteditScore: &score,
	}, ai.ModeQEAPE, currentPlain, currentRich, tbl)

	if got.Postedit == nil {
		t.Fatal("a tag-only correction was discarded — the plain text is unchanged by design, only the markup moved")
	}
	if got.PosteditRichText == nil {
		t.Fatal("no rich postedit, so the corrected tags would never reach the segment")
	}
	want := `<g id="1" ctype="italic">Um zu aktualisieren, </g>` +
		`<g id="2" ctype="bold">Aktualisieren</g><g id="1" ctype="italic"> anklicken.</g>`
	if *got.PosteditRichText != want {
		t.Errorf("rich postedit:\n got %q\nwant %q", *got.PosteditRichText, want)
	}
	// The words did not change, so the plain column stays as it was.
	if *got.Postedit != currentPlain {
		t.Errorf("plain postedit = %q, want the unchanged words %q", *got.Postedit, currentPlain)
	}
}

// The mirror: a postedit identical to what is already stored is not a change,
// and must not be applied or scored as one.
func TestMapLLMPredictResult_IgnoresIdenticalTaggedPostedit(t *testing.T) {
	srcRich := `<g id="1" ctype="bold">Click</g>`
	currentRich := `<g id="1" ctype="bold">Klicken</g>`

	tbl := inlinetag.NewTable()
	inlinetag.EncodeWith(tbl, srcRich)
	inlinetag.EncodeWith(tbl, currentRich)

	approved := true
	postedit := `<1>Klicken</1>`

	got := mapLLMPredictResult(ai.PredictResult{
		ID: "1", Approved: false, Postedit: &postedit, PosteditApproved: &approved,
	}, ai.ModeQEAPE, stripInlineTags(currentRich), currentRich, tbl)

	if got.Postedit != nil {
		t.Errorf("an unchanged postedit was applied: %q", *got.Postedit)
	}
}
