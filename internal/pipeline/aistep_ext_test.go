package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"openloc/internal/httputil"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/enginehealth"
	"openloc/internal/extension"
	"openloc/pkg/extsdk"
)

// extStepJob seeds a job on an extension QE+APE step with the given MT rows
// (ids 100, 101, …, all target "de").
func extStepJob(targets ...string) *mockQuerier {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "Review")
	rows := make([]db.ListPreTranslatedByFileRow, len(targets))
	for i := range targets {
		rows[i] = db.ListPreTranslatedByFileRow{
			ID: int64(100 + i), SegmentID: int64(i + 1), Position: int32(i),
			TargetLanguage: "de", SourceText: "src", ActiveText: &targets[i],
		}
	}
	mq.preTranslated[1] = rows
	return mq
}

func predictCallFor(t *testing.T, mq *mockQuerier, id int64) db.UpdateTranslationPredictParams {
	t.Helper()
	for _, c := range mq.updatePredictCalls {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no predict update for translation %d", id)
	return db.UpdateTranslationPredictParams{}
}

// An extension without the ape capability only verifies: a postedit it
// returns anyway is ignored, so the MT text stays and nothing is approved on
// the strength of a rewrite.
func TestRunAIStep_QEOnlyExtensionIgnoresPostedit(t *testing.T) {
	rewrite, yes := "rewritten", true
	pred := &fakeExtPredictor{ape: false, verdict: func(r extsdk.PredictRow) extsdk.PredictResult {
		if r.Target == "good" {
			return extsdk.PredictResult{Approved: true}
		}
		return extsdk.PredictResult{Postedit: &rewrite, PosteditApproved: &yes}
	}}
	mq := extStepJob("good", "bad")

	svc, _ := newExtStepService(mq, pred)
	var labels []string
	res, err := svc.RunAIStep(context.Background(), 1, 5, func(phase string, _, _ int) { labels = append(labels, phase) })
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if res.AutoApproved != 1 || res.NeedsReview != 1 {
		t.Fatalf("counts = %+v, want 1 approved, 1 needs review", res)
	}
	good := predictCallFor(t, mq, 100)
	if good.Status != db.TranslationStatusAutoApproved || good.PredictOutcome.PredictOutcome != db.PredictOutcomeApproved {
		t.Errorf("approved row: status %s outcome %s", good.Status, good.PredictOutcome.PredictOutcome)
	}
	bad := predictCallFor(t, mq, 101)
	if bad.Status != db.TranslationStatusNeedsReview {
		t.Errorf("rejected row status = %s, want needs_review", bad.Status)
	}
	if bad.ActiveText == nil || *bad.ActiveText != "bad" {
		t.Errorf("QE must keep the MT text, got %v", bad.ActiveText)
	}
	if len(mq.updateActiveSourceCalls) != 0 {
		t.Errorf("QE wrote %d ape lineage updates, want none", len(mq.updateActiveSourceCalls))
	}
	if len(labels) == 0 || labels[0] != AIStepLabel("qe") {
		t.Errorf("progress label = %v, want %q", labels, AIStepLabel("qe"))
	}
}

// An extension with the ape capability rewrites: its postedit replaces the
// target and its postedit_approved decides the approval.
func TestRunAIStep_APEExtensionAppliesPostedit(t *testing.T) {
	rewrite, yes, no := "rewritten", true, false
	pred := &fakeExtPredictor{ape: true, verdict: func(r extsdk.PredictRow) extsdk.PredictResult {
		switch r.Target {
		case "fix-and-ship":
			return extsdk.PredictResult{Postedit: &rewrite, PosteditApproved: &yes}
		case "fix-and-check":
			return extsdk.PredictResult{Postedit: &rewrite, PosteditApproved: &no}
		}
		return extsdk.PredictResult{}
	}}
	mq := extStepJob("fix-and-ship", "fix-and-check")

	svc, _ := newExtStepService(mq, pred)
	if _, err := svc.RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	ship := predictCallFor(t, mq, 100)
	if ship.ActiveText == nil || *ship.ActiveText != rewrite || ship.Status != db.TranslationStatusAutoApproved {
		t.Errorf("approved postedit: text %v status %s", ship.ActiveText, ship.Status)
	}
	check := predictCallFor(t, mq, 101)
	if check.ActiveText == nil || *check.ActiveText != rewrite || check.Status != db.TranslationStatusNeedsReview {
		t.Errorf("unapproved postedit: text %v status %s", check.ActiveText, check.Status)
	}
	if check.PredictOutcome.PredictOutcome != db.PredictOutcomeApeNeedsReview {
		t.Errorf("outcome = %s, want ape_needs_review", check.PredictOutcome.PredictOutcome)
	}
	// Both rows were post-edited, so each gets one history entry naming the
	// extension as the source.
	if len(mq.insertHistoryCalls) != 2 {
		t.Fatalf("history entries = %d, want 2 (one per post-edited row)", len(mq.insertHistoryCalls))
	}
	for _, h := range mq.insertHistoryCalls {
		var details map[string]any
		if err := json.Unmarshal(h.Details, &details); err != nil {
			t.Fatalf("history details: %v", err)
		}
		if details["source"] != "ext:mockqe" {
			t.Errorf("history source = %v, want ext:mockqe", details["source"])
		}
	}
}

// Two keys can belong to the same extension (say, two vendor accounts). The
// step runs with the key on the engine ROW, never another key of the same
// extension.
func TestRunAIStep_UsesTheEngineRowsKey(t *testing.T) {
	mq := extStepJob("good")
	rowKey := int64(4242)
	mq.setAIEngineRowToken(901, &rowKey)

	other := &fakeExtPredictor{name: "other account"}
	onRow := &fakeExtPredictor{name: "row account"}
	exts := &fakeExtensions{byToken: map[int64]extension.BatchPredictor{
		extTokenID(1): other,
		rowKey:        onRow,
	}}
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil).SetAIProvider(nil, nil, identityDecryptor{})
	svc.SetExtensions(exts)

	if _, err := svc.RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if len(exts.requested) != 1 || exts.requested[0] != [2]int64{1, rowKey} {
		t.Fatalf("PredictorForToken calls = %v, want one for tenant 1, key %d", exts.requested, rowKey)
	}
	if len(onRow.calls) != 1 || len(other.calls) != 0 {
		t.Errorf("row key batches = %d, other key batches = %d; want 1 and 0", len(onRow.calls), len(other.calls))
	}
}

// An engine whose extension was removed fails the job with a reason the AI
// settings page can name, and touches nothing.
func TestRunAIStep_RemovedExtensionFailsTheJob(t *testing.T) {
	mq := extStepJob("good")
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil).SetAIProvider(nil, nil, identityDecryptor{})
	svc.SetExtensions(&fakeExtensions{byToken: map[int64]extension.BatchPredictor{}})

	_, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	var unusable *EngineUnusableError
	if !errors.As(err, &unusable) {
		t.Fatalf("a removed extension must fail the job, got err=%v", err)
	}
	if unusable.Reason != enginehealth.NoExtension {
		t.Errorf("Reason = %q, want %q", unusable.Reason, enginehealth.NoExtension)
	}
	if len(mq.updatePredictCalls) != 0 {
		t.Errorf("a removed extension may not touch a segment, got %d updates", len(mq.updatePredictCalls))
	}
}

// A key that cannot be decrypted (the secrets key changed) fails the job with
// a message the user can act on, not an internal error.
func TestRunAIStep_UnreadableExtensionKeyIsAClientError(t *testing.T) {
	mq := extStepJob("good")
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil).SetAIProvider(nil, nil, identityDecryptor{})
	svc.SetExtensions(&fakeExtensions{err: fmt.Errorf("decrypt key 7: %w", extension.ErrKeyUnreadable)})

	_, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	msg, ok := httputil.AsClientError(err)
	if !ok || !strings.Contains(msg, "cannot be read") {
		t.Fatalf("err = %v, want a client error saying the key cannot be read", err)
	}
	if !IsPermanent(err) {
		t.Error("an unreadable key is not fixed by a retry; the error must be permanent")
	}
	if len(mq.updatePredictCalls) != 0 {
		t.Errorf("got %d segment updates, want none", len(mq.updatePredictCalls))
	}
}

// Every batch tells the extension which file, project, step and batch it
// belongs to, so a vendor can group the predictions.
func TestRunAIStep_ExtensionGetsBatchMetadata(t *testing.T) {
	mq := extStepJob("a", "b", "c")
	pred := &fakeExtPredictor{batch: 2}
	svc, _ := newExtStepService(mq, pred)
	if _, err := svc.RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatal(err)
	}
	want := []extsdk.PredictMeta{
		{FileID: "1", FileName: "file_1.json", ProjectID: "10", ProjectName: "project_10", StepName: "Review", BatchIndex: 0, BatchCount: 2},
		{FileID: "1", FileName: "file_1.json", ProjectID: "10", ProjectName: "project_10", StepName: "Review", BatchIndex: 1, BatchCount: 2},
	}
	_, metas := pred.inBatchOrder()
	if len(metas) != len(want) {
		t.Fatalf("got %d batches, want %d", len(metas), len(want))
	}
	for i := range want {
		if metas[i] != want[i] {
			t.Errorf("batch %d meta = %+v, want %+v", i, metas[i], want[i])
		}
	}
}

// A vendor 4xx (other than 429) is a request the vendor will refuse again,
// so the job fails without retries; 429 and 5xx may pass on a later try.
func TestRunAIStep_VendorClientErrorIsNotRetried(t *testing.T) {
	for _, tc := range []struct {
		status    int
		permanent bool
	}{{400, true}, {401, true}, {422, true}, {429, false}, {503, false}, {0, false}} {
		mq := extStepJob("good")
		pred := &fakeExtPredictor{err: &extension.CallError{Msg: "refused", VendorStatus: tc.status}}
		svc, _ := newExtStepService(mq, pred)
		_, err := svc.RunAIStep(context.Background(), 1, 5, nil)
		if err == nil {
			t.Fatalf("status %d: want an error", tc.status)
		}
		if got := IsPermanent(err); got != tc.permanent {
			t.Errorf("status %d: permanent = %v, want %v (err: %v)", tc.status, got, tc.permanent, err)
		}
		if _, ok := httputil.AsClientError(err); !ok {
			t.Errorf("status %d: the vendor's message must reach the user as a client error, got %v", tc.status, err)
		}
	}
}

// Batches are cut at the extension's own batch size.
func TestRunAIStep_BatchSizeFromExtension(t *testing.T) {
	pred := &fakeExtPredictor{batch: 2}
	mq := extStepJob("a", "b", "c", "d", "e")

	svc, _ := newExtStepService(mq, pred)
	res, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	var sizes []int
	calls, _ := pred.inBatchOrder()
	for _, c := range calls {
		sizes = append(sizes, len(c))
	}
	if len(sizes) != 3 || sizes[0] != 2 || sizes[1] != 2 || sizes[2] != 1 {
		t.Errorf("batch sizes = %v, want [2 2 1]", sizes)
	}
	if res.AutoApproved != 5 {
		t.Errorf("AutoApproved = %d, want 5", res.AutoApproved)
	}
}

// The extension learns where each target came from, and its score and
// comment land on the segment.
func TestRunAIStep_ExtensionRowOriginScoreAndComment(t *testing.T) {
	score, comment := 87, "Terminology looks off."
	pred := &fakeExtPredictor{verdict: func(r extsdk.PredictRow) extsdk.PredictResult {
		return extsdk.PredictResult{Score: &score, Comment: &comment}
	}}
	mq := extStepJob("from tm", "from mt")
	mq.preTranslated[1][0].Origin = db.NullTranslationOrigin{TranslationOrigin: db.TranslationOriginTm, Valid: true}

	svc, _ := newExtStepService(mq, pred)
	if _, err := svc.RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	sent := pred.calls[0]
	if sent[0].Origin != "tm" || sent[1].Origin != "mt" {
		t.Errorf("origins sent = %q, %q; want tm, mt", sent[0].Origin, sent[1].Origin)
	}
	if note := predictCallFor(t, mq, 100).QeNote; note == nil || *note != "87" {
		t.Errorf("qe_note = %v, want 87", note)
	}
	if len(mq.createAICommentCalls) != 2 || mq.createAICommentCalls[0].Content != comment {
		t.Errorf("AI comments = %+v, want the extension's comment on both rows", mq.createAICommentCalls)
	}
}
