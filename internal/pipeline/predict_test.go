package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/extension"
	"openloc/internal/httputil"
	"openloc/internal/ptr"
)

func TestRoutePredictOutcome_Approved(t *testing.T) {
	mtText := "Hello translated"
	row := db.ListPreTranslatedByFileRow{
		ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText,
	}
	result := predictVerdict{
		ID: "100", Approved: true,
	}

	params := routePredictOutcome(row, aiPredictResult{predictVerdict: result})

	if params.Status != db.TranslationStatusAutoApproved {
		t.Errorf("expected auto_approved, got %s", params.Status)
	}
	if params.PredictOutcome.PredictOutcome != db.PredictOutcomeApproved {
		t.Errorf("expected approved outcome, got %s", params.PredictOutcome.PredictOutcome)
	}
	if params.ActiveText == nil || *params.ActiveText != mtText {
		t.Errorf("expected active_text to be MT text, got %v", params.ActiveText)
	}
	// "No APE" is expressed as predict_outcome='approved' (asserted above) —
	// active_text stays MT.
}

func TestRoutePredictOutcome_APEApproved(t *testing.T) {
	mtText := "Hello translated"
	row := db.ListPreTranslatedByFileRow{
		ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText,
	}
	apeText := "Hello better translation"
	apeApproved := true
	result := predictVerdict{
		ID: "100", Approved: false, Postedit: &apeText, PosteditApproved: &apeApproved,
	}

	params := routePredictOutcome(row, aiPredictResult{predictVerdict: result})

	if params.Status != db.TranslationStatusAutoApproved {
		t.Errorf("expected auto_approved, got %s", params.Status)
	}
	if params.PredictOutcome.PredictOutcome != db.PredictOutcomeApeApproved {
		t.Errorf("expected ape_approved outcome, got %s", params.PredictOutcome.PredictOutcome)
	}
	if params.ActiveText == nil || *params.ActiveText != apeText {
		t.Errorf("expected active_text to be APE text, got %v", params.ActiveText)
	}
	// The APE rewrite lives in active_text (asserted above) +
	// translation_history kind='ape'.
}

func TestRoutePredictOutcome_APENeedsReview(t *testing.T) {
	mtText := "Hello translated"
	row := db.ListPreTranslatedByFileRow{
		ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText,
	}
	apeText := "Hello suggested"
	apeApproved := false
	result := predictVerdict{
		ID: "100", Approved: false, Postedit: &apeText, PosteditApproved: &apeApproved,
	}

	params := routePredictOutcome(row, aiPredictResult{predictVerdict: result})

	if params.Status != db.TranslationStatusNeedsReview {
		t.Errorf("expected needs_review, got %s", params.Status)
	}
	if params.PredictOutcome.PredictOutcome != db.PredictOutcomeApeNeedsReview {
		t.Errorf("expected ape_needs_review outcome, got %s", params.PredictOutcome.PredictOutcome)
	}
	if params.ActiveText == nil || *params.ActiveText != apeText {
		t.Errorf("expected active_text to be APE text, got %v", params.ActiveText)
	}
}

func TestRoutePredictOutcome_Rejected(t *testing.T) {
	mtText := "Hello translated"
	row := db.ListPreTranslatedByFileRow{
		ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText,
	}
	result := predictVerdict{
		ID: "100", Approved: false,
	}

	params := routePredictOutcome(row, aiPredictResult{predictVerdict: result})

	if params.Status != db.TranslationStatusNeedsReview {
		t.Errorf("expected needs_review, got %s", params.Status)
	}
	if params.PredictOutcome.PredictOutcome != db.PredictOutcomeRejected {
		t.Errorf("expected rejected outcome, got %s", params.PredictOutcome.PredictOutcome)
	}
	if params.ActiveText == nil || *params.ActiveText != mtText {
		t.Errorf("expected active_text to be original MT text, got %v", params.ActiveText)
	}
}

// QE verifies only — it never applies a postedit. approved=true →
// auto_approved with the MT text unchanged; anything else → needs_review, and
// any postedit the backend returns is deliberately ignored.
func TestRouteQEOutcome_Approved(t *testing.T) {
	mtText := "Hallo"
	row := db.ListPreTranslatedByFileRow{ID: 100, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText}
	params := routeQEOutcome(row, aiPredictResult{predictVerdict: predictVerdict{ID: "100", Approved: true}})

	if params.Status != db.TranslationStatusAutoApproved {
		t.Errorf("expected auto_approved, got %s", params.Status)
	}
	if params.PredictOutcome.PredictOutcome != db.PredictOutcomeApproved {
		t.Errorf("expected approved outcome, got %s", params.PredictOutcome.PredictOutcome)
	}
	if params.ActiveText == nil || *params.ActiveText != mtText {
		t.Errorf("QE must keep MT text, got %v", params.ActiveText)
	}
}

func TestRouteQEOutcome_RejectedIgnoresPostedit(t *testing.T) {
	mtText := "Hallo"
	apeText := "Hallo (verbessert)"
	apeApproved := true
	row := db.ListPreTranslatedByFileRow{ID: 100, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText}
	// Even with a confident postedit, QE ignores it and drops to the human.
	params := routeQEOutcome(row, aiPredictResult{predictVerdict: predictVerdict{
		ID: "100", Approved: false, Postedit: &apeText, PosteditApproved: &apeApproved,
	}})

	if params.Status != db.TranslationStatusNeedsReview {
		t.Errorf("expected needs_review, got %s", params.Status)
	}
	if params.PredictOutcome.PredictOutcome != db.PredictOutcomeRejected {
		t.Errorf("expected rejected outcome, got %s", params.PredictOutcome.PredictOutcome)
	}
	if params.ActiveText == nil || *params.ActiveText != mtText {
		t.Errorf("QE must keep MT text (ignore postedit), got %v", params.ActiveText)
	}
}

func TestRunAIStep_APEFullFlow(t *testing.T) {
	pred := &fakeExtPredictor{ape: true, verdict: approveUnless("Welt")}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")

	mtText1, mtText2 := "Hallo", "Welt"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText1},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "World", ActiveText: &mtText2},
	}

	svc, _ := newExtStepService(mq, pred)
	res, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if len(pred.calls) != 1 {
		t.Fatalf("expected 1 predict call (2 segments < 30 batch), got %d", len(pred.calls))
	}
	if pred.langs[0] != [2]string{"en", "de"} {
		t.Errorf("language pair sent = %v, want [en de]", pred.langs[0])
	}
	if res.Total != 2 || res.AutoApproved != 1 || res.NeedsReview != 1 {
		t.Fatalf("unexpected counts: %+v", res)
	}
	if len(mq.updatePredictCalls) != 2 {
		t.Fatalf("expected 2 predict updates, got %d", len(mq.updatePredictCalls))
	}
	for _, call := range mq.updatePredictCalls {
		switch call.ID {
		case 100:
			if call.Status != db.TranslationStatusAutoApproved {
				t.Errorf("ID 100: expected auto_approved, got %s", call.Status)
			}
		case 101:
			if call.Status != db.TranslationStatusNeedsReview {
				t.Errorf("ID 101: expected needs_review, got %s", call.Status)
			}
		default:
			t.Errorf("unexpected ID in predict update: %d", call.ID)
		}
	}
}

func TestRunAIStep_FiltersTargetLanguage(t *testing.T) {
	pred := &fakeExtPredictor{}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de", "fr"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")

	mtText := "translated"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText},
		{ID: 101, SegmentID: 2, TargetLanguage: "fr", SourceText: "Hello", ActiveText: &mtText},
	}

	svc, _ := newExtStepService(mq, pred)
	if _, err := svc.RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if pred.rowCount() != 1 {
		t.Fatalf("extension got %d rows, want only the 'de' one", pred.rowCount())
	}
	if len(mq.updatePredictCalls) != 1 {
		t.Fatalf("expected 1 predict update (only 'de'), got %d", len(mq.updatePredictCalls))
	}
	if mq.updatePredictCalls[0].ID != 100 {
		t.Errorf("expected prediction for ID 100, got %d", mq.updatePredictCalls[0].ID)
	}
}

func TestRunAIStep_SkipsNilMtText(t *testing.T) {
	pred := &fakeExtPredictor{}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")

	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: nil},
	}

	svc, _ := newExtStepService(mq, pred)
	res, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if len(pred.calls) != 0 {
		t.Fatalf("extension called %d times for a row with no text", len(pred.calls))
	}
	if len(mq.updatePredictCalls) != 0 {
		t.Fatalf("expected 0 predict updates (nil MtText), got %d", len(mq.updatePredictCalls))
	}
	if res.SkipReason != PhaseSkipNoSegments {
		t.Errorf("expected no_segments skip, got %q", res.SkipReason)
	}
}

// A server without the extension runtime wired skips the step instead of
// failing the job.
func TestRunAIStep_NoExtensionRuntime_SkipsSilently(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")

	mtText := "translated"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText},
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil).SetAIProvider(nil, nil, identityDecryptor{})
	res, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("expected no error when extensions are not wired, got: %v", err)
	}
	if res.SkipReason != PhaseSkipNoExtensions {
		t.Errorf("expected no_extensions skip, got %q", res.SkipReason)
	}
	if len(mq.updatePredictCalls) != 0 {
		t.Fatalf("expected 0 predict updates, got %d", len(mq.updatePredictCalls))
	}
}

func TestRunAIStep_EmptyPending_NoOp(t *testing.T) {
	pred := &fakeExtPredictor{}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")

	svc, _ := newExtStepService(mq, pred)
	res, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if res.SkipReason != PhaseSkipNoSegments {
		t.Errorf("expected no_segments skip, got %q", res.SkipReason)
	}
	if len(pred.calls) != 0 {
		t.Errorf("extension called %d times with nothing to score", len(pred.calls))
	}
}

// An error the extension returns must reach the user verbatim — the
// actionable "why" (e.g. unsupported language pair) is what lets them fix it.
func TestRunAIStep_PredictFailure_SurfacesExtensionError(t *testing.T) {
	pred := &fakeExtPredictor{err: &extension.CallError{Msg: "unsupported language pair en -> de"}}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "QE review")
	mtText := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText},
	}

	svc, _ := newExtStepService(mq, pred)
	_, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	if err == nil {
		t.Fatal("expected error when the extension fails")
	}
	msg, ok := httputil.AsClientError(err)
	if !ok {
		t.Fatalf("expected a ClientError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, "unsupported language pair") || !strings.Contains(msg, "Mock QE") {
		t.Fatalf("client error should name the extension and carry its message, got: %q", msg)
	}
	if len(mq.updatePredictCalls) != 0 {
		t.Fatalf("a failed batch wrote %d rows", len(mq.updatePredictCalls))
	}
}

// A host failure (not the extension's own error) gets a generic message: its
// text may describe the host, not the vendor.
func TestRunAIStep_HostFailure_GenericMessage(t *testing.T) {
	pred := &fakeExtPredictor{err: errors.New("wasm trap: out of bounds memory access")}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "QE review")
	mtText := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mtText},
	}

	svc, _ := newExtStepService(mq, pred)
	_, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	msg, ok := httputil.AsClientError(err)
	if !ok {
		t.Fatalf("expected a ClientError, got %T: %v", err, err)
	}
	if strings.Contains(msg, "wasm trap") || !strings.Contains(msg, "did not respond") {
		t.Fatalf("host failure message = %q", msg)
	}
}

// --- Rich text through the predict outcomes ---
//
// The editor and the export both prefer active_rich_text over active_text, so
// every outcome has to say what happens to the rich column. An outcome that
// leaves it alone blanks the tags; an outcome that rewrites the target without
// touching it ships the OLD target.

// Pins that an APE postedit on a tagged segment writes active_rich_text too, so
// the editor and the exported file show the postedit, not the pre-APE MT.
func TestRoutePredictOutcome_APEWritesRichText(t *testing.T) {
	mtText := "Klicken Sie Aktualisieren"
	mtRich := `<g id="1" ctype="bold">Klicken Sie Aktualisieren</g>`
	row := db.ListPreTranslatedByFileRow{
		ID: 100, SegmentID: 1, TargetLanguage: "de",
		SourceText: "Click Refresh", SourceRichText: ptr.To(`<g id="1" ctype="bold">Click Refresh</g>`),
		ActiveText: &mtText, ActiveRichText: &mtRich,
	}
	apeText := "Klicken Sie auf Aktualisieren"
	apeRich := `<g id="1" ctype="bold">Klicken Sie auf Aktualisieren</g>`
	approved := true
	result := aiPredictResult{
		predictVerdict: predictVerdict{
			ID: "100", Approved: false, Postedit: &apeText, PosteditApproved: &approved,
		},
		PosteditRichText: &apeRich,
	}

	params := routePredictOutcome(row, result)

	if params.ActiveRichText == nil {
		t.Fatal("APE postedit must write active_rich_text — leaving it NULL keeps the old target in the editor and the export")
	}
	if *params.ActiveRichText != apeRich {
		t.Errorf("active_rich_text = %q, want the postedit %q", *params.ActiveRichText, apeRich)
	}
}

// The mirror of the bug above: the outcomes that leave the target alone must
// pass the row's existing rich text through, or the COALESCE in the query is
// the only thing standing between an approved segment and losing its tags.
func TestRoutePredictOutcome_UnchangedOutcomesKeepRichText(t *testing.T) {
	mtText := "Klicken Sie Aktualisieren"
	mtRich := `<g id="1" ctype="bold">Klicken Sie Aktualisieren</g>`
	row := db.ListPreTranslatedByFileRow{
		ID: 100, TargetLanguage: "de", SourceText: "Click Refresh",
		ActiveText: &mtText, ActiveRichText: &mtRich,
	}

	cases := map[string]db.UpdateTranslationPredictParams{
		"approved":    routePredictOutcome(row, aiPredictResult{predictVerdict: predictVerdict{ID: "100", Approved: true}}),
		"rejected":    routePredictOutcome(row, aiPredictResult{predictVerdict: predictVerdict{ID: "100", Approved: false}}),
		"qe approved": routeQEOutcome(row, aiPredictResult{predictVerdict: predictVerdict{ID: "100", Approved: true}}),
		"qe rejected": routeQEOutcome(row, aiPredictResult{predictVerdict: predictVerdict{ID: "100", Approved: false}}),
	}
	for name, params := range cases {
		if params.ActiveRichText == nil || *params.ActiveRichText != mtRich {
			t.Errorf("%s: active_rich_text = %v, want the row's existing %q", name, params.ActiveRichText, mtRich)
		}
	}
}

// An APE postedit with no tags on a segment that had them must not leave the
// old rich text in place — COALESCE would then hide the postedit completely.
func TestRoutePredictOutcome_APEWithoutTagsClearsStaleRichText(t *testing.T) {
	mtText := "alt"
	mtRich := `<g id="1" ctype="bold">alt</g>`
	row := db.ListPreTranslatedByFileRow{
		ID: 100, TargetLanguage: "de", SourceText: "old", ActiveText: &mtText, ActiveRichText: &mtRich,
	}
	apeText := "neu"
	approved := true
	params := routePredictOutcome(row, aiPredictResult{
		predictVerdict: predictVerdict{
			ID: "100", Approved: false, Postedit: &apeText, PosteditApproved: &approved,
		},
		// No PosteditRichText: the model returned plain text.
	})

	// nil is not enough: the query COALESCEs a nil rich text with the old one.
	if params.ActiveRichText == nil || *params.ActiveRichText != apeText {
		t.Errorf("active_rich_text = %v, want the postedit %q — the editor would still show the old target", params.ActiveRichText, apeText)
	}
}

// A postedit always replaces the translation, and postedit_approved — not
// approved — decides whether it ships. An extension that sets both approved
// and a postedit gets its rewrite applied, not the original MT.
func TestRoutePredictOutcome_PosteditWinsOverApproved(t *testing.T) {
	mtText := "Hello translated"
	row := db.ListPreTranslatedByFileRow{ID: 100, ActiveText: &mtText}
	rewrite := "Hello rewritten"
	yes, no := true, false

	shipped := routePredictOutcome(row, aiPredictResult{predictVerdict: predictVerdict{
		ID: "100", Approved: true, Postedit: &rewrite, PosteditApproved: &yes,
	}})
	if shipped.ActiveText == nil || *shipped.ActiveText != rewrite {
		t.Errorf("active_text = %v, want the postedit %q", shipped.ActiveText, rewrite)
	}
	if shipped.PredictOutcome.PredictOutcome != db.PredictOutcomeApeApproved || shipped.Status != db.TranslationStatusAutoApproved {
		t.Errorf("outcome %s status %s, want ape_approved auto_approved", shipped.PredictOutcome.PredictOutcome, shipped.Status)
	}

	held := routePredictOutcome(row, aiPredictResult{predictVerdict: predictVerdict{
		ID: "100", Approved: true, Postedit: &rewrite, PosteditApproved: &no,
	}})
	if held.Status != db.TranslationStatusNeedsReview || held.PredictOutcome.PredictOutcome != db.PredictOutcomeApeNeedsReview {
		t.Errorf("approved=true must not ship an unapproved postedit: outcome %s status %s",
			held.PredictOutcome.PredictOutcome, held.Status)
	}
}
