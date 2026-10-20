package pipeline

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/pkg/extsdk"
)

// The predict phase writes one history row per result, and which one depends on
// whether the text moved:
//
//   ape_approved / ape_needs_review — APE rewrote the target: an `ape` row with
//       the new text, plus translations.origin='ape'.
//   approved — the target is accepted untouched: an `auto_approved` row, no
//       text, no origin update (nothing came from anywhere new).
//   rejected — neither text nor approval changed: no row at all.
//
// Mock-based unit test: drives RunAIStep (APE engine) against a fake extension
// and asserts on mock-querier call records (no real DB). Mirrors the
// TestRunAIStep_APEFullFlow shape.
//
// Anti-tautology: removing the EmitAutomationHistory call inside the APE
// branch in aistep.go drops insertHistoryCalls back to 0 — verified
// locally 2026-05-26.

// historyCallsOfKind filters the mock's recorded history inserts. Predict
// writes one row per result — `ape` when the text moved, `auto_approved` when
// it was accepted untouched — so a bare len() over all calls does not say
// what any single test means.
func historyCallsOfKind(mq *mockQuerier, kind db.TranslationHistoryKind) []db.InsertTranslationHistoryParams {
	var out []db.InsertTranslationHistoryParams
	for _, call := range mq.insertHistoryCalls {
		if call.Kind == kind {
			out = append(out, call)
		}
	}
	return out
}

// fourOutcomes gives translations 100-103 the four predict outcomes:
// approved, ape_approved, ape_needs_review and rejected.
func fourOutcomes(apeText string) func(extsdk.PredictRow) extsdk.PredictResult {
	yes, no := true, false
	return func(r extsdk.PredictRow) extsdk.PredictResult {
		switch r.ID {
		case "100":
			return extsdk.PredictResult{Approved: true}
		case "101":
			return extsdk.PredictResult{Postedit: &apeText, PosteditApproved: &yes}
		case "102":
			return extsdk.PredictResult{Postedit: &apeText, PosteditApproved: &no}
		}
		return extsdk.PredictResult{}
	}
}

func TestRunAIStep_EmitsApeHistoryOnApeOutcomes(t *testing.T) {
	apeText := "Hallo Welt (verbessert)"

	pred := &fakeExtPredictor{ape: true, verdict: fourOutcomes(apeText)}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")

	mt := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello A", ActiveText: &mt},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "Hello B", ActiveText: &mt},
		{ID: 102, SegmentID: 3, TargetLanguage: "de", SourceText: "Hello C", ActiveText: &mt},
		{ID: 103, SegmentID: 4, TargetLanguage: "de", SourceText: "Hello D", ActiveText: &mt},
	}

	svc, _ := newExtStepService(mq, pred)
	if _, err := svc.RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}

	// All four UpdateTranslationPredict calls must fire (predict ran on
	// every pre-translated row regardless of outcome).
	if len(mq.updatePredictCalls) != 4 {
		t.Fatalf("expected 4 predict updates, got %d", len(mq.updatePredictCalls))
	}

	// Two APE rewrites + one auto-approval. Only the rewrites touch
	// active_source: an approval changes no text, so there is no new lineage.
	if len(mq.insertHistoryCalls) != 3 {
		t.Fatalf("expected 3 history rows (2 ape + 1 auto_approved), got %d", len(mq.insertHistoryCalls))
	}
	if len(mq.updateActiveSourceCalls) != 2 {
		t.Fatalf("expected 2 active_source updates, got %d", len(mq.updateActiveSourceCalls))
	}

	byID := map[int64]db.InsertTranslationHistoryParams{}
	for _, call := range mq.insertHistoryCalls {
		byID[call.TranslationID] = call
	}
	for _, id := range []int64{101, 102} {
		call, ok := byID[id]
		if !ok {
			t.Errorf("translation %d: want an ape history row, got none", id)
			continue
		}
		if call.Kind != db.TranslationHistoryKindApe {
			t.Errorf("translation %d: history kind = %s, want 'ape'", id, call.Kind)
		}
		if call.Text == nil || *call.Text != apeText {
			t.Errorf("translation %d: history.text = %v, want %q", id, call.Text, apeText)
		}
	}
	// The approval is its own event, and carries no text — a text snapshot here
	// would become the next confirm's edit-distance baseline.
	approval, ok := byID[100]
	if !ok {
		t.Errorf("translation 100: want an auto_approved row, got none")
	} else {
		if approval.Kind != db.TranslationHistoryKindAutoApproved {
			t.Errorf("translation 100: history kind = %s, want 'auto_approved'", approval.Kind)
		}
		if approval.Text != nil {
			t.Errorf("translation 100: auto_approved.text = %q, want NULL", *approval.Text)
		}
	}
	if _, ok := byID[103]; ok {
		t.Errorf("translation 103: rejected changed neither text nor approval — it must emit nothing")
	}
	// Both kinds are attributed to the step that ran them (addAIStepJob's
	// assignment 910), so the History panel can name it — a hybrid step's APE
	// row sits in the same step as the reviewer's edits that follow it. Both
	// also name the AI engine (addAIStepJob's 901), so an approval charts by
	// engine like a post-edit does.
	for _, call := range mq.insertHistoryCalls {
		if call.JobStepAssignmentID == nil || *call.JobStepAssignmentID != 910 {
			t.Errorf("translation %d (%s): job_step_assignment_id = %v, want 910", call.TranslationID, call.Kind, call.JobStepAssignmentID)
		}
		if call.AiEngineID == nil || *call.AiEngineID != 901 {
			t.Errorf("translation %d (%s): ai_engine_id = %v, want 901", call.TranslationID, call.Kind, call.AiEngineID)
		}
	}

	for _, call := range mq.updateActiveSourceCalls {
		if !call.Origin.Valid || call.Origin.TranslationOrigin != db.TranslationOriginApe {
			t.Errorf("active_source update: want origin='ape', got valid=%v value=%v", call.Origin.Valid, call.Origin.TranslationOrigin)
		}
		if call.TmMatchPercent != nil {
			t.Errorf("active_source update: tm_match_percent must be NULL for APE; got %v", *call.TmMatchPercent)
		}
	}
}

// An APE rewrite replaces active_text, so the segment's stored QA findings —
// and any ignore on them — describe text that is gone.
// Same gate as the history row above: only the two ape_* outcomes change text.
//
// Anti-tautology RED proof: drop the dropStaleQAResults call from the APE
// branch in aistep.go and droppedQAResultIDs comes back empty.
func TestRunAIStep_DropsStaleQAResultsOnApeRewrites(t *testing.T) {
	apeText := "Hallo Welt (verbessert)"

	pred := &fakeExtPredictor{ape: true, verdict: fourOutcomes(apeText)}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")

	mt := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello A", ActiveText: &mt},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "Hello B", ActiveText: &mt},
		{ID: 102, SegmentID: 3, TargetLanguage: "de", SourceText: "Hello C", ActiveText: &mt},
		{ID: 103, SegmentID: 4, TargetLanguage: "de", SourceText: "Hello D", ActiveText: &mt},
	}

	svc, _ := newExtStepService(mq, pred)
	if _, err := svc.RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}

	dropped := map[int64]bool{}
	for _, id := range mq.droppedQAResultIDs {
		dropped[id] = true
	}
	if !dropped[101] || !dropped[102] {
		t.Errorf("APE-rewritten rows must lose their QA findings; dropped=%v", mq.droppedQAResultIDs)
	}
	if dropped[100] || dropped[103] {
		t.Errorf("rows whose text APE left alone must keep their findings; dropped=%v", mq.droppedQAResultIDs)
	}
}
