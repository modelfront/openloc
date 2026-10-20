package pipeline

import (
	"context"
	"testing"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
)

// An APE prompt stores out_status = false, so an APE engine never decides
// whether a segment ships. Its job is to post-edit every translation and hand
// the result to a human — outcome ape_needs_review.

func TestMapLLMPredictResult_APEAppliesThePosteditAndNeverApproves(t *testing.T) {
	postedit := "Datei speichern."
	score := 88.0

	got := mapLLMPredictResult(ai.PredictResult{
		ID: "1", Postedit: &postedit, Score: &score,
	}, ai.ModeAPE, "Datei sichern.", "", nil)

	if got.Postedit == nil || *got.Postedit != postedit {
		t.Fatalf("the postedit must be applied, got %v", got.Postedit)
	}
	if got.Approved {
		t.Error("an APE engine must not approve a segment")
	}
	// nil, not false: routePredictOutcome reads a non-nil true as "APE was
	// confident, auto-approve". Leaving it unset is what lands outcome 3.
	if got.PosteditApproved != nil {
		t.Errorf("PosteditApproved must stay unset, got %v", *got.PosteditApproved)
	}
	// The APE contract asks for no score, and the fixture volunteers one anyway.
	// Recording it would put a machine verdict on a segment whose whole point is
	// that a human judges it.
	if got.Note != nil {
		t.Errorf("an APE engine must record no score, got %q", *got.Note)
	}
}

// End to end through the router: what the segment actually ends up as.
func TestAPEOutcome_LandsInNeedsReviewWithTheFix(t *testing.T) {
	postedit := "Datei speichern."
	original := "Datei sichern."
	row := db.ListPreTranslatedByFileRow{ID: 1, ActiveText: &original}

	mapped := mapLLMPredictResult(ai.PredictResult{
		ID: "1", Postedit: &postedit,
	}, ai.ModeAPE, original, "", nil)
	params := routePredictOutcome(row, mapped)

	if params.Status != db.TranslationStatusNeedsReview {
		t.Errorf("status = %v, want needs_review — APE never auto-approves", params.Status)
	}
	if params.PredictOutcome.PredictOutcome != db.PredictOutcomeApeNeedsReview {
		t.Errorf("outcome = %v, want ape_needs_review", params.PredictOutcome.PredictOutcome)
	}
	if params.ActiveText == nil || *params.ActiveText != postedit {
		t.Errorf("active_text = %v, want the postedit — APE's whole job is applying it", params.ActiveText)
	}
}

// A model that says "nothing to fix" leaves the translation alone, and the
// human still reviews it.
func TestMapLLMPredictResult_APEWithNoPosteditLeavesTheTextAlone(t *testing.T) {
	got := mapLLMPredictResult(ai.PredictResult{ID: "1"}, ai.ModeAPE, "Datei sichern.", "", nil)

	if got.Postedit != nil {
		t.Errorf("no postedit was offered, so none must be applied: %q", *got.Postedit)
	}
	if got.Approved {
		t.Error("an APE engine must not approve a segment")
	}
}

// QE+APE is the mode that judges, and it is unchanged: an approved postedit
// still auto-approves.
func TestMapLLMPredictResult_QEAPEStillAutoApprovesItsOwnFix(t *testing.T) {
	postedit := "Datei speichern."
	yes := true
	score := 93.0

	got := mapLLMPredictResult(ai.PredictResult{
		ID: "1", Approved: false, Postedit: &postedit,
		PosteditApproved: &yes, PosteditScore: &score,
	}, ai.ModeQEAPE, "Datei sichern.", "", nil)

	if got.Postedit == nil {
		t.Fatal("QE+APE must still apply an approved postedit")
	}
	if got.PosteditApproved == nil || !*got.PosteditApproved {
		t.Error("QE+APE must still mark its approved postedit approved")
	}
}

// The whole step, through the real runner: an APE engine touches every segment
// and auto-approves none of them.
func TestRunAIStep_LLM_APENeverAutoApproves(t *testing.T) {
	fp := &fakePredictor{results: map[string]ai.PredictResult{
		// A fix, a "nothing to change", and a model that volunteers a verdict
		// it was never asked for — none of the three may auto-approve.
		"1": {Postedit: ptr.To("Hallo besser"), Score: ptr.To[float64](88), Comment: ptr.To("fixed the greeting")},
		"2": {Score: ptr.To[float64](95)},
		"3": {Approved: true, Postedit: ptr.To("Hallo anders"), PosteditApproved: ptr.To(true), Score: ptr.To[float64](99)},
	}}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.setAIProviderConn(1, "openai", "sk-test")
	mq.addLLMAIStepJob(5, 1, 1, "de", "ape", "LLM APE", "openai", "reject")

	mt := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "de", SourceText: "Hello A", ActiveText: &mt},
		{ID: 101, SegmentID: 2, Position: 1, TargetLanguage: "de", SourceText: "Hello B", ActiveText: &mt},
		{ID: 102, SegmentID: 3, Position: 2, TargetLanguage: "de", SourceText: "Hello C", ActiveText: &mt},
	}

	res, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if res.AutoApproved != 0 {
		t.Errorf("an APE step auto-approved %d segments; it declares no status output", res.AutoApproved)
	}
	if res.Total != 3 || res.NeedsReview != 3 {
		t.Fatalf("every segment must reach a human: %+v", res)
	}

	// …and the engine was asked for the APE contract, not the judging one.
	if len(fp.gotCfgs) != 1 || fp.gotCfgs[0].Mode != ai.ModeAPE {
		t.Fatalf("APE prompt must send the APE contract, got %+v", fp.gotCfgs)
	}

	byID := map[int64]db.UpdateTranslationPredictParams{}
	for _, call := range mq.updatePredictCalls {
		byID[call.ID] = call
	}
	// The fix lands in the segment, and the human still reviews it.
	if got := byID[100]; got.ActiveText == nil || *got.ActiveText != "Hallo besser" {
		t.Errorf("the postedit must be applied, got %v", got.ActiveText)
	}
	if got := byID[100].PredictOutcome.PredictOutcome; got != db.PredictOutcomeApeNeedsReview {
		t.Errorf("outcome = %v, want ape_needs_review", got)
	}
	// Nothing to change: the translation stays as it was.
	if got := byID[101]; got.ActiveText == nil || *got.ActiveText != mt {
		t.Errorf("a segment with no postedit must keep its text, got %v", got.ActiveText)
	}
	// An off-contract verdict is ignored rather than obeyed.
	if got := byID[102].Status; got != db.TranslationStatusNeedsReview {
		t.Errorf("ID 102: status %v — an APE engine must not act on a verdict it was not asked for", got)
	}
}
