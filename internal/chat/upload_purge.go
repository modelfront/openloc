package chat

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// Purge of assistant uploads. An upload expires after 24
// hours; this sweep deletes the expired rows and the blobs of every deleted
// row. A delete trigger on chat_uploads queues each blob key, so blobs of rows
// removed by a tenant or user delete (FK cascade) are purged here too.
//
// Blobs go before their queue rows: a failure leaves the key queued for the
// next run, and deleting a blob that is already gone is not an error.

// TaskChatUploadPurge is the asynq task type for the hourly sweep.
// Producer: asynq.Scheduler in cmd/server/main.go. Consumer: UploadPurger.HandlePurge.
const TaskChatUploadPurge = "scheduler:chat-upload-purge"

// uploadPurgeBatch bounds one DELETE and one storage request.
const uploadPurgeBatch = 500

// NewUploadPurgeTask returns the task the scheduler enqueues. No payload.
func NewUploadPurgeTask() *asynq.Task {
	return asynq.NewTask(TaskChatUploadPurge, nil)
}

// blobDeleter is the storage seam for the purge. *file.Storage and
// *file.MemoryStore satisfy it.
type blobDeleter interface {
	DeleteKeys(ctx context.Context, keys []string) error
}

type uploadPurgeQuerier interface {
	DeleteExpiredChatUploads(ctx context.Context, batchSize int32) (int64, error)
	ListChatUploadBlobPurges(ctx context.Context, batchSize int32) ([]string, error)
	DeleteChatUploadBlobPurges(ctx context.Context, storageKeys []string) error
}

var _ uploadPurgeQuerier = (db.Querier)(nil)

// UploadPurger deletes expired assistant uploads and queued upload blobs.
type UploadPurger struct {
	q     uploadPurgeQuerier
	store blobDeleter
	batch int32
}

// NewUploadPurger returns a purger over the given queries and storage.
func NewUploadPurger(q uploadPurgeQuerier, store blobDeleter) *UploadPurger {
	return &UploadPurger{q: q, store: store, batch: uploadPurgeBatch}
}

// Purge runs one sweep and returns how many rows and blobs it deleted.
func (p *UploadPurger) Purge(ctx context.Context) (rows int64, blobs int, err error) {
	for {
		n, err := p.q.DeleteExpiredChatUploads(ctx, p.batch)
		if err != nil {
			return rows, blobs, fmt.Errorf("delete expired chat uploads: %w", err)
		}
		rows += n
		if n < int64(p.batch) {
			break
		}
	}
	for {
		keys, err := p.q.ListChatUploadBlobPurges(ctx, p.batch)
		if err != nil {
			return rows, blobs, fmt.Errorf("list queued chat upload blobs: %w", err)
		}
		if len(keys) == 0 {
			return rows, blobs, nil
		}
		if err := p.store.DeleteKeys(ctx, keys); err != nil {
			return rows, blobs, fmt.Errorf("delete chat upload blobs: %w", err)
		}
		if err := p.q.DeleteChatUploadBlobPurges(ctx, keys); err != nil {
			return rows, blobs, fmt.Errorf("dequeue chat upload blobs: %w", err)
		}
		blobs += len(keys)
		if len(keys) < int(p.batch) {
			return rows, blobs, nil
		}
	}
}

// RegisterHandlers wires the purge task onto the shared asynq mux.
func (p *UploadPurger) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskChatUploadPurge, p.HandlePurge)
}

// HandlePurge is the asynq handler. A failure is returned so asynq retries;
// what is left stays queued for the next run either way.
func (p *UploadPurger) HandlePurge(ctx context.Context, _ *asynq.Task) error {
	rows, blobs, err := p.Purge(ctx)
	if err != nil {
		logging.Warn(ctx, err, "chat upload purge failed", "chat-upload-purge-failed",
			logging.TaskType(TaskChatUploadPurge))
		return err
	}
	logging.Info(ctx, "chat uploads purged", "chat-upload-purged",
		logging.TaskType(TaskChatUploadPurge),
		logging.Debugf("deleted_rows", rows),
		logging.Count(blobs))
	return nil
}
