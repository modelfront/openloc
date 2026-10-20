package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/pkg/extsdk"
)

// barrierPredictor holds each batch until a second one is in flight (or a
// second passes), so it sees how many batches the step runs at once. Batch
// delay[i] then sleeps before answering, to finish batches out of order.
type barrierPredictor struct {
	*fakeExtPredictor
	delay map[int]time.Duration
	fail  map[int]error

	mu       sync.Mutex
	inFlight int
	max      int
}

func (b *barrierPredictor) Predict(ctx context.Context, src, tgt string, meta extsdk.PredictMeta, rows []extsdk.PredictRow) (extsdk.PredictResponse, error) {
	b.mu.Lock()
	b.inFlight++
	b.max = max(b.max, b.inFlight)
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.inFlight--
		b.mu.Unlock()
	}()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		b.mu.Lock()
		reached := b.max >= 2
		b.mu.Unlock()
		if reached {
			break
		}
	}
	time.Sleep(b.delay[meta.BatchIndex])
	if err := b.fail[meta.BatchIndex]; err != nil {
		return extsdk.PredictResponse{}, err
	}
	return b.fakeExtPredictor.Predict(ctx, src, tgt, meta, rows)
}

// aiStepRows seeds n pre-translated de rows with ids 100.. in file order.
func aiStepRows(mq *mockQuerier, n int) {
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")
	rows := make([]db.ListPreTranslatedByFileRow, n)
	for i := range rows {
		mt := "MT"
		rows[i] = db.ListPreTranslatedByFileRow{ID: int64(100 + i), SegmentID: int64(i + 1), Position: int32(i), TargetLanguage: "de", SourceText: "Hello", ActiveText: &mt}
	}
	mq.preTranslated[1] = rows
}

// Provider batches run concurrently, and their results are still written in
// file order even when a later batch answers first.
func TestRunAIStep_RunsBatchesConcurrentlyAndWritesInOrder(t *testing.T) {
	mq := newMockQuerier()
	aiStepRows(mq, 90)
	pred := &barrierPredictor{
		fakeExtPredictor: &fakeExtPredictor{ape: true, batch: 30},
		delay:            map[int]time.Duration{0: 50 * time.Millisecond},
	}
	svc, _ := newExtStepService(mq, pred)

	res, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if pred.max < 2 {
		t.Errorf("at most %d provider batch in flight, want at least 2", pred.max)
	}
	if res.AutoApproved != 90 || len(mq.updatePredictCalls) != 90 {
		t.Fatalf("wrote %d rows (%d approved), want 90", len(mq.updatePredictCalls), res.AutoApproved)
	}
	for i, c := range mq.updatePredictCalls {
		if c.ID != int64(100+i) {
			t.Fatalf("write %d is row %d, want %d: results must be written in file order", i, c.ID, 100+i)
		}
	}
}

// A failed batch fails the step, but the answers of the batches already sent
// are written first, so a retry does not pay for them again; the failed
// batch's rows are not written.
func TestRunAIStep_FailedBatchKeepsTheAnswersInFlight(t *testing.T) {
	mq := newMockQuerier()
	aiStepRows(mq, 120)
	boom := errors.New("vendor down")
	pred := &barrierPredictor{
		fakeExtPredictor: &fakeExtPredictor{ape: true, batch: 30},
		delay:            map[int]time.Duration{0: 100 * time.Millisecond},
		fail:             map[int]error{0: boom},
	}
	svc, _ := newExtStepService(mq, pred)

	_, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	if err == nil {
		t.Fatal("want the failed batch's error")
	}
	written := map[int64]bool{}
	for _, c := range mq.updatePredictCalls {
		written[c.ID] = true
	}
	if len(written) != 90 {
		t.Errorf("wrote %d rows, want the 90 of the three batches that answered", len(written))
	}
	for id := int64(100); id < 130; id++ {
		if written[id] {
			t.Fatalf("row %d of the failed batch was written", id)
		}
	}
}
