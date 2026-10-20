// Package projectdelete purges a deleted project in the background, in
// bounded statements: its segments in batches (each takes its translations,
// history, holds, comments and QA results by cascade), then its files with
// their jobs, its AI usage and activity events, and last the project row with
// its settings profile. Each batch commits on its own, so no step holds a long
// transaction or outlives a request timeout, and a retried task continues
// where the last one stopped.
//
// Only a project marked deleted (projects.deleted_at) is purged: the delete
// request marks it, which already hides it from every read.
package projectdelete

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// TaskPurge purges one deleted project. Producer: DELETE /api/projects/{id}
// and the sweep. Consumer: Purger.Handle.
const TaskPurge = "project:purge"

// TaskSweep re-enqueues the purge of every project still marked deleted, so a
// lost or failed task does not leave a half-purged project behind.
const TaskSweep = "scheduler:project-purge-sweep"

// TaskEnqueuer is the slice of the worker's enqueuer a producer needs. A purge
// runs for up to purgeTimeout, so it goes to the long-task queue.
type TaskEnqueuer interface {
	EnqueueLongTask(task *asynq.Task, opts ...asynq.Option) error
}

// purgeTimeout bounds one purge attempt. It is also how long a queued or
// failed purge keeps a second enqueue for the same project out.
const purgeTimeout = 2 * time.Hour

// Batch sizes. A segment batch also deletes one translation per target
// language and each translation's history, so it is the smallest.
const (
	segmentBatch = 500
	fileBatch    = 100
	rowBatch     = 5000
)

type payload struct {
	ProjectID int64 `json:"project_id"`
	TenantID  int64 `json:"tenant_id"`
}

// Enqueue queues the purge of projectID. A second enqueue for the project
// within purgeTimeout of the first is dropped, so a running purge never gets a
// twin; after that window the sweep can queue a purge whose task failed for
// good again.
func Enqueue(q TaskEnqueuer, tenantID, projectID int64) error {
	body, err := json.Marshal(payload{ProjectID: projectID, TenantID: tenantID})
	if err != nil {
		return err
	}
	err = q.EnqueueLongTask(asynq.NewTask(TaskPurge, body),
		asynq.Unique(purgeTimeout),
		asynq.MaxRetry(10),
		asynq.Timeout(purgeTimeout),
	)
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// NewSweepTask returns the task the scheduler enqueues. No payload.
func NewSweepTask() *asynq.Task { return asynq.NewTask(TaskSweep, nil) }

// Purger runs project purges.
type Purger struct {
	pool  *pgxpool.Pool
	q     db.Querier
	queue TaskEnqueuer // nil: the sweep has nowhere to enqueue
}

// New returns a Purger on pool. queue is where the sweep enqueues purges.
func New(pool *pgxpool.Pool, queue TaskEnqueuer) *Purger {
	return &Purger{pool: pool, q: db.New(pool), queue: queue}
}

// RegisterHandlers wires the purge and the sweep onto the shared asynq mux.
func (p *Purger) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskPurge, p.Handle)
	mux.HandleFunc(TaskSweep, p.HandleSweep)
}

// Handle is the asynq handler for TaskPurge. A failure is returned so asynq
// retries; the next attempt continues from what is left.
func (p *Purger) Handle(ctx context.Context, t *asynq.Task) error {
	var pl payload
	if err := json.Unmarshal(t.Payload(), &pl); err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	if pl.ProjectID <= 0 || pl.TenantID <= 0 {
		return fmt.Errorf("%w: project purge payload without project or tenant", asynq.SkipRetry)
	}
	return p.Purge(ctx, pl.TenantID, pl.ProjectID)
}

// HandleSweep enqueues the purge of every project still marked deleted.
func (p *Purger) HandleSweep(ctx context.Context, _ *asynq.Task) error {
	rows, err := p.q.ListDeletedProjects(ctx)
	if err != nil {
		return fmt.Errorf("list deleted projects: %w", err)
	}
	if p.queue == nil {
		return nil
	}
	for _, r := range rows {
		if err := Enqueue(p.queue, r.TenantID, r.ID); err != nil {
			return fmt.Errorf("enqueue project purge: %w", err)
		}
	}
	return nil
}

// Purge removes the project of tenantID and everything it owns. A project
// that is gone, belongs to another tenant or is not marked deleted is left
// alone. One purge per project runs at a time: while another holds the
// project's purge lock, Purge returns at once.
func (p *Purger) Purge(ctx context.Context, tenantID, projectID int64) error {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for the purge lock: %w", err)
	}
	defer conn.Release()
	lq := db.New(conn)
	locked, err := lq.TryLockProjectDelete(ctx, projectID)
	if err != nil {
		return fmt.Errorf("take the project purge lock: %w", err)
	}
	if !locked {
		logging.Info(ctx, "project purge already running elsewhere; this one stops", "project-purge-already-running",
			logging.TenantID(tenantID), logging.ProjectID(projectID))
		return nil
	}
	// The lock belongs to the connection, which goes back to the pool: release
	// it even when ctx is done.
	defer func() {
		if _, err := lq.UnlockProjectDelete(context.WithoutCancel(ctx), projectID); err != nil {
			logging.Error(ctx, err, "project purge lock not released", "project-purge-unlock-failed",
				logging.TenantID(tenantID), logging.ProjectID(projectID))
		}
	}()
	return purgeLocked(ctx, conn, lq, tenantID, projectID)
}

// purgeLocked runs every statement on conn, the connection holding the lock,
// so a purge never waits for a second connection. Each batch is its own
// transaction and runs again when it loses a deadlock: an editor still open on
// the project can be writing a segment hold on the rows a batch cascades
// through.
func purgeLocked(ctx context.Context, conn *pgxpool.Conn, lq *db.Queries, tenantID, projectID int64) error {
	if _, err := lq.GetDeletedProject(ctx, db.GetDeletedProjectParams{ID: projectID, TenantID: tenantID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			logging.Info(ctx, "project purge skipped: no deleted project to purge", "project-purge-nothing",
				logging.TenantID(tenantID), logging.ProjectID(projectID))
			return nil
		}
		return fmt.Errorf("get deleted project: %w", err)
	}

	steps := []struct {
		name string
		run  func(context.Context, db.Querier) (int64, error)
	}{
		{"segments", func(ctx context.Context, q db.Querier) (int64, error) {
			return q.PurgeDeletedProjectSegmentBatch(ctx, db.PurgeDeletedProjectSegmentBatchParams{ProjectID: projectID, Lim: segmentBatch})
		}},
		{"files", func(ctx context.Context, q db.Querier) (int64, error) {
			return q.PurgeDeletedProjectFileBatch(ctx, db.PurgeDeletedProjectFileBatchParams{ProjectID: projectID, Lim: fileBatch})
		}},
		{"AI usage events", func(ctx context.Context, q db.Querier) (int64, error) {
			return q.PurgeDeletedProjectUsageBatch(ctx, db.PurgeDeletedProjectUsageBatchParams{ProjectID: projectID, Lim: rowBatch})
		}},
		{"project events", func(ctx context.Context, q db.Querier) (int64, error) {
			return q.PurgeDeletedProjectEventBatch(ctx, db.PurgeDeletedProjectEventBatchParams{ProjectID: projectID, Lim: rowBatch})
		}},
	}
	for _, step := range steps {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			var n int64
			err := database.RetryConflicts(ctx, "project-purge-conflict-retried", func() error {
				return database.InTx(ctx, conn, func(_ pgx.Tx, q db.Querier) error {
					var err error
					n, err = step.run(ctx, q)
					return err
				})
			})
			if err != nil {
				return fmt.Errorf("purge %s: %w", step.name, err)
			}
			if n == 0 {
				break
			}
		}
	}

	// The settings profile has no cascade from the project (the FK points the
	// other way), so it goes in the same transaction as the row.
	err := database.RetryConflicts(ctx, "project-purge-conflict-retried", func() error {
		return database.InTx(ctx, conn, func(_ pgx.Tx, q db.Querier) error {
			profileID, err := q.PurgeDeletedProject(ctx, projectID)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("delete project: %w", err)
			}
			if err := q.DeleteSettingsProfile(ctx, profileID); err != nil {
				return fmt.Errorf("delete settings profile: %w", err)
			}
			return nil
		})
	})
	if err != nil {
		return fmt.Errorf("purge project: %w", err)
	}
	logging.Info(ctx, "project purged", "project-purged", logging.TenantID(tenantID), logging.ProjectID(projectID))
	return nil
}
