package hold

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// TaskSweep is the asynq task type of the sweep of expired holds.
// Producer: asynq.Scheduler in cmd/server/main.go. Consumer: Sweeper.HandleSweep.
// An expired hold already blocks nothing; the sweep keeps the table small.
const TaskSweep = "scheduler:segment-hold-sweep"

// NewSweepTask returns the task the scheduler enqueues. No payload.
func NewSweepTask() *asynq.Task { return asynq.NewTask(TaskSweep, nil) }

type sweepQuerier interface {
	DeleteExpiredHolds(ctx context.Context) (int64, error)
}

var _ sweepQuerier = (db.Querier)(nil)

// Sweeper deletes expired holds.
type Sweeper struct{ q sweepQuerier }

// NewSweeper returns a Sweeper over the given queries.
func NewSweeper(q sweepQuerier) *Sweeper { return &Sweeper{q: q} }

// RegisterHandlers wires the sweep onto the shared asynq mux.
func (s *Sweeper) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskSweep, s.HandleSweep)
}

// HandleSweep is the asynq handler. A failure is returned so asynq retries.
func (s *Sweeper) HandleSweep(ctx context.Context, _ *asynq.Task) error {
	n, err := s.q.DeleteExpiredHolds(ctx)
	if err != nil {
		logging.Warn(ctx, err, "segment hold sweep failed", "segment-hold-sweep-failed",
			logging.TaskType(TaskSweep))
		return fmt.Errorf("delete expired segment holds: %w", err)
	}
	logging.Info(ctx, "expired segment holds deleted", "segment-hold-swept",
		logging.TaskType(TaskSweep), logging.Count(int(n)))
	return nil
}
