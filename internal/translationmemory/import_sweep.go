package translationmemory

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// TaskImportSweep is the asynq task type of the sweep of stale TMX imports.
// Producer: asynq.Scheduler in cmd/server/main.go. Consumer:
// ImportSweeper.HandleSweep.
const TaskImportSweep = "scheduler:tm-import-sweep"

// importStaleAfter is how long an import may go without updating its row
// before the sweep fails it. An import updates the row after every batch of
// 500 entries, which takes seconds.
const importStaleAfter = 30 * time.Minute

// staleImportMessage is what the import list shows for a swept import.
const staleImportMessage = "The import stopped before it finished, because the server restarted. The entries imported so far stay in the memory."

// NewImportSweepTask returns the task the scheduler enqueues. No payload.
func NewImportSweepTask() *asynq.Task { return asynq.NewTask(TaskImportSweep, nil) }

type importSweepQuerier interface {
	FailStaleTMImports(ctx context.Context, arg db.FailStaleTMImportsParams) (int64, error)
}

var _ importSweepQuerier = (db.Querier)(nil)

// ImportSweeper fails the imports whose process died. An import runs in the
// process that received the file, so a restart or a crash ends it without a
// word; without the sweep its row would say "processing" for good. It also
// removes the spool files such an import left on this process's disk.
type ImportSweeper struct{ q importSweepQuerier }

// NewImportSweeper returns an ImportSweeper over the given queries.
func NewImportSweeper(q importSweepQuerier) *ImportSweeper { return &ImportSweeper{q: q} }

// RegisterHandlers wires the sweep onto the shared asynq mux.
func (s *ImportSweeper) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskImportSweep, s.HandleSweep)
}

// HandleSweep is the asynq handler. A failure is returned so asynq retries.
func (s *ImportSweeper) HandleSweep(ctx context.Context, _ *asynq.Task) error {
	sweepImportSpool(ctx)
	msg := staleImportMessage
	n, err := s.q.FailStaleTMImports(ctx, db.FailStaleTMImportsParams{
		ErrorMessage: &msg,
		StaleBefore:  pgtype.Timestamptz{Time: time.Now().Add(-importStaleAfter), Valid: true},
	})
	if err != nil {
		logging.Warn(ctx, err, "TM import sweep failed", "tm-import-sweep-failed", logging.TaskType(TaskImportSweep))
		return fmt.Errorf("fail stale TM imports: %w", err)
	}
	if n > 0 {
		logging.Warn(ctx, nil, "stale TM imports marked failed", "tm-import-swept",
			logging.TaskType(TaskImportSweep), logging.Count(int(n)))
	}
	return nil
}
