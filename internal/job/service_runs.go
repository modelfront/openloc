package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	"openloc/internal/autostep"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/llmdebug"
	"openloc/internal/logging"
	"openloc/internal/notification"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/stepdef"
	"openloc/internal/taskedit"
)

// ErrRunInterrupted: a synchronous run was cut short by a server restart. The
// pipeline is idempotent per segment, so running it again resumes.
var ErrRunInterrupted = errors.New("interrupted by a server restart — run it again to resume (completed work is kept)")

// ResolveJob loads a job and its project's source language, tenant-scoped at
// every hop. pgx.ErrNoRows when any hop is outside the tenant.
func (s *Service) ResolveJob(ctx context.Context, tenantID, jobID int64) (db.Job, string, error) {
	job, err := s.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return db.Job{}, "", err
	}
	file, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: job.FileID, TenantID: tenantID})
	if err != nil {
		return db.Job{}, "", err
	}
	project, err := s.queries.GetProject(ctx, db.GetProjectParams{ID: file.ProjectID, TenantID: tenantID})
	if err != nil {
		return db.Job{}, "", err
	}
	return job, project.SourceLanguage, nil
}

// runContext is the context a synchronous pipeline run executes on: detached
// from the caller's cancellation (a browser refresh or closed chat must not
// abort paid work mid-flight) but canceled when the process starts draining.
// WithoutCancel keeps the values (claims, logging scope).
func (s *Service) runContext(ctx context.Context) (context.Context, context.CancelFunc) {
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	if s.draining != nil {
		go func() {
			select {
			case <-s.draining:
				cancel()
			case <-runCtx.Done(): // run finished first
			}
		}()
	}
	return runCtx, cancel
}

// afterRunTimeout bounds the bookkeeping after a run.
const afterRunTimeout = 30 * time.Second

// afterRunContext is the context the work after a run executes on (the
// history event, the step it completes, the failure notice): detached from
// the caller like the run itself, so a user who left still gets it, and
// bounded so it cannot hang a request.
func afterRunContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), afterRunTimeout)
}

// claimJob atomically flips the job to in_progress unless a pipeline run
// already holds it (ConflictError). The SQL twin of pipeline.IsPipelineRunning:
// a step label ending in "complete" does not block, and a claim untouched for
// 30 minutes (crashed pod) is reclaimable. It returns the state the claim
// replaced; a caller that then rejects the run pre-flight must put the job
// back with restoreJobState.
func (s *Service) claimJob(ctx context.Context, tenantID int64, job db.Job, step string) (db.ClaimJobPipelineRow, error) {
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prev, err := s.queries.ClaimJobPipeline(dbCtx, db.ClaimJobPipelineParams{ID: job.ID, TenantID: tenantID, CurrentStep: step})
	if errors.Is(err, pgx.ErrNoRows) {
		return prev, httputil.NewConflictError("a pipeline run is already in progress for this job — wait for it to finish")
	}
	if err != nil {
		logging.Error(ctx, err, "failed to claim job for pipeline run", "job-pipeline-claim-failed", logging.JobID(job.ID))
		return prev, fmt.Errorf("claim job: %w", err)
	}
	return prev, nil
}

// restoreJobState undoes a claim after a pre-flight rejection: nothing ran,
// so the job goes back exactly as the claim found it — status, step,
// error_message (the claim nulled it) and updated_at. The query is
// conditional on the claim's own step, so a concurrent writer that
// legitimately moved the job since wins.
func (s *Service) restoreJobState(ctx context.Context, jobID int64, prev db.ClaimJobPipelineRow, claimStep string) {
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.queries.RestoreJobPipelineState(dbCtx, db.RestoreJobPipelineStateParams{
		ID:           jobID,
		Status:       prev.Status,
		CurrentStep:  prev.CurrentStep,
		ErrorMessage: prev.ErrorMessage,
		UpdatedAt:    prev.UpdatedAt,
		ClaimStep:    claimStep,
	}); err != nil {
		logging.Error(ctx, err, "failed to restore job state after pre-flight rejection", "job-pipeline-restore-failed", logging.JobID(jobID))
	}
}

// runFailed records a failed run and returns the error the caller reports:
// ErrRunInterrupted for a drain, the provider's actionable ClientError as is.
func (s *Service) runFailed(ctx, runCtx context.Context, tenantID, jobID int64, label string, err error) error {
	if interruptedByDrain(runCtx, err) {
		logging.Warn(ctx, err, label+" interrupted by shutdown", "job-pipeline-interrupted", logging.JobID(jobID))
		s.markJobError(jobID, ErrRunInterrupted)
		s.notifyFailure(ctx, tenantID, jobID, ErrRunInterrupted)
		return ErrRunInterrupted
	}
	logging.Error(ctx, err, label+" failed", "job-pipeline-run-failed", logging.JobID(jobID))
	s.markJobError(jobID, err)
	s.notifyFailure(ctx, tenantID, jobID, err)
	if _, ok := httputil.AsClientError(err); ok {
		return err
	}
	// An engine that cannot run (switched off, key or extension removed) is
	// the user's to fix, and its message says how.
	var unusable *pipeline.EngineUnusableError
	if errors.As(err, &unusable) {
		return httputil.NewClientError(err.Error())
	}
	// %v, not %w: a lookup's pgx.ErrNoRows deep in the run is a failed run,
	// not a missing job, and must not be reported as one.
	return fmt.Errorf("%s failed: %v", label, err)
}

// notifyFailure sends the bell notification for a failed run to the user who
// started it (and, through the notifier, the project's creator).
func (s *Service) notifyFailure(ctx context.Context, tenantID, jobID int64, err error) {
	if s.failures == nil {
		return
	}
	f := notification.JobFailure{TenantID: tenantID, JobID: jobID, MT: pipeline.IsMTFailure(err), Reason: err.Error()}
	if claims, ok := auth.ClaimsFromContext(ctx); ok && claims.UserID != 0 {
		f.StartedBy = &claims.UserID
	}
	if nerr := s.failures.NotifyJobFailed(ctx, f); nerr != nil {
		logging.Warn(ctx, nerr, "failed to send job failure notification", "job-failure-notify-failed", logging.JobID(jobID))
	}
}

// interruptedByDrain distinguishes "the run died because WE canceled it"
// from a genuine failure that merely coincided with draining.
func interruptedByDrain(runCtx context.Context, err error) bool {
	return runCtx.Err() != nil && errors.Is(err, context.Canceled)
}

// PhaseRunResult is what one Apply TM / Apply MT run did.
type PhaseRunResult struct {
	// Stopped: a user stopped the run; the saved segments are kept.
	Stopped bool
	// SkipReason is "no_segments" when there was nothing to translate.
	SkipReason string
	// TaskReopened: the run reopened the job's completed task.
	TaskReopened bool
	// Held counts the segments skipped because another user has them open.
	Held int
}

// checkPretranslation is the gate for running pre-translation on a job, shared
// by Apply TM/MT and Re-run. Pre-translation writes text into the job's current
// task, so the task edit guard applies, first step only. A task whose
// pre-translation failed may run it again: that is how the error is fixed.
func (s *Service) checkPretranslation(ctx context.Context, tenantID, jobID int64) (taskedit.Decision, error) {
	edit, err := s.guard.Check(ctx, tenantID, taskedit.Ref{JobID: jobID}, taskedit.Opts{AllowPretranslationError: true})
	if err != nil {
		return taskedit.Decision{}, err
	}
	if !edit.InFirstStep() {
		return taskedit.Decision{}, httputil.NewForbiddenError("pre-translation is only available in the first workflow step")
	}
	return edit, nil
}

// ApplyPhases runs Apply TM or Apply MT on one job synchronously: claim, run,
// status bookkeeping, audit event. A phase the project has no configuration
// for is a ClientError, and the job is put back as it was.
func (s *Service) ApplyPhases(ctx context.Context, tenantID int64, job db.Job, sourceLang string, phases pipeline.TranslatePhases, label string) (PhaseRunResult, error) {
	edit, err := s.checkPretranslation(ctx, tenantID, job.ID)
	if err != nil {
		return PhaseRunResult{}, err
	}
	task, err := s.guard.Apply(ctx, tenantID, edit)
	if err != nil {
		return PhaseRunResult{}, err
	}
	if task.Reopened {
		// The reopen moved the job out of 'completed'; the claim below
		// snapshots the job, and an unactionable phase restores that snapshot.
		if job, err = s.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: job.ID, TenantID: tenantID}); err != nil {
			return PhaseRunResult{}, fmt.Errorf("reload job: %w", err)
		}
	}
	claimStep := pipeline.PhaseDescription(phases)
	claimed, err := s.claimJob(ctx, tenantID, job, claimStep)
	if err != nil {
		return PhaseRunResult{}, err
	}
	runCtx, cancel := s.runContext(ctx)
	defer cancel()
	// The debug feed pairs an MT entry with the QE+APE entry for the same job.
	runCtx = llmdebug.WithMeta(runCtx, llmdebug.Meta{JobID: job.ID})
	// The history names the user who ran it, and the run passes their holds.
	runCtx = pipeline.WithTriggeredBy(runCtx, auth.UserIDPtr(auth.UserIDFromContext(ctx)))
	outcome, err := s.pipeline.TranslateFile(runCtx, tenantID, job.FileID, sourceLang, job.TargetLanguage, s.pipelineProgress(job.ID, cancel), phases)
	ctx, cancelAfter := afterRunContext(ctx)
	defer cancelAfter()
	if err != nil {
		// A user stop also cancels runCtx, so it is checked before the drain.
		if st, ok := pipeline.StopRequested(s.queries, job.ID, err); ok {
			s.finishStopped(ctx, job, pipeline.PhaseNoun(phases), st)
			return PhaseRunResult{Stopped: true}, nil
		}
		return PhaseRunResult{}, s.runFailed(ctx, runCtx, tenantID, job.ID, label, err)
	}
	// The pipeline no-ops a phase with no configuration (right for the upload
	// hook); a user who asked for it gets a message they can act on.
	if msg := phaseUnactionableMessage(phases, outcome, sourceLang, job.TargetLanguage); msg != "" {
		s.restoreJobState(ctx, job.ID, claimed, claimStep)
		return PhaseRunResult{}, httputil.NewClientError(msg)
	}
	eventType := projectevents.TypeJobMTApplyStarted
	if phases.UseTM {
		eventType = projectevents.TypeJobTMApplyStarted
	}
	// Recorded once the run is over, with what it filled.
	s.emitJobEvent(ctx, eventType, job, outcome.HistoryDetails(phases))
	// "completed" is reserved for job completion (CompleteJob).
	s.setJobStatus(job.ID, "in_progress", label+" complete")
	// MT ran, so the job no longer lacks it, and a Pre-translation step left
	// open for want of an engine is done — the same bookkeeping as the upload
	// worker. After setJobStatus: completing the last step completes the job.
	// A run on a selection leaves the step open: the rest of the job may still
	// be empty.
	if phases.UseMT {
		autostep.RecordMTMissing(ctx, s.queries, job.ID, outcome.MTMissing())
		if outcome.MTMissing() == "" && phases.TranslationIDs == nil {
			autostep.Complete(ctx, s.queries, s.workflow, tenantID, job.ID, stepdef.TypePretranslate,
				auth.UserIDPtr(auth.UserIDFromContext(ctx)), map[string]any{"phase": pipeline.PhaseNoun(phases)})
		}
	}
	return PhaseRunResult{SkipReason: phaseSkipReason(phases, outcome), TaskReopened: task.Reopened, Held: outcome.Held}, nil
}

// AIStepRunResult is what one Re-run AI step did.
type AIStepRunResult struct {
	Stopped bool
	pipeline.AIStepResult
}

// RunAIStep re-runs the QE+APE predict for the job's active workflow step
// synchronously. The claim label engages the QE+APE editor lock
// for the run; a skip (no router, no engine, no provider) is a ClientError and
// the job is put back as it was.
func (s *Service) RunAIStep(ctx context.Context, tenantID int64, job db.Job) (AIStepRunResult, error) {
	// Keep the label in sync with internal/worker (enqueue.go / handler.go).
	aiClaimStep := "Running " + pipeline.AIStepMarker
	claimed, err := s.claimJob(ctx, tenantID, job, aiClaimStep)
	if err != nil {
		return AIStepRunResult{}, err
	}
	runCtx, cancel := s.runContext(ctx)
	defer cancel()
	res, err := s.pipeline.RunAIStep(runCtx, tenantID, job.ID, s.pipelineProgress(job.ID, cancel))
	ctx, cancelAfter := afterRunContext(ctx)
	defer cancelAfter()
	if err != nil {
		if st, ok := pipeline.StopRequested(s.queries, job.ID, err); ok {
			s.finishStopped(ctx, job, pipeline.PhaseQEAPE, st)
			return AIStepRunResult{Stopped: true}, nil
		}
		return AIStepRunResult{}, s.runFailed(ctx, runCtx, tenantID, job.ID, "ai-step", err)
	}
	if msg := aiStepSkipMessage(res.SkipReason); msg != "" {
		s.restoreJobState(ctx, job.ID, claimed, aiClaimStep)
		return AIStepRunResult{}, httputil.NewClientError(msg)
	}
	s.emitJobEvent(ctx, projectevents.TypeJobAIApplyStarted, job, nil)
	s.setJobStatus(job.ID, "in_progress", "ai-step complete")
	return AIStepRunResult{AIStepResult: res}, nil
}
