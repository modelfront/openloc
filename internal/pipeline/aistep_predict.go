package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/timing"
)

// aiPredictMaxInFlight is how many provider batches one QE+APE step sends at
// once. Both backends make one independent HTTP round trip per batch, so a
// large file finishes in a fraction of the sequential time while one job
// still sends a provider only a few requests at a time.
const aiPredictMaxInFlight = 4

// batchPrediction is one batch's provider answer.
type batchPrediction struct {
	results []aiPredictResult
	err     error
}

// predictions runs a step's provider batches in the background, a bounded
// number at a time, in batch order.
type predictions struct {
	outs   []chan batchPrediction
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// halted stops new batches from being sent; those in flight finish.
	halted atomic.Bool
}

// errHalted answers a batch that was not sent because the run stopped.
var errHalted = errors.New("qe/ape: batch not sent")

// predictConcurrently starts runPredict on every batchSize slice of pending,
// at most limit at a time. Collect the answers in order with wait; stop
// cancels the batches still running and waits for them.
func predictConcurrently(ctx context.Context, runPredict aiPredictRunner, pending []db.ListPreTranslatedByFileRow, batchSize, limit int, stage string, meta []timing.Option) *predictions {
	total := (len(pending) + batchSize - 1) / batchSize
	p := &predictions{outs: make([]chan batchPrediction, total)}
	for i := range p.outs {
		p.outs[i] = make(chan batchPrediction, 1)
	}
	p.ctx, p.cancel = context.WithCancel(ctx)

	var g errgroup.Group
	g.SetLimit(limit)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer g.Wait() //nolint:errcheck // every batch reports through its channel
		for i := range total {
			if p.ctx.Err() != nil || p.halted.Load() {
				return
			}
			batch := pending[i*batchSize : min((i+1)*batchSize, len(pending))]
			g.Go(func() error {
				if err := p.ctx.Err(); err != nil {
					p.outs[i] <- batchPrediction{err: err}
					return nil
				}
				if p.halted.Load() {
					p.outs[i] <- batchPrediction{err: errHalted}
					return nil
				}
				// n = provider round trips (one batch each), the unit that costs latency.
				predictCtx, done := timing.Start(p.ctx, stage, meta...)
				results, err := runPredict(predictCtx, batch, i, total)
				done(err)
				p.outs[i] <- batchPrediction{results: results, err: err}
				return nil
			})
		}
	}()
	return p
}

// wait returns batch i's answer, or the context's error once it is done.
func (p *predictions) wait(i int) ([]aiPredictResult, error) {
	select {
	case out := <-p.outs[i]:
		return out.results, out.err
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	}
}

// halt sends no more batches and waits for those in flight; read their
// answers with received.
func (p *predictions) halt() {
	p.halted.Store(true)
	p.wg.Wait()
}

// received returns batch i's answer after halt, ok only when the batch was
// sent and answered without error.
func (p *predictions) received(i int) ([]aiPredictResult, bool) {
	select {
	case out := <-p.outs[i]:
		return out.results, out.err == nil
	default:
		return nil, false
	}
}

func (p *predictions) stop() {
	p.cancel()
	p.wg.Wait()
}
