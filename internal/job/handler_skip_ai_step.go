package job

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/stepdef"
	"openloc/internal/workflow"
)

// HandleSkipAIStep accepts a failed AI run on a hybrid step without completing
// the step: the error is cleared and the task stays in progress, so the person
// works the machine translation without the AI pass.
// POST /api/jobs/{jobID}/skip-ai-step.
//
// Access: managers, like the AI step's re-run. Only a hybrid step can skip;
// on an `ai` step nobody works after the AI, so Complete step is the way on.
func (h *Handler) HandleSkipAIStep(w http.ResponseWriter, r *http.Request) {
	job, _, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}
	ctx := r.Context()
	if !auth.IsManagerRole(auth.RoleFromContext(ctx)) {
		httputil.Error(w, http.StatusForbidden, "only a manager can skip the AI step")
		return
	}
	if pipeline.FailedPhaseOf(job.Status, job.CurrentStep) != pipeline.FailedPhaseAIStep {
		httputil.Error(w, http.StatusConflict, "this task has no AI step error to skip")
		return
	}
	active, err := h.queries.GetActiveStepForJob(ctx, job.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		logging.Error(ctx, err, "skip AI step: active step lookup failed", "job-skip-ai-step-lookup-failed", logging.JobID(job.ID))
		httputil.ServerError(w, "skip", "AI step")
		return
	}
	if err != nil || active.StepType != stepdef.TypeHybrid {
		httputil.Error(w, http.StatusConflict, "only a step a person works after the AI can skip it; complete the step instead")
		return
	}

	dismissed, err := workflow.DismissFailure(ctx, h.queries, job.ID)
	if err != nil {
		logging.Error(ctx, err, "skip AI step: dismissing the failure failed", "job-skip-ai-step-dismiss-failed", logging.JobID(job.ID))
		httputil.ServerError(w, "skip", "AI step")
		return
	}
	if dismissed == nil {
		// A concurrent re-run or completion has already cleared the failure.
		httputil.Error(w, http.StatusConflict, "this task has no AI step error to skip")
		return
	}

	h.emitJobEvent(ctx, projectevents.TypeJobAIStepSkipped, job, map[string]any{
		"error_dismissed": projectevents.ShortMessage(*dismissed),
	})
	httputil.NoContent(w)
}
