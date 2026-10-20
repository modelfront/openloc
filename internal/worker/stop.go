package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/notification"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
)

// verifyPayloadJob confirms the payload's job belongs to the payload's tenant
// and, when the payload names a file, to that file. Handlers call it before
// they write anything, so a malformed payload cannot touch another tenant's
// job. A job that no longer exists passes: startRun closes it out as stopped.
// A mismatch is permanent, so the task is not retried.
func (h *Handler) verifyPayloadJob(ctx context.Context, p PipelinePayload) error {
	if p.JobID == 0 || h.queries == nil {
		return nil
	}
	job, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: p.JobID, TenantID: p.TenantID})
	if err == nil {
		if p.FileID != 0 && job.FileID != p.FileID {
			return fmt.Errorf("job %d does not belong to file %d: %w", p.JobID, p.FileID, asynq.SkipRetry)
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("verify job %d: %w", p.JobID, err)
	}
	if _, err := h.queries.GetJob(ctx, p.JobID); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("verify job %d: %w", p.JobID, err)
	}
	return fmt.Errorf("job %d does not belong to tenant %d: %w", p.JobID, p.TenantID, asynq.SkipRetry)
}

// startRun is the worker's in_progress transition. It returns false when a
// stop was requested while the task sat in the queue (or the job is gone): the
// job is then closed out as stopped and the handler must return nil so asynq
// records success instead of retrying. phase names the run for the history
// event and the stop label.
func (h *Handler) startRun(ctx context.Context, p PipelinePayload, step, phase string) bool {
	if p.JobID == 0 || h.queries == nil {
		return true
	}
	dbCtx, cancel := freshDBContext()
	defer cancel()
	n, err := h.queries.StartJobRun(dbCtx, db.StartJobRunParams{ID: p.JobID, CurrentStep: step})
	if err != nil {
		logging.Warn(dbCtx, err, "failed to mark job run as started", "job-run-start-failed", logging.JobID(p.JobID))
		return true
	}
	if n == 0 {
		logging.Info(dbCtx, "job run stopped before it started", "job-run-stopped-queued", logging.JobID(p.JobID))
		fileID := p.FileID
		if fileID == 0 {
			fileID = h.jobFileID(p.JobID)
		}
		h.finishStopped(ctx, p, fileID, phase, pipeline.RunState{})
		return false
	}
	return true
}

// finishStopped closes a run the user stopped: terminal 'stopped' status with a
// resumable label, a pipeline_stopped event, and the auto-analysis promotion a
// finished run performs. Callers return nil afterwards (no asynq retry).
func (h *Handler) finishStopped(ctx context.Context, p PipelinePayload, fileID int64, phase string, st pipeline.RunState) {
	h.markStopped(p.JobID, pipeline.StoppedStepLabel(phase, st))
	h.forgetRunSoFar(p)
	details := map[string]any{"phase": phase}
	if st.Translated != nil && st.Total != nil {
		details["translated_count"] = *st.Translated
		details["total_count"] = *st.Total
	}
	h.emitJobPipelineEvent(ctx, p.TenantID, p.JobID, projectevents.TypeJobPipelineStopped, p.TriggeredBy, details)
	h.maybeAutoAnalyzeProject(ctx, fileID, p.TenantID)
}

// failRun is the shared failure tail of the pipeline handlers; it returns the
// error the handler hands back to asynq. A non-final attempt marks the job
// retrying. The final one — the last attempt, or a pipeline.Permanent error on
// any attempt — writes the failed status with the error, a pipeline_failed
// event, and the auto-analysis promotion a permanently failed run must not
// strand.
func (h *Handler) failRun(ctx context.Context, p PipelinePayload, fileID int64, phase string, err error) error {
	// asynq tries again: the job reads as running until the last attempt, so
	// nobody re-runs or completes its task in between.
	if willRetry(ctx, err) {
		h.markRetrying(p.JobID, phase, err)
		return err
	}
	h.failJob(ctx, p.JobID, err)
	h.emitJobPipelineEvent(ctx, p.TenantID, p.JobID, projectevents.TypeJobPipelineFailed, p.TriggeredBy,
		map[string]any{"phase": phase, "error": projectevents.ShortMessage(err.Error())})
	h.promoteAfterFailure(fileID, p.TenantID)
	h.notifyFailure(ctx, p, err)
	if pipeline.IsPermanent(err) {
		return fmt.Errorf("%w: %w", err, asynq.SkipRetry)
	}
	return err
}

// freshDBContext is the context for a job write the task context may no
// longer carry: it is often cancelled or past its deadline on a failure path.
func freshDBContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// willRetry reports whether asynq delivers this task again after err.
func willRetry(ctx context.Context, err error) bool {
	return !pipeline.IsPermanent(err) && !isLastAttempt(ctx)
}

// notifyFailure sends the bell notification for a job that failed for good.
// Callers reach it on the final attempt only.
func (h *Handler) notifyFailure(ctx context.Context, p PipelinePayload, err error) {
	if h.failures == nil || p.JobID == 0 {
		return
	}
	f := notification.JobFailure{
		TenantID: p.TenantID, JobID: p.JobID, StartedBy: p.TriggeredBy,
		MT: pipeline.IsMTFailure(err), Reason: err.Error(),
	}
	if nerr := h.failures.NotifyJobFailed(ctx, f); nerr != nil {
		logging.Warn(ctx, nerr, "failed to send job failure notification", "job-failure-notify-failed", logging.JobID(p.JobID))
	}
}

// markRetrying records a failed attempt that asynq will retry, on a fresh
// context like failJob: the task context is usually done by now.
func (h *Handler) markRetrying(jobID int64, phase string, jobErr error) {
	if jobID == 0 || h.queries == nil {
		return
	}
	dbCtx, cancel := freshDBContext()
	defer cancel()
	msg := jobErr.Error()
	if err := h.queries.MarkJobRetrying(dbCtx, db.MarkJobRetryingParams{
		ID: jobID, CurrentStep: pipeline.RetryingStepLabel(phase), ErrorMessage: &msg,
	}); err != nil {
		logging.Warn(dbCtx, err, "failed to mark job as retrying", "job-run-retry-write-failed", logging.JobID(jobID))
	}
}

func (h *Handler) markStopped(jobID int64, label string) {
	if jobID == 0 || h.queries == nil {
		return
	}
	dbCtx, cancel := freshDBContext()
	defer cancel()
	if err := h.queries.MarkJobStopped(dbCtx, db.MarkJobStoppedParams{ID: jobID, CurrentStep: label}); err != nil {
		logging.Warn(dbCtx, err, "failed to mark job as stopped", "job-run-stop-write-failed", logging.JobID(jobID))
	}
}

// jobFileID resolves the file behind a job on a fresh context; 0 when unknown.
// The AI-step payload carries no file_id, and the failure paths run after the
// task context is usually cancelled.
func (h *Handler) jobFileID(jobID int64) int64 {
	if h.queries == nil {
		return 0
	}
	dbCtx, cancel := freshDBContext()
	defer cancel()
	job, err := h.queries.GetJob(dbCtx, jobID)
	if err != nil {
		return 0
	}
	return job.FileID
}
