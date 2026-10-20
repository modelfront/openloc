package translation

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/stepround"
)

// HandleStepStatuses serves GET /api/translations/{translationID}/step-statuses
// — how every finished workflow step round left one segment, oldest first.
//
// Access: same as segment history (managers and owners/admins on any segment,
// linguists on their assigned segments, via RequireLinguistTranslationAccess
// on the route). The rows carry tenant_id and the query filters on it; the
// parent translation is resolved tenant-scoped first so a miss is a 404.
func (h *Handler) HandleStepStatuses(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "translationID")
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	if _, err := h.queries.GetTranslationTenant(ctx, db.GetTranslationTenantParams{ID: id, TenantID: tenantID}); httputil.DBError(w, err, "translation") {
		return
	}
	rows, err := h.queries.ListTranslationStepStatuses(ctx, db.ListTranslationStepStatusesParams{TranslationID: id, TenantID: tenantID})
	if err != nil {
		logging.Error(ctx, err, "segment step statuses read failed", "segment-step-statuses-list-failed",
			logging.TranslationID(id))
		httputil.ServerError(w, "list", "step statuses")
		return
	}
	out := make([]stepround.Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, stepround.NewEntry(r.StepName, r.StepOrder, r.StepType, r.Round, r.EndedAt,
			r.EndReason, r.Status, r.PredictOutcome, r.HumanEdited))
	}
	httputil.Data(w, out)
}
