package demoorg

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/logging"
)

// TaskRefresh is the daily scheduler task that keeps the demo organization
// current. Producer: asynq.Scheduler in cmd/server/main.go. Consumer: Refresher.
const TaskRefresh = "scheduler:demo-org-refresh"

// NewRefreshTask returns the task the scheduler enqueues daily. No payload:
// the refresh reads the clock when it runs.
func NewRefreshTask() *asynq.Task {
	return asynq.NewTask(TaskRefresh, nil)
}

// Refresher runs Ensure for the scheduler and at server start.
type Refresher struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewRefresher(pool *pgxpool.Pool) *Refresher {
	return &Refresher{pool: pool, now: time.Now}
}

// RegisterHandlers wires the refresh task onto the shared asynq mux.
func (r *Refresher) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskRefresh, r.HandleRefresh)
}

// HandleRefresh is the asynq handler. A failure is returned so asynq retries.
func (r *Refresher) HandleRefresh(ctx context.Context, _ *asynq.Task) error {
	return r.Refresh(ctx)
}

// Refresh runs Ensure and logs what it did.
func (r *Refresher) Refresh(ctx context.Context) error {
	tenantID, rebuilt, err := Ensure(ctx, r.pool, r.now())
	if err != nil {
		logging.Warn(ctx, err, "demo organization refresh failed", "demo-org-refresh-failed",
			logging.TaskType(TaskRefresh))
		return err
	}
	status := "current"
	if rebuilt {
		status = "rebuilt"
	}
	logging.Info(ctx, "demo organization "+status, "demo-org-refreshed",
		logging.TenantID(tenantID), logging.Status(status))
	return nil
}
