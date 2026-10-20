package pipeline

import (
	"context"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/timing"
	"openloc/internal/translationmemory"
)

// fuzzySessionSize is how many segments the TM phase looks up in one
// session, and so with one setting of the trigram threshold.
const fuzzySessionSize = 200

// fuzzySession runs fuzzy TM lookups in a read transaction that sets the
// trigram threshold once, opened on the first lookup and renewed after a
// failed one (which ends its transaction). It holds a pool connection until
// close, so a caller does only lookups inside it and writes after close —
// otherwise a full pool waits on itself. Use withFuzzySession.
type fuzzySession struct {
	ctx     context.Context
	s       *Service
	q       db.Querier
	done    chan error
	release chan struct{}
	broken  bool
}

// withFuzzySession runs fn's lookups in one session and ends its transaction
// when fn returns.
func (s *Service) withFuzzySession(ctx context.Context, fn func(*fuzzySession)) {
	f := &fuzzySession{ctx: ctx, s: s}
	defer f.close()
	fn(f)
}

// querier returns the transaction's querier, opening one when needed. The
// transaction runs in its own goroutine until close, because TxRunner only
// hands out a querier inside its callback.
func (f *fuzzySession) querier() (db.Querier, error) {
	if f.q != nil && !f.broken {
		return f.q, nil
	}
	f.close()
	ready := make(chan error, 1)
	f.done = make(chan error, 1)
	f.release = make(chan struct{})
	var q db.Querier
	go func(release chan struct{}, done chan error) {
		started := false
		err := f.s.txr.InTx(f.ctx, func(tq db.Querier) error {
			started = true
			if err := tq.SetTrgmSimilarityThreshold(f.ctx, translationmemory.FindRecallThreshold); err != nil {
				ready <- err
				return err
			}
			q = tq
			ready <- nil
			<-release
			return nil
		})
		if !started {
			ready <- err // the transaction did not open
		}
		done <- err
	}(f.release, f.done)
	if err := <-ready; err != nil {
		<-f.done
		f.done, f.release = nil, nil
		return nil, err
	}
	f.q, f.broken = q, false
	return q, nil
}

// close ends the open transaction, if any.
func (f *fuzzySession) close() {
	if f.release == nil {
		return
	}
	close(f.release)
	if err := <-f.done; err != nil {
		logging.Warn(f.ctx, err, "fuzzy lookup transaction did not end cleanly", "pipeline-fuzzy-session-end-failed")
	}
	f.q, f.done, f.release = nil, nil, nil
}

// findBest returns the best rescored fuzzy hit for query, trying the TMs in
// their configured priority order: the first TM with a passing candidate
// wins, so TM priority beats a better score in a lower TM.
func (f *fuzzySession) findBest(tmIDs []int64, cfg tmMatchConfig, query string) (db.FuzzyLookupTMRow, float64, bool) {
	for _, tmID := range tmIDs {
		// Spans the pg_trgm recall query AND the local rescore, because the
		// rescore is strdist CPU work that a query-only timing would hide.
		// n counts candidate lookups (segments × TMs tried).
		lookupCtx, doneLookup := timing.Start(f.ctx, timing.StageTMFuzzyLookup)
		q, err := f.querier()
		var candidates []db.FuzzyLookupTMRow
		if err == nil {
			candidates, err = q.FuzzyLookupTM(lookupCtx, db.FuzzyLookupTMParams{
				TmID:           tmID,
				SourceLanguage: cfg.sourceLang,
				TargetLanguage: cfg.targetLang,
				Query:          query,
				Threshold:      translationmemory.FindRecallThreshold,
				EntryLimit:     pipelineFuzzyCandidateLimit,
			})
			if err != nil {
				// A failed statement aborts the transaction; the next lookup
				// opens a new one.
				f.broken = true
			}
		}
		if err != nil || len(candidates) == 0 {
			doneLookup(err)
			continue
		}
		best, bestScore, found := f.s.bestRescoredCandidate(query, cfg, tmID, candidates)
		doneLookup(nil)
		if found {
			return best, bestScore, true
		}
	}
	return db.FuzzyLookupTMRow{}, 0, false
}
