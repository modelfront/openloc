package llmdebug

// Retention for the persisted LLM debug log.
//
// Rotation is free because the date IS the object key: Store writes under
// llm-debug/<YYYY-MM-DD>/ and this deletes whole date prefixes once they age
// out. There is no read-modify-write anywhere, which is exactly why retention
// is time-based rather than a "last N requests" cap.
//
// The asynq task type lives in this package rather than internal/worker,
// following the convention that a scheduled task belongs to its own
// domain (see internal/notification/scheduled.go). It is registered @daily by
// cmd/server/main.go under RUN_SCHEDULER, which is true on exactly one replica
// — so two pods never prune against each other.

import (
	"context"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"openloc/internal/logging"
)

// TaskDebugLogPrune is the asynq task type for the daily retention sweep.
// Producer: asynq.Scheduler in cmd/server/main.go. Consumer: Pruner.HandlePrune.
const TaskDebugLogPrune = "scheduler:llm-debug-prune"

// NewPruneTask returns the task the scheduler enqueues daily. No payload — the
// Pruner holds its own configuration.
func NewPruneTask() *asynq.Task {
	return asynq.NewTask(TaskDebugLogPrune, nil)
}

// DefaultRetainDays keeps today and yesterday. Deleting everything older than
// 24h would discard this morning's log the moment the clock passed midnight.
const DefaultRetainDays = 1

// pruneStore is the storage seam for retention — list + delete, nothing else.
// *file.Storage satisfies it.
type pruneStore interface {
	ListKeys(ctx context.Context, prefix string) ([]string, error)
	DeleteKeys(ctx context.Context, keys []string) error
}

// PrunerOpts configures a Pruner; the zero value is usable.
type PrunerOpts struct {
	// KeyPrefix defaults to DefaultKeyPrefix. Must match the Store's.
	KeyPrefix string
	// RetainDays is how many days BEFORE today to keep. Negative is clamped to
	// 0. Defaults to DefaultRetainDays when zero-valued via NewPruner's caller
	// (an explicit 0 means "keep only today").
	RetainDays int
	// Now is injectable for tests.
	Now func() time.Time
}

// Pruner deletes aged-out debug objects.
type Pruner struct {
	store pruneStore
	opts  PrunerOpts
}

// NewPruner returns a Pruner. RetainDays is taken as given (including 0) so a
// caller can express "keep only today"; the config layer supplies the default.
func NewPruner(store pruneStore, opts PrunerOpts) *Pruner {
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = DefaultKeyPrefix
	}
	if opts.RetainDays < 0 {
		opts.RetainDays = 0
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Pruner{store: store, opts: opts}
}

// Prune deletes every object whose date prefix is older than the retention
// window and returns how many were removed.
func (p *Pruner) Prune(ctx context.Context) (int, error) {
	keys, err := p.store.ListKeys(ctx, p.opts.KeyPrefix+"/")
	if err != nil {
		return 0, err
	}
	stale := staleKeys(keys, p.opts.KeyPrefix, p.opts.Now().UTC(), p.opts.RetainDays)
	if len(stale) == 0 {
		return 0, nil
	}
	if err := p.store.DeleteKeys(ctx, stale); err != nil {
		return 0, err
	}
	return len(stale), nil
}

// RegisterHandlers wires the prune task onto the shared asynq mux, alongside
// worker.Handler and notification.ScheduledHandler.
func (p *Pruner) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskDebugLogPrune, p.HandlePrune)
}

// HandlePrune is the asynq handler. A failure is returned so asynq retries;
// losing one day's sweep just means the objects go on the next one.
func (p *Pruner) HandlePrune(ctx context.Context, _ *asynq.Task) error {
	deleted, err := p.Prune(ctx)
	if err != nil {
		logging.Warn(ctx, err, "llm debug log prune failed", "llm-debug-prune-failed",
			logging.TaskType(TaskDebugLogPrune))
		return err
	}
	logging.Info(ctx, "llm debug log pruned", "llm-debug-pruned",
		logging.TaskType(TaskDebugLogPrune),
		logging.Debugf("deleted_objects", deleted),
		logging.Debugf("retain_days", p.opts.RetainDays))
	return nil
}

// staleKeys returns the keys under prefix whose date component is strictly
// older than (today - retainDays).
//
// A key it cannot confidently date is LEFT ALONE — an unparseable prefix might
// not be ours, and the cost of skipping is a stale object while the cost of
// guessing is deleting someone else's data. Same reasoning applies to
// future-dated keys (clock skew between pods): they're simply not stale yet.
func staleKeys(keys []string, prefix string, today time.Time, retainDays int) []string {
	if retainDays < 0 {
		retainDays = 0
	}
	// Truncate to a date before subtracting so the comparison is date-vs-date
	// and doesn't depend on what time of day the sweep happens to run.
	cutoff := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, -retainDays)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		rest, ok := strings.CutPrefix(k, prefix+"/")
		if !ok {
			continue
		}
		datePart, _, ok := strings.Cut(rest, "/")
		if !ok {
			continue // no date component (e.g. a stray file at the prefix root)
		}
		d, err := time.ParseInLocation(dateLayout, datePart, time.UTC)
		if err != nil {
			continue // not a date we wrote
		}
		if d.Before(cutoff) {
			out = append(out, k)
		}
	}
	return out
}
