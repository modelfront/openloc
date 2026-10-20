package job

import (
	"context"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/workflow"
)

// HandleRerun queues the automation a failed job stopped in to run again — the
// AI step or pre-translation — so an errored task can recover without anyone
// editing it. POST /api/jobs/{jobID}/rerun.
//
// Access: re-running the AI step needs a manager, like Re-run AI step;
// pre-translation is open to whoever may run Apply TM/MT on the job (the
// job group's RequireLinguistJobAccess) and has Apply TM/MT's gate: the
// current task takes edits and is the first step. The worker runs it, so
// failures, pipeline events and an `ai` step's self-completion behave as in
// any automatic run. Pre-translation re-runs TM and MT: the failed label does not
// say which phases ran, and both skip segments already translated.
func (h *Handler) HandleRerun(w http.ResponseWriter, r *http.Request) {
	job, sourceLang, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	userID := auth.UserIDFromContext(ctx)

	phase := pipeline.FailedPhaseOf(job.Status, job.CurrentStep)
	if phase == "" {
		httputil.Error(w, http.StatusConflict, "this task has no error to re-run")
		return
	}

	var aiEnq workflow.AIStepEnqueuer
	if phase == pipeline.FailedPhaseAIStep {
		if !auth.IsManagerRole(auth.RoleFromContext(ctx)) {
			httputil.Error(w, http.StatusForbidden, "only a manager can re-run the AI step")
			return
		}
		if !h.activeStepRunsAI(ctx, job.ID) {
			httputil.BadRequest(w, aiStepSkipMessage(pipeline.PhaseSkipNoAIStep))
			return
		}
		e, ok := h.enqueuer.(workflow.AIStepEnqueuer)
		if !ok {
			httputil.Error(w, http.StatusServiceUnavailable, "background processing is not available")
			return
		}
		aiEnq = e
	} else if _, err := h.checkPretranslation(ctx, tenantID, job.ID); err != nil {
		httputil.ServiceError(w, err, "re-run", "job")
		return
	}
	if h.enqueuer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "background processing is not available")
		return
	}

	label := "Queued for pre-translation"
	if aiEnq != nil {
		label = "Queued for " + pipeline.AIStepMarker
	}
	n, err := h.queries.ClaimFailedJobForRerun(ctx, db.ClaimFailedJobForRerunParams{ID: job.ID, CurrentStep: label})
	if err != nil {
		httputil.ServerError(w, "claim", "job")
		return
	}
	if n == 0 {
		httputil.Error(w, http.StatusConflict, "this task is already running again")
		return
	}

	if aiEnq != nil {
		err = aiEnq.EnqueueAIStep(job.ID, tenantID, &userID)
	} else {
		err = h.enqueuer.EnqueuePipeline(job.FileID, sourceLang, job.TargetLanguage, job.ID, tenantID, pipeline.FullPipeline, &userID)
	}
	if err != nil {
		logging.Error(ctx, err, "re-run enqueue failed; job left failed", "job-rerun-enqueue-failed", logging.JobID(job.ID))
		// Put the error back so the task still reads as errored.
		if rerr := h.queries.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: job.ID, ErrorMessage: job.ErrorMessage}); rerr != nil {
			logging.Error(ctx, rerr, "restoring the failed state after a re-run enqueue error failed", "job-rerun-restore-failed", logging.JobID(job.ID))
		}
		httputil.ServerError(w, "queue", "re-run")
		return
	}

	details := map[string]any{"phase": string(phase)}
	if job.ErrorMessage != nil {
		details["error"] = projectevents.ShortMessage(*job.ErrorMessage)
	}
	h.emitJobEvent(ctx, projectevents.TypeJobRerunStarted, job, details)

	httputil.JSON(w, http.StatusAccepted, map[string]any{"status": "queued", "phase": phase})
}

// activeStepRunsAI reports whether the job's in-progress task carries an AI
// router, which an AI-step re-run needs. A lookup failure reads as "no".
func (h *Handler) activeStepRunsAI(ctx context.Context, jobID int64) bool {
	active, err := h.queries.GetActiveStepForJob(ctx, jobID)
	if err != nil {
		return false
	}
	ws, err := h.queries.GetWorkflowStep(ctx, active.WorkflowStepID)
	return err == nil && ws.AiRouterID != nil
}
