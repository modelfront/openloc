package segmentedit

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Handler serves split and join for the CAT editor. The routes sit under
// /api/jobs/{jobID}, behind RequireJobOwnership and RequireLinguistJobAccess:
// owners, admins, project managers and the linguists assigned to the job may
// split and join while the job's task takes edits.
type Handler struct {
	queries db.Querier
	svc     *Service
}

func NewHandler(q db.Querier, svc *Service) *Handler {
	return &Handler{queries: q, svc: svc}
}

// HandleSplit answers POST /api/jobs/{jobID}/segments/split.
func (h *Handler) HandleSplit(w http.ResponseWriter, r *http.Request) {
	job, ok := h.job(w, r)
	if !ok {
		return
	}
	var req SplitRequest
	if err := httputil.Decode(r, &req); err != nil || req.TranslationID == 0 || req.ExpectedUpdatedAt.IsZero() {
		httputil.BadRequest(w, "translation_id, offset and expected_updated_at are required")
		return
	}
	ctx := r.Context()
	res, err := h.svc.Split(ctx, job, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), req)
	if httputil.ServiceError(w, err, "split", "segment") {
		return
	}
	httputil.Data(w, res)
}

// HandleJoin answers POST /api/jobs/{jobID}/segments/join.
func (h *Handler) HandleJoin(w http.ResponseWriter, r *http.Request) {
	job, ok := h.job(w, r)
	if !ok {
		return
	}
	var req JoinRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	for _, row := range req.Rows {
		if row.TranslationID == 0 || row.ExpectedUpdatedAt.IsZero() {
			httputil.BadRequest(w, "every row needs translation_id and expected_updated_at")
			return
		}
	}
	ctx := r.Context()
	res, err := h.svc.Join(ctx, job, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), req)
	if httputil.ServiceError(w, err, "join", "segments") {
		return
	}
	httputil.Data(w, res)
}

// job resolves the route's job in the caller's tenant.
func (h *Handler) job(w http.ResponseWriter, r *http.Request) (db.Job, bool) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return db.Job{}, false
	}
	job, err := h.queries.GetJobTenant(r.Context(), db.GetJobTenantParams{ID: jobID, TenantID: auth.TenantIDFromContext(r.Context())})
	if httputil.DBError(w, err, "job") {
		return db.Job{}, false
	}
	return job, true
}
