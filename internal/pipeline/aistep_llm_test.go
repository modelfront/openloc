package pipeline

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/ptr"
)

// fakePredictor scripts LLM QE+APE verdicts per translation ID and
// records the configs/inputs it was called with.
type fakePredictor struct {
	results   map[string]ai.PredictResult
	err       error
	gotCfgs   []ai.PredictConfig
	gotInputs [][]ai.PredictInput
}

func (f *fakePredictor) PredictBatch(_ context.Context, cfg ai.PredictConfig, rows []ai.PredictInput) ([]ai.PredictResult, error) {
	f.gotCfgs = append(f.gotCfgs, cfg)
	f.gotInputs = append(f.gotInputs, rows)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]ai.PredictResult, len(rows))
	for i, r := range rows {
		res := f.results[r.ID]
		res.ID = r.ID
		out[i] = res
	}
	return out, nil
}

func newLLMAIStepService(mq *mockQuerier, p ai.Predictor) *Service {
	return NewService(mq, &passthroughTxRunner{q: mq}, nil).SetAIProvider(nil, p, identityDecryptor{})
}

// APE full flow across the outcome matrix under the split-verdict contract
// (approved/score judge the ORIGINAL; postedit_approved/postedit_score judge
// the postedit, which applies ONLY when postedit_approved):
//   - 100 original approved                        → auto_approved, text unchanged, qe_note = score
//   - 101 rejected original + APPROVED postedit    → auto_approved, postedit applied, qe_note = postedit_score
//   - 102 rejected original + UNapproved postedit  → needs_review, postedit DISCARDED, qe_note = score
//   - 103 rejected, no postedit                    → needs_review, text unchanged
//
// Comments land as AI segment comments; only the applied postedit emits ape
// history with source/model/note details.
func TestRunAIStep_LLM_APEFullFlow(t *testing.T) {
	fp := &fakePredictor{results: map[string]ai.PredictResult{
		"1": {Approved: true, Score: ptr.To[float64](96)},
		"2": {Approved: false, Score: ptr.To[float64](40), Postedit: ptr.To("Hallo besser"), PosteditApproved: ptr.To(true), PosteditScore: ptr.To[float64](88), Comment: ptr.To("fixed the greeting")},
		"3": {Approved: false, Score: ptr.To[float64](60), Postedit: ptr.To("Hallo vielleicht"), PosteditApproved: ptr.To(false), PosteditScore: ptr.To[float64](65), Comment: ptr.To("meaning unclear")},
		"4": {Approved: false, Score: ptr.To[float64](12)},
	}}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.setAIProviderConn(1, "openai", "sk-test")
	mq.addLLMAIStepJob(5, 1, 1, "de", "qe_ape", "LLM APE", "openai", "reject")

	mt := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "de", SourceText: "Hello A", ActiveText: &mt},
		{ID: 101, SegmentID: 2, Position: 1, TargetLanguage: "de", SourceText: "Hello B", ActiveText: &mt},
		{ID: 102, SegmentID: 3, Position: 2, TargetLanguage: "de", SourceText: "Hello C", ActiveText: &mt},
		{ID: 103, SegmentID: 4, Position: 3, TargetLanguage: "de", SourceText: "Hello D", ActiveText: &mt},
	}

	res, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if res.Total != 4 || res.AutoApproved != 2 || res.NeedsReview != 2 {
		t.Fatalf("unexpected counts: %+v", res)
	}

	// The predictor got the APE contract with the row's preset + prompt.
	if len(fp.gotCfgs) != 1 {
		t.Fatalf("expected 1 batch (4 segments < llmBatchSize), got %d", len(fp.gotCfgs))
	}
	cfg := fp.gotCfgs[0]
	if cfg.Provider != "openai" || cfg.Model != "test-model" || cfg.APIKey != "sk-test" || cfg.Mode != ai.ModeQEAPE {
		t.Errorf("unexpected predict config: %+v", cfg)
	}
	if cfg.PromptText != "Evaluate carefully." || cfg.SourceLang != "en" || cfg.TargetLang != "de" {
		t.Errorf("prompt/langs not threaded: %+v", cfg)
	}

	byID := map[int64]db.UpdateTranslationPredictParams{}
	for _, call := range mq.updatePredictCalls {
		byID[call.ID] = call
	}
	if len(byID) != 4 {
		t.Fatalf("expected 4 predict updates, got %d", len(byID))
	}
	assertUpd := func(id int64, status db.TranslationStatus, outcome db.PredictOutcome, text, note string) {
		t.Helper()
		call := byID[id]
		if call.Status != status {
			t.Errorf("ID %d: status want %s, got %s", id, status, call.Status)
		}
		if !call.PredictOutcome.Valid || call.PredictOutcome.PredictOutcome != outcome {
			t.Errorf("ID %d: outcome want %s, got %+v", id, outcome, call.PredictOutcome)
		}
		if call.ActiveText == nil || *call.ActiveText != text {
			t.Errorf("ID %d: active_text want %q, got %v", id, text, call.ActiveText)
		}
		if call.QeNote == nil || *call.QeNote != note {
			t.Errorf("ID %d: qe_note want %q, got %v", id, note, call.QeNote)
		}
	}
	assertUpd(100, db.TranslationStatusAutoApproved, db.PredictOutcomeApproved, "Hallo", "96")
	// Applied postedit keeps the POSTEDIT's score (88), not the original's (40).
	assertUpd(101, db.TranslationStatusAutoApproved, db.PredictOutcomeApeApproved, "Hallo besser", "88")
	// An unapproved postedit is discarded — original MT + its score stay.
	assertUpd(102, db.TranslationStatusNeedsReview, db.PredictOutcomeRejected, "Hallo", "60")
	assertUpd(103, db.TranslationStatusNeedsReview, db.PredictOutcomeRejected, "Hallo", "12")

	// The `comment` field lands as AI segment comments — one per commented row.
	if len(mq.createAICommentCalls) != 2 {
		t.Fatalf("expected 2 AI comments, got %d", len(mq.createAICommentCalls))
	}
	comments := map[int64]string{}
	for _, c := range mq.createAICommentCalls {
		comments[c.TranslationID] = c.Content
	}
	if comments[101] != "fixed the greeting" || comments[102] != "meaning unclear" {
		t.Errorf("unexpected AI comments: %v", comments)
	}

	// Only the APPLIED postedit (101) rewrote text, so it is the only `ape`
	// row. 100 was approved untouched and gets an `auto_approved` row instead;
	// the rejected 102/103 get nothing.
	apeCalls := historyCallsOfKind(mq, db.TranslationHistoryKindApe)
	if len(apeCalls) != 1 {
		t.Fatalf("expected 1 ape history row, got %d", len(apeCalls))
	}
	approvals := historyCallsOfKind(mq, db.TranslationHistoryKindAutoApproved)
	if len(approvals) != 1 || approvals[0].TranslationID != 100 {
		t.Fatalf("expected 1 auto_approved row on translation 100, got %+v", approvals)
	}
	var details map[string]any
	if err := json.Unmarshal(apeCalls[0].Details, &details); err != nil {
		t.Fatalf("unmarshal history details: %v", err)
	}
	if apeCalls[0].TranslationID != 101 {
		t.Errorf("history must be for translation 101, got %d", apeCalls[0].TranslationID)
	}
	if details["source"] != "openai" || details["model"] != "test-model" {
		t.Errorf("history details want source=openai model=test-model, got %v", details)
	}
	if _, ok := details["note"]; !ok {
		t.Errorf("history details should carry the note, got %v", details)
	}
}

// QE mode: the config must not ask for a postedit, and even if the model
// returns one anyway it is never applied — approve/reject only, text intact.
// Comments still land.
func TestRunAIStep_LLM_QEIgnoresPostedit(t *testing.T) {
	fp := &fakePredictor{results: map[string]ai.PredictResult{
		"1": {Approved: true, Score: ptr.To[float64](95)},
		"2": {Approved: false, Postedit: ptr.To("sneaky rewrite"), Score: ptr.To[float64](30), Comment: ptr.To("wrong term")},
	}}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.setAIProviderConn(1, "claude", "sk-ant")
	mq.addLLMAIStepJob(5, 1, 1, "de", "qe", "LLM QE", "claude", "reject")

	mt := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "de", SourceText: "Hello A", ActiveText: &mt},
		{ID: 101, SegmentID: 2, Position: 1, TargetLanguage: "de", SourceText: "Hello B", ActiveText: &mt},
	}

	res, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if res.AutoApproved != 1 || res.NeedsReview != 1 {
		t.Fatalf("unexpected counts: %+v", res)
	}
	if fp.gotCfgs[0].Mode != ai.ModeQE {
		t.Errorf("a QE prompt must send the QE contract, got mode %q", fp.gotCfgs[0].Mode)
	}
	for _, call := range mq.updatePredictCalls {
		if call.ActiveText == nil || *call.ActiveText != "Hallo" {
			t.Errorf("ID %d: QE must keep the MT text, got %v", call.ID, call.ActiveText)
		}
	}
	// QE never rewrites text, so it never writes an `ape` row. The approval it
	// did make is recorded on its own, with no text.
	if got := historyCallsOfKind(mq, db.TranslationHistoryKindApe); len(got) != 0 {
		t.Errorf("QE must not emit ape history, got %d rows", len(got))
	}
	approvals := historyCallsOfKind(mq, db.TranslationHistoryKindAutoApproved)
	if len(approvals) != 1 || approvals[0].TranslationID != 100 {
		t.Fatalf("expected 1 auto_approved row on translation 100, got %+v", approvals)
	}
	if approvals[0].Text != nil {
		t.Errorf("auto_approved.text = %q, want NULL", *approvals[0].Text)
	}
	if len(mq.createAICommentCalls) != 1 || mq.createAICommentCalls[0].TranslationID != 101 {
		t.Errorf("expected 1 AI comment on 101, got %+v", mq.createAICommentCalls)
	}
}

// Unparseable model output follows the row's bad-output action: "reject"
// drops the segment to the human (rejected outcome), "ignore" leaves it
// completely untouched, "comment" rejects AND attaches the raw output as an
// AI comment.
func TestRunAIStep_LLM_BadOutputAction(t *testing.T) {
	for _, tc := range []struct {
		action       string
		wantUpdates  int
		wantComments int
	}{
		{"reject", 1, 0},
		{"ignore", 0, 0},
		{"comment", 1, 1},
	} {
		fp := &fakePredictor{results: map[string]ai.PredictResult{
			"1": {Invalid: true, Raw: "I refuse to answer in JSON"},
		}}
		mq := newMockQuerier()
		mq.addFileProject(1, 10, nil, "en", []string{"de"})
		mq.setAIProviderConn(1, "openai", "sk-test")
		mq.addLLMAIStepJob(5, 1, 1, "de", "qe_ape", "LLM APE", "openai", tc.action)
		mt := "Hallo"
		mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
			{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mt},
		}

		if _, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil); err != nil {
			t.Fatalf("action=%s RunAIStep: %v", tc.action, err)
		}
		if len(mq.updatePredictCalls) != tc.wantUpdates {
			t.Fatalf("action=%s: expected %d predict updates, got %d", tc.action, tc.wantUpdates, len(mq.updatePredictCalls))
		}
		if len(mq.createAICommentCalls) != tc.wantComments {
			t.Fatalf("action=%s: expected %d AI comments, got %d", tc.action, tc.wantComments, len(mq.createAICommentCalls))
		}
		if tc.wantUpdates == 1 {
			call := mq.updatePredictCalls[0]
			if call.Status != db.TranslationStatusNeedsReview ||
				call.PredictOutcome.PredictOutcome != db.PredictOutcomeRejected {
				t.Errorf("action=%s: bad output must reject to human, got %+v", tc.action, call)
			}
			if call.QeNote != nil {
				t.Errorf("bad output carries no note, got %v", *call.QeNote)
			}
		}
		if tc.wantComments == 1 {
			c := mq.createAICommentCalls[0]
			if c.TranslationID != 100 || !strings.Contains(c.Content, "unparseable output") || !strings.Contains(c.Content, "I refuse") {
				t.Errorf("comment action must attach the raw output, got %+v", c)
			}
		}
	}
}

// An LLM row whose provider isn't connected is a benign skip (the human still
// reviews).
func TestRunAIStep_LLM_ProviderNotConnected_SkipsSilently(t *testing.T) {
	fp := &fakePredictor{}
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	// No setAIProviderConn — openai has no vendor token.
	mq.addLLMAIStepJob(5, 1, 1, "de", "qe_ape", "LLM APE", "openai", "reject")
	mt := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mt},
	}

	res, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("expected benign skip, got: %v", err)
	}
	if res.SkipReason != PhaseSkipNoProvider {
		t.Errorf("expected no_provider skip, got %q", res.SkipReason)
	}
	if len(fp.gotCfgs) != 0 || len(mq.updatePredictCalls) != 0 {
		t.Errorf("nothing must run when the provider is not connected")
	}
}

// A provider-side failure (bad key etc.) surfaces as an actionable ClientError,
// like the extension predict-failure path.
func TestRunAIStep_LLM_ProviderErrorSurfaced(t *testing.T) {
	fp := &fakePredictor{err: &ai.ProviderError{Provider: "openai", Status: 401, Kind: ai.ProviderErrAuth}}
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.setAIProviderConn(1, "openai", "sk-test")
	mq.addLLMAIStepJob(5, 1, 1, "de", "qe_ape", "LLM APE", "openai", "reject")
	mt := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mt},
	}

	_, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil)
	if err == nil {
		t.Fatal("expected error when the provider rejects the key")
	}
	msg, ok := httputil.AsClientError(err)
	if !ok {
		t.Fatalf("expected a ClientError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, "rejected the API key") {
		t.Fatalf("client error should carry the actionable reason, got: %q", msg)
	}
}

// The model is addressed by segment number, the database by translation id.
// Those are different numbers, and mixing them up applies a verdict to the
// wrong row — silently, since both are valid ints.
func TestRunAIStep_AddressesTheModelBySegmentNumberAndTheDatabaseByID(t *testing.T) {
	// Verdicts scripted per SEGMENT NUMBER, deliberately the reverse of the
	// row ids, so a runner that sent ids would map every result to the wrong
	// row rather than merely to a differently-named one.
	fp := &fakePredictor{results: map[string]ai.PredictResult{
		"1": {Approved: true, Score: ptr.To[float64](99)},
		"2": {Approved: false, Score: ptr.To[float64](10)},
	}}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.setAIProviderConn(1, "openai", "sk-test")
	mq.addLLMAIStepJob(5, 1, 1, "de", "qe", "LLM QE", "openai", "reject")

	mt := "Hallo"
	// Row ids run high and descending; segment numbers are 1 and 2.
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 48211, SegmentID: 7, Position: 0, TargetLanguage: "de", SourceText: "Hello A", ActiveText: &mt},
		{ID: 48210, SegmentID: 8, Position: 1, TargetLanguage: "de", SourceText: "Hello B", ActiveText: &mt},
	}

	if _, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}

	// What went out: the file's segment numbers, not the row ids.
	var sent []string
	for _, in := range fp.gotInputs[0] {
		sent = append(sent, in.ID)
	}
	if !reflect.DeepEqual(sent, []string{"1", "2"}) {
		t.Errorf("segment ids on the wire = %v, want the file's segment numbers [1 2]", sent)
	}

	// What landed: segment 1's approval on row 48211, segment 2's rejection on
	// 48210 — the mapping back, which is the half that can silently corrupt.
	byID := map[int64]db.UpdateTranslationPredictParams{}
	for _, call := range mq.updatePredictCalls {
		byID[call.ID] = call
	}
	if got := byID[48211].Status; got != db.TranslationStatusAutoApproved {
		t.Errorf("row 48211 (segment 1, approved) = %v, want auto_approved", got)
	}
	if got := byID[48210].Status; got != db.TranslationStatusNeedsReview {
		t.Errorf("row 48210 (segment 2, rejected) = %v, want needs_review", got)
	}
}
