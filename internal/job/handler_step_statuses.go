package job

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/stepround"
)

// PreviousStepStatus is how the last finished step round left one segment.
type PreviousStepStatus struct {
	TranslationID int64 `json:"translation_id"`
	stepround.Entry
}

// JobStepStatuses is what the editor needs to show a segment's previous step
// next to its status in the current one.
type JobStepStatuses struct {
	Previous []PreviousStepStatus `json:"previous"`
	// PredictedBeforeStep lists translations whose AI verdict came from an
	// earlier step: in this step it is history, not work to do.
	PredictedBeforeStep []int64 `json:"predicted_before_step"`
}

// HandleStepStatuses serves GET /api/jobs/{jobID}/step-statuses. Same access
// as the job's translations (the job route group's ownership and linguist
// assignment gates); both queries are tenant-scoped.
func (h *Handler) HandleStepStatuses(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	if _, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID}); httputil.DBError(w, err, "job") {
		return
	}
	prev, err := h.queries.ListJobPreviousStepStatuses(ctx, db.ListJobPreviousStepStatusesParams{JobID: jobID, TenantID: tenantID})
	if err != nil {
		logging.Error(ctx, err, "job step statuses read failed", "job-step-statuses-list-failed", logging.JobID(jobID))
		httputil.ServerError(w, "list", "step statuses")
		return
	}
	predicted, err := h.queries.ListJobPredictedBeforeStep(ctx, db.ListJobPredictedBeforeStepParams{JobID: jobID, TenantID: tenantID})
	if err != nil {
		logging.Error(ctx, err, "job step statuses read failed", "job-step-statuses-list-failed", logging.JobID(jobID))
		httputil.ServerError(w, "list", "step statuses")
		return
	}
	out := JobStepStatuses{Previous: make([]PreviousStepStatus, 0, len(prev)), PredictedBeforeStep: predicted}
	if out.PredictedBeforeStep == nil {
		out.PredictedBeforeStep = []int64{}
	}
	for _, r := range prev {
		out.Previous = append(out.Previous, PreviousStepStatus{
			TranslationID: r.TranslationID,
			Entry: stepround.NewEntry(r.StepName, r.StepOrder, r.StepType, r.Round, r.EndedAt,
				r.EndReason, r.Status, r.PredictOutcome, r.HumanEdited),
		})
	}
	httputil.Data(w, out)
}
