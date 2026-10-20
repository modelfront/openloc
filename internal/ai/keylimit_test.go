package ai

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Provider requests with one key share one limit across every run in the
// process: four step batches of 30 segments (three requests each) running at
// once never put more than llmConcurrency requests in flight for the key.
func TestPredictBatch_ConcurrentRunsShareTheKeyLimit(t *testing.T) {
	var inFlight, peak atomic.Int32
	srv := predictStub(t, func(segs []reqSegment, _ string) string {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
		return verdictReply(segs, func(reqSegment) string { return `{"approved": true, "score": 90}` })
	})
	defer srv.Close()

	cfg := predictCfg(srv.URL, ModeQE)
	cfg.APIKey = "sk-shared-limit-test"
	tr := NewHTTPTranslator()
	var wg sync.WaitGroup
	for b := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows := make([]PredictInput, 3*llmBatchSize)
			for i := range rows {
				rows[i] = PredictInput{ID: fmt.Sprintf("%d-%d", b, i), Source: "Hello", Translation: "Hallo"}
			}
			if _, err := tr.PredictBatch(context.Background(), cfg, rows); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	// 12 requests with a slow reply reach the cap, and never pass it.
	if p := peak.Load(); p != llmConcurrency {
		t.Errorf("%d requests in flight for one key at the peak, want exactly %d", p, llmConcurrency)
	}
}
