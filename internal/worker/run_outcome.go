package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
)

// runSoFar is what the earlier attempts of a retried pre-translation filled.
// asynq retries a failed attempt and the retry does only the work left, so the
// attempt that finishes adds this to its own outcome and the History reports
// the whole run. It is stored as JSON in job_run_outcomes; the tags are that
// stored format.
type runSoFar struct {
	// Run is the task's enqueue time: the same in every attempt of one run
	// and new for each run, so a row an older run left is never read.
	Run time.Time `json:"run"`
	// TMDone: an earlier attempt finished the TM phase, so a retry skips it
	// and these TM fields are the run's.
	TMDone       bool     `json:"tm_done"`
	TMSkipReason string   `json:"tm_skip_reason,omitempty"`
	TMExact      int      `json:"tm_exact"`
	TMFuzzy      int      `json:"tm_fuzzy"`
	TMNames      []string `json:"tm_names,omitempty"`
	MTTranslated int      `json:"mt_translated"`
	MTEngine     string   `json:"mt_engine,omitempty"`
}

// isRetry reports whether this delivery is a retry of a failed attempt. A var
// so tests can force it: asynq builds the retry-count context internally.
var isRetry = func(ctx context.Context) bool {
	n, ok := asynq.GetRetryCount(ctx)
	return ok && n > 0
}

// phases is what this attempt runs: everything asked for, minus a TM phase an
// earlier attempt finished. MT fills only empty rows, so it picks up where the
// failed attempt stopped.
func (r runSoFar) phases(asked pipeline.TranslatePhases) pipeline.TranslatePhases {
	if r.TMDone {
		asked.UseTM = false
	}
	return asked
}

// then is the whole run's outcome once this attempt produced o.
func (r runSoFar) then(o pipeline.TranslateOutcome) pipeline.TranslateOutcome {
	if r.TMDone {
		o.TMSkipReason = pipeline.PhaseSkipReason(r.TMSkipReason)
		o.TMExact, o.TMFuzzy, o.TMNames = r.TMExact, r.TMFuzzy, r.TMNames
	}
	if r.MTTranslated > 0 {
		// MT ran in this run, so a retry with nothing left to translate is
		// not a skipped phase. Any other reason (no route, no provider) still
		// leaves segments empty and stands.
		if o.MTSkipReason == pipeline.PhaseSkipNoSegments {
			o.MTSkipReason = ""
		}
		o.MTTranslated += r.MTTranslated
		if o.MTEngine == "" {
			o.MTEngine = r.MTEngine
		}
	}
	return o
}

// after is what the run has filled once this attempt produced o and failed
// with err. An MT failure means the TM phase before it finished.
func (r runSoFar) after(asked pipeline.TranslatePhases, o pipeline.TranslateOutcome, err error) runSoFar {
	run := r.then(o)
	next := runSoFar{
		Run:          r.Run,
		TMDone:       r.TMDone || (asked.UseTM && pipeline.IsMTFailure(err)),
		MTTranslated: run.MTTranslated,
		MTEngine:     run.MTEngine,
	}
	if next.TMDone {
		next.TMSkipReason = string(run.TMSkipReason)
		next.TMExact, next.TMFuzzy, next.TMNames = run.TMExact, run.TMFuzzy, run.TMNames
	}
	return next
}

// loadRunSoFar returns what the earlier attempts of this run filled. A first
// attempt starts a new run, so it drops whatever an earlier run left; a row
// from another run is ignored. A payload with no enqueue time cannot tell its
// run from an older one, so it keeps nothing. A failed read only costs the
// History its earlier counts: it is logged, and the attempt runs as a fresh
// one.
func (h *Handler) loadRunSoFar(ctx context.Context, p PipelinePayload) runSoFar {
	fresh := runSoFar{Run: p.EnqueuedAt}
	if p.JobID == 0 || h.queries == nil || p.EnqueuedAt.IsZero() {
		return fresh
	}
	if !isRetry(ctx) {
		h.forgetRunSoFar(p)
		return fresh
	}
	raw, err := h.queries.GetJobRunOutcome(ctx, db.GetJobRunOutcomeParams{JobID: p.JobID, TenantID: p.TenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return fresh
	}
	if err != nil {
		logging.Warn(ctx, err, "failed to read the earlier attempts of a run", "job-run-outcome-read-failed", logging.JobID(p.JobID))
		return fresh
	}
	var r runSoFar
	if err := json.Unmarshal(raw, &r); err != nil {
		logging.Warn(ctx, err, "failed to decode the earlier attempts of a run", "job-run-outcome-decode-failed", logging.JobID(p.JobID))
		return fresh
	}
	if !r.Run.Equal(p.EnqueuedAt) {
		return fresh
	}
	return r
}

// saveRunSoFar keeps r for the retry, on a fresh context: the task context is
// usually done by now.
func (h *Handler) saveRunSoFar(p PipelinePayload, r runSoFar) {
	if p.JobID == 0 || h.queries == nil || r.Run.IsZero() {
		return
	}
	dbCtx, cancel := freshDBContext()
	defer cancel()
	raw, err := json.Marshal(r)
	if err == nil {
		err = h.queries.SaveJobRunOutcome(dbCtx, db.SaveJobRunOutcomeParams{JobID: p.JobID, TenantID: p.TenantID, Outcome: raw})
	}
	if err != nil {
		logging.Warn(dbCtx, err, "failed to save a failed attempt's outcome", "job-run-outcome-write-failed", logging.JobID(p.JobID))
	}
}

// forgetRunSoFar drops the saved outcome once the run is over or a new one
// starts.
func (h *Handler) forgetRunSoFar(p PipelinePayload) {
	if p.JobID == 0 || h.queries == nil {
		return
	}
	dbCtx, cancel := freshDBContext()
	defer cancel()
	if err := h.queries.DeleteJobRunOutcome(dbCtx, db.DeleteJobRunOutcomeParams{JobID: p.JobID, TenantID: p.TenantID}); err != nil {
		logging.Warn(dbCtx, err, "failed to drop a run's saved outcome", "job-run-outcome-delete-failed", logging.JobID(p.JobID))
	}
}

// translateFailed is the failure tail of a pre-translation attempt: a stopped
// run ends (finishStopped drops the saved outcome), an attempt asynq retries
// saves what the run has filled, and a final failure drops it and fails the
// job.
func (h *Handler) translateFailed(ctx context.Context, p PipelinePayload, prior runSoFar, o pipeline.TranslateOutcome, err error) error {
	phase := pipeline.PhaseNoun(p.Phases)
	if st, ok := pipeline.StopRequested(h.queries, p.JobID, err); ok {
		h.finishStopped(ctx, p, p.FileID, phase, st)
		return nil
	}
	if willRetry(ctx, err) {
		h.saveRunSoFar(p, prior.after(p.Phases, o, err))
	} else {
		h.forgetRunSoFar(p)
	}
	return h.failRun(ctx, p, p.FileID, phase, err)
}
