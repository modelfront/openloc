package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
)

// aiStepFixture: 35 pre-translated rows (two 30-row batches) against a fake
// APE extension that approves everything and records each batch.
func aiStepFixture(t *testing.T) (*mockQuerier, *fakeExtPredictor) {
	t.Helper()
	pred := &fakeExtPredictor{ape: true, batch: 30}
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")
	rows := make([]db.ListPreTranslatedByFileRow, 35)
	for i := range rows {
		mt := "MT " + string(rune('a'+i%26))
		rows[i] = db.ListPreTranslatedByFileRow{ID: int64(100 + i), SegmentID: int64(i + 1), Position: int32(i), TargetLanguage: "de", SourceText: "Hello", ActiveText: &mt}
	}
	mq.preTranslated[1] = rows
	return mq, pred
}

type progressReport struct {
	phase       string
	done, total int
}

// Progress is reported once per provider batch under the QE+APE phase — the
// literal the editor lock keys on.
func TestRunAIStep_ReportsProgressPerBatch(t *testing.T) {
	mq, pred := aiStepFixture(t)
	svc, _ := newExtStepService(mq, pred)

	var reports []progressReport
	_, err := svc.RunAIStep(context.Background(), 1, 5, func(phase string, done, total int) {
		reports = append(reports, progressReport{phase, done, total})
	})
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	// The label carries the mode the step is actually running, not the phase
	// key: PhaseQEAPE names the phase for every mode.
	label := AIStepLabel("qe_ape")
	want := []progressReport{{label, 30, 35}, {label, 35, 35}}
	if len(reports) != len(want) {
		t.Fatalf("progress reports = %+v, want %+v", reports, want)
	}
	for i := range want {
		if reports[i] != want[i] {
			t.Errorf("report %d = %+v, want %+v", i, reports[i], want[i])
		}
	}
	if len(pred.calls) != 2 {
		t.Errorf("expected 2 provider calls, got %d", len(pred.calls))
	}
}

// A cancellation between batches stops the run before the next batch is
// written and before the remaining batches are sent: of seven batches, only
// those in flight at the stop (plus at most one that took the first batch's
// slot) reach the provider, and the finished batch's writes stay.
func TestRunAIStep_StopsBetweenBatchesWhenCancelled(t *testing.T) {
	const batches = aiPredictMaxInFlight + 3
	mq := newMockQuerier()
	aiStepRows(mq, 30*batches)
	slow := map[int]time.Duration{}
	for i := 1; i < batches; i++ {
		slow[i] = 200 * time.Millisecond
	}
	pred := &barrierPredictor{fakeExtPredictor: &fakeExtPredictor{ape: true, batch: 30}, delay: slow}
	svc, _ := newExtStepService(mq, pred)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := svc.RunAIStep(ctx, 1, 5, func(string, int, int) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if n := len(pred.calls); n > aiPredictMaxInFlight+1 {
		t.Errorf("%d of %d batches sent, want at most the %d in flight at the stop", n, batches, aiPredictMaxInFlight+1)
	}
	if len(mq.updatePredictCalls) != 30 {
		t.Errorf("expected the first batch's 30 rows written, got %d", len(mq.updatePredictCalls))
	}
}
