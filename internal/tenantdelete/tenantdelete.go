// Package tenantdelete removes a closed organization and everything it owns
// in the background, in bounded statements: one file at a time, translation
// memory entries and term concepts in chunks, then the tenant row, whose
// cascade removes what is left. Each statement commits on its own, so no step
// holds a long transaction or outlives a request timeout, and a retried task
// continues where the last one stopped.
//
// Only a tenant marked 'deleting' is deleted: the owner's request marks it,
// which already shuts every member out (the tenant is no longer approved).
package tenantdelete

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// TaskDelete deletes one tenant marked 'deleting'. Producer: the owner's
// DELETE /api/tenants/current and the sweep. Consumer: Deleter.Handle.
const TaskDelete = "tenant:delete"

// TaskSweep re-enqueues the delete of every tenant still marked 'deleting', so
// a lost or failed task does not leave a half-deleted organization behind.
const TaskSweep = "scheduler:tenant-delete-sweep"

// StatusDeleting marks a tenant whose delete is under way.
const StatusDeleting = "deleting"

// TaskEnqueuer is the slice of the worker's enqueuer a producer needs. A
// delete runs for up to deleteTimeout, so it goes to the long-task queue.
type TaskEnqueuer interface {
	EnqueueLongTask(task *asynq.Task, opts ...asynq.Option) error
}

// deleteTimeout bounds one delete attempt. It is also how long a queued or
// failed delete keeps a second enqueue for the same tenant out.
const deleteTimeout = 6 * time.Hour

type payload struct {
	TenantID int64 `json:"tenant_id"`
}

// Enqueue queues the delete of tenantID. A second enqueue for the tenant
// within deleteTimeout of the first is dropped, so a running delete never
// gets a twin; after that window the sweep can queue a delete whose task
// failed for good again.
func Enqueue(q TaskEnqueuer, tenantID int64) error {
	body, err := json.Marshal(payload{TenantID: tenantID})
	if err != nil {
		return err
	}
	err = q.EnqueueLongTask(asynq.NewTask(TaskDelete, body),
		asynq.Unique(deleteTimeout),
		asynq.MaxRetry(10),
		asynq.Timeout(deleteTimeout),
	)
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// NewSweepTask returns the task the scheduler enqueues. No payload.
func NewSweepTask() *asynq.Task { return asynq.NewTask(TaskSweep, nil) }

// Deleter runs tenant deletes.
type Deleter struct {
	pool  *pgxpool.Pool
	q     db.Querier
	queue TaskEnqueuer // nil: the sweep has nowhere to enqueue
}

// New returns a Deleter on pool. queue is where the sweep enqueues deletes.
func New(pool *pgxpool.Pool, queue TaskEnqueuer) *Deleter {
	return &Deleter{pool: pool, q: db.New(pool), queue: queue}
}

// RegisterHandlers wires the delete and the sweep onto the shared asynq mux.
func (d *Deleter) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskDelete, d.Handle)
	mux.HandleFunc(TaskSweep, d.HandleSweep)
}

// Handle is the asynq handler for TaskDelete. A failure is returned so asynq
// retries; the next attempt continues from what is left.
func (d *Deleter) Handle(ctx context.Context, t *asynq.Task) error {
	var p payload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	return d.Delete(ctx, p.TenantID)
}

// HandleSweep enqueues the delete of every tenant still marked 'deleting'.
func (d *Deleter) HandleSweep(ctx context.Context, _ *asynq.Task) error {
	ids, err := d.q.ListDeletingTenants(ctx)
	if err != nil {
		return fmt.Errorf("list deleting tenants: %w", err)
	}
	if d.queue == nil {
		return nil
	}
	for _, id := range ids {
		if err := Enqueue(d.queue, id); err != nil {
			return fmt.Errorf("enqueue tenant delete: %w", err)
		}
	}
	return nil
}

// Delete removes the tenant and everything it owns. A tenant that is gone or
// not marked 'deleting' is left alone. One delete per tenant runs at a time:
// while another holds the tenant's delete lock, Delete returns at once.
func (d *Deleter) Delete(ctx context.Context, tenantID int64) error {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for the delete lock: %w", err)
	}
	defer conn.Release()
	lq := db.New(conn)
	locked, err := lq.TryLockTenantDelete(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("take the tenant delete lock: %w", err)
	}
	if !locked {
		logging.Info(ctx, "tenant delete already running elsewhere; this one stops", "tenant-delete-already-running",
			logging.TenantID(tenantID))
		return nil
	}
	// The lock belongs to the connection, which goes back to the pool: release
	// it even when ctx is done.
	defer func() {
		if _, err := lq.UnlockTenantDelete(context.WithoutCancel(ctx), tenantID); err != nil {
			logging.Error(ctx, err, "tenant delete lock not released", "tenant-delete-unlock-failed", logging.TenantID(tenantID))
		}
	}()
	return deleteLocked(ctx, lq, tenantID)
}

// deleteLocked runs every statement on q, the connection holding the lock, so
// a delete never waits for a second connection.
func deleteLocked(ctx context.Context, q *db.Queries, tenantID int64) error {
	t, err := q.GetTenantStatus(ctx, tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get tenant: %w", err)
	}
	if t.Status != StatusDeleting {
		logging.Warn(ctx, nil, "tenant delete skipped: the tenant is not marked deleting", "tenant-delete-not-marked",
			logging.TenantID(tenantID), logging.Label("status", t.Status))
		return nil
	}

	if err := deleteFiles(ctx, q, tenantID); err != nil {
		return err
	}
	steps := []struct {
		name string
		run  func(context.Context, int64) (int64, error)
	}{
		{"translation memory entries", q.DeleteTenantTMEntryBatch},
		{"term concepts", q.DeleteTenantTermConceptBatch},
	}
	for _, step := range steps {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := step.run(ctx, tenantID)
			if err != nil {
				return fmt.Errorf("delete %s: %w", step.name, err)
			}
			if n == 0 {
				break
			}
		}
	}
	if err := q.DeleteTenant(ctx, tenantID); err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}
	logging.Info(ctx, "tenant deleted", "tenant-deleted", logging.TenantID(tenantID))
	return nil
}

// projectPage is how many project ids deleteFiles reads per statement.
const projectPage = 1000

// deleteFiles deletes the tenant's files one per statement, project by
// project, so each project is looked at once more after it is empty rather
// than again for every file that follows.
func deleteFiles(ctx context.Context, q *db.Queries, tenantID int64) error {
	var after int64
	for {
		ids, err := q.ListTenantProjectIDsPage(ctx, db.ListTenantProjectIDsPageParams{
			TenantID: tenantID, AfterID: after, Lim: projectPage,
		})
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		for _, projectID := range ids {
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				n, err := q.DeleteProjectFileBatch(ctx, projectID)
				if err != nil {
					return fmt.Errorf("delete file: %w", err)
				}
				if n == 0 {
					break
				}
			}
		}
		after = ids[len(ids)-1]
	}
}
