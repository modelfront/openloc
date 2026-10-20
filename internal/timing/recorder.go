package timing

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// flushTimeout bounds the write on the way out. Flush usually runs from a
// deferred call on a task context that's already cancelled (a failed job) or
// about to be, so it detaches from cancellation and needs its own deadline.
const flushTimeout = 10 * time.Second

// Stage is one stage's accumulated cost.
//
// SelfMS vs TotalMS is the whole point of the design — see the package doc.
// Spans nest (mt.tag_transfer runs inside mt.db_save; every task.* span wraps
// the entire pipeline), so a single duration per stage would count the same
// milliseconds under several names. TotalMS is inclusive, SelfMS excludes
// whatever the span's children already claimed, and only SelfMS may be summed.
//
// Field tags are load-bearing: they're the object keys merge_stage_timings()
// reads in migration 086, so renaming one silently stops the merge from
// finding the old value.
type Stage struct {
	N       int64             `json:"n"`
	SelfMS  int64             `json:"self_ms"`
	TotalMS int64             `json:"total_ms"`
	MaxMS   int64             `json:"max_ms"`
	Err     int64             `json:"err,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// span is one open measurement and its position in the tree. Children report
// their inclusive time up to childMS on close, which is what lets the parent
// compute its own exclusive time.
type span struct {
	parent  *span
	started time.Time

	mu      sync.Mutex
	childMS int64
}

// addChild accumulates a direct child's inclusive time. Guarded because a
// parent can fan out concurrently (spellcheck's CheckBatch is the live case).
func (s *span) addChild(ms int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.childMS += ms
	s.mu.Unlock()
}

func (s *span) children() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.childMS
}

// Recorder accumulates stage timings for one unit of work — one asynq task, or
// one upload handler run. Safe for concurrent spans.
//
// A Recorder is scoped to a task, not to a job. The fan-out case is deliberate:
// an upload records its ingest stages once and then flushes the same Recorder
// once per job the upload created, copying those timings into every job's row.
type Recorder struct {
	mu        sync.Mutex
	stages    map[string]*Stage
	queueWait time.Duration
	startedAt time.Time
}

// New returns a Recorder that starts counting wall-clock now.
func New() *Recorder {
	return &Recorder{stages: make(map[string]*Stage), startedAt: time.Now()}
}

// Option customises a span. Currently only Meta.
type Option func(*Stage)

// Meta tags a stage with a dimension worth keeping — which MT engine ran, which
// model answered, whether the parse went through the Python sidecar. Merged
// last-write-wins, so a job re-run on a different engine reports the engine
// that ran most recently.
func Meta(key, value string) Option {
	return func(s *Stage) {
		if s.Meta == nil {
			s.Meta = make(map[string]string, 1)
		}
		s.Meta[key] = value
	}
}

type recorderKey struct{}
type spanKey struct{}

// WithRecorder installs a fresh Recorder on ctx and returns both. Call it once
// per unit of work; every Start below finds it through the context.
func WithRecorder(ctx context.Context) (context.Context, *Recorder) {
	r := New()
	return context.WithValue(ctx, recorderKey{}, r), r
}

// FromContext returns the Recorder installed on ctx, or nil. Callers that just
// want to time something should use Start, which handles the nil case.
func FromContext(ctx context.Context) *Recorder {
	r, _ := ctx.Value(recorderKey{}).(*Recorder)
	return r
}

// noop is returned by Start when there's no recorder, so instrumented code
// stays callable from paths that don't track timings (HTTP handlers, tests).
func noop(error) {}

// Start opens a span for stage and returns a CHILD CONTEXT plus the span's
// closer. Pass the operation's error to the closer — nil on success — so
// failures land in the stage's err counter instead of silently inflating the
// average:
//
//	ctx, done := timing.Start(ctx, timing.StageMTEngineCall, timing.Meta("engine", name))
//	results, err := engine.Translate(ctx, texts)
//	done(err)
//
// Thread the returned context into the work being measured. That is what makes
// nested spans attribute correctly: a span started from this context is
// recorded as a child, and its time is subtracted from this span's self time.
// Passing the ORIGINAL context to the measured work instead makes any nested
// span a sibling, which double-counts — the exact bug this design removes. Even
// for a span with no children today, thread it: the cost is nothing and it
// keeps the guarantee if one is added later.
//
// The closer is safe to call from a defer, and safe to call when Start found no
// recorder.
func Start(ctx context.Context, stage string, opts ...Option) (context.Context, func(error)) {
	r := FromContext(ctx)
	if r == nil {
		return ctx, noop
	}

	parent, _ := ctx.Value(spanKey{}).(*span)
	sp := &span{parent: parent, started: time.Now()}
	childCtx := context.WithValue(ctx, spanKey{}, sp)

	var once sync.Once
	return childCtx, func(err error) {
		once.Do(func() {
			total := time.Since(sp.started)
			totalMS := total.Milliseconds()

			// Exclusive time. Clamps at zero: children that ran concurrently
			// can sum past their parent's wall clock, and a negative self
			// would corrupt the partition.
			selfMS := totalMS - sp.children()
			if selfMS < 0 {
				selfMS = 0
			}

			r.record(stage, selfMS, totalMS, err, opts...)
			sp.parent.addChild(totalMS)

			// Per-occurrence drill-down. Debug because the loops are chatty —
			// ~20 MT chunks and ~34 predict batches on a 1000-segment job —
			// and the flush summary already carries the aggregate at Info.
			// Failures always surface.
			attrs := []slog.Attr{logging.Duration(total), logging.Label("stage", stage)}
			if err != nil {
				logging.Warn(ctx, err, "stage failed", "timing-stage-failed", attrs...)
				return
			}
			logging.Debug(ctx, "stage complete", "timing-stage", attrs...)
		})
	}
}

// Record adds one LEAF observation directly, for callers that already measured
// the duration themselves or that aren't on a context-carrying path (the seed
// replaying a synthetic profile, tests). Self and total are equal — a caller
// supplying its own duration has no children to subtract.
func (r *Recorder) Record(stage string, d time.Duration, err error, opts ...Option) {
	if r == nil {
		return
	}
	ms := d.Milliseconds()
	r.record(stage, ms, ms, err, opts...)
}

func (r *Recorder) record(stage string, selfMS, totalMS int64, err error, opts ...Option) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := r.stages[stage]
	if s == nil {
		s = &Stage{}
		r.stages[stage] = s
	}
	s.N++
	s.SelfMS += selfMS
	s.TotalMS += totalMS
	if totalMS > s.MaxMS {
		s.MaxMS = totalMS
	}
	if err != nil {
		s.Err++
	}
	for _, o := range opts {
		o(s)
	}
}

// SetQueueWait records how long the task sat in Redis between enqueue and
// pickup. Set by the asynq handlers from the task's enqueue timestamp; it isn't
// a stage because nothing was executing.
func (r *Recorder) SetQueueWait(d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queueWait = d
}

// Stages returns a copy of the accumulated map. Tests and the flush path use
// it; callers must not mutate the Recorder's internals.
func (r *Recorder) Stages() map[string]Stage {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make(map[string]Stage, len(r.stages))
	for k, v := range r.stages {
		s := *v
		if v.Meta != nil {
			s.Meta = make(map[string]string, len(v.Meta))
			for mk, mv := range v.Meta {
				s.Meta[mk] = mv
			}
		}
		out[k] = s
	}
	return out
}

// Store is the slice of the generated querier that Flush needs. Narrow so tests
// can assert on what got written without a database.
type Store interface {
	UpsertJobTiming(ctx context.Context, arg db.UpsertJobTimingParams) error
}

// Flush merges everything recorded so far into the job's job_timings row and
// emits the summary log line. Designed for `defer`: it never returns an error
// and never panics, because a timing write failing must not fail the job that
// produced it.
//
// Calling it more than once with different job IDs is the supported way to fan
// file-scoped ingest timings out across every job an upload created. Calling it
// twice for the same job double-counts — one flush per job per task.
func (r *Recorder) Flush(ctx context.Context, store Store, tenantID, jobID int64) {
	if r == nil || store == nil || jobID == 0 {
		return
	}

	r.mu.Lock()
	queueWait := r.queueWait
	startedAt := r.startedAt
	r.mu.Unlock()

	// Value copy, not r.stages: the map holds pointers, and slog would
	// render those as addresses in the summary line below.
	stages := r.Stages()
	if len(stages) == 0 && queueWait == 0 {
		return
	}

	payload, err := json.Marshal(stages)
	if err != nil {
		// Only reachable if a Meta value is somehow unmarshalable, which
		// map[string]string can't be — but swallowing it silently would
		// make a future struct change fail invisibly.
		logging.Warn(ctx, err, "encode job timings", "timing-encode-failed", logging.JobID(jobID))
		return
	}

	compute := time.Since(startedAt)

	// Detach from cancellation but keep the logging scope: Flush is usually
	// deferred off a task context that's already been cancelled by the
	// failure path it's reporting on.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flushTimeout)
	defer cancel()

	err = store.UpsertJobTiming(wctx, db.UpsertJobTimingParams{
		JobID:       jobID,
		TenantID:    tenantID,
		Stages:      payload,
		ComputeMs:   compute.Milliseconds(),
		QueueWaitMs: queueWait.Milliseconds(),
	})
	if err != nil {
		logging.Warn(wctx, err, "persist job timings", "timing-flush-failed", logging.JobID(jobID))
		return
	}

	logging.Info(wctx, "job timings recorded", "timing-flush",
		logging.JobID(jobID),
		logging.Duration(compute),
		logging.Debugf("timing_stages", stages),
	)
}
