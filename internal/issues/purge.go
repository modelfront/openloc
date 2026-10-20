package issues

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// Purge of attachment files. A delete trigger on issue_attachments queues each
// file's key, so the files of rows removed by a tenant or user delete (FK
// cascade) are deleted here. Files go before their queue rows: a failure
// leaves the key queued for the next run, and deleting a file that is already
// gone is not an error.

// TaskAttachmentPurge is the asynq task type for the hourly sweep.
// Producer: asynq.Scheduler in cmd/server/main.go. Consumer: AttachmentPurger.HandlePurge.
const TaskAttachmentPurge = "scheduler:issue-attachment-purge"

// attachmentPurgeBatch bounds one storage request.
const attachmentPurgeBatch = 500

// NewAttachmentPurgeTask returns the task the scheduler enqueues. No payload.
func NewAttachmentPurgeTask() *asynq.Task {
	return asynq.NewTask(TaskAttachmentPurge, nil)
}

type blobDeleter interface {
	DeleteKeys(ctx context.Context, keys []string) error
}

type attachmentPurgeQuerier interface {
	ListIssueAttachmentBlobPurges(ctx context.Context, batchSize int32) ([]string, error)
	DeleteIssueAttachmentBlobPurges(ctx context.Context, storageKeys []string) error
}

var _ attachmentPurgeQuerier = (db.Querier)(nil)

// AttachmentPurger deletes the queued files of deleted attachments.
type AttachmentPurger struct {
	q     attachmentPurgeQuerier
	store blobDeleter
	batch int32
}

// NewAttachmentPurger returns a purger over the given queries and storage.
func NewAttachmentPurger(q attachmentPurgeQuerier, store blobDeleter) *AttachmentPurger {
	return &AttachmentPurger{q: q, store: store, batch: attachmentPurgeBatch}
}

// Purge runs one sweep and returns how many files it deleted.
func (p *AttachmentPurger) Purge(ctx context.Context) (int, error) {
	deleted := 0
	for {
		keys, err := p.q.ListIssueAttachmentBlobPurges(ctx, p.batch)
		if err != nil {
			return deleted, fmt.Errorf("list queued attachment files: %w", err)
		}
		if len(keys) == 0 {
			return deleted, nil
		}
		if err := p.store.DeleteKeys(ctx, keys); err != nil {
			return deleted, fmt.Errorf("delete attachment files: %w", err)
		}
		if err := p.q.DeleteIssueAttachmentBlobPurges(ctx, keys); err != nil {
			return deleted, fmt.Errorf("dequeue attachment files: %w", err)
		}
		deleted += len(keys)
		if len(keys) < int(p.batch) {
			return deleted, nil
		}
	}
}

// RegisterHandlers wires the purge task onto the shared asynq mux.
func (p *AttachmentPurger) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskAttachmentPurge, p.HandlePurge)
}

// HandlePurge is the asynq handler. A failure is returned so asynq retries;
// what is left stays queued for the next run either way.
func (p *AttachmentPurger) HandlePurge(ctx context.Context, _ *asynq.Task) error {
	n, err := p.Purge(ctx)
	if err != nil {
		logging.Warn(ctx, err, "issue attachment purge failed", "issue-attachment-purge-failed",
			logging.TaskType(TaskAttachmentPurge))
		return err
	}
	logging.Info(ctx, "issue attachments purged", "issue-attachment-purged",
		logging.TaskType(TaskAttachmentPurge), logging.Count(n))
	return nil
}
