package translation

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/segmentbulk"
)

// HandleConfirmSegments confirms the editor's selection:
// POST /api/jobs/{jobID}/translations/confirm. Same gate as a single confirm —
// any member with access to the job.
func (h *Handler) HandleConfirmSegments(w http.ResponseWriter, r *http.Request) {
	job, ids, ok := h.jobSelection(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	res, err := h.ConfirmSelection(ctx, job, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), ids)
	if httputil.ServiceError(w, err, "confirm", "translations") {
		return
	}
	httputil.Data(w, res)
}

// HandleConfirmAll confirms every translated, unapproved segment of the job:
// POST /api/jobs/{jobID}/translations/confirm-all, from the Complete step
// dialog's "Approve all and complete". Same gate as a bulk confirm — any
// member with access to the job.
func (h *Handler) HandleConfirmAll(w http.ResponseWriter, r *http.Request) {
	job, ok := h.routeJob(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	res, err := h.ConfirmJob(ctx, job, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx))
	if httputil.ServiceError(w, err, "confirm", "translations") {
		return
	}
	httputil.Data(w, res)
}

// HandleLockSegments locks and approves the editor's selection:
// POST /api/jobs/{jobID}/translations/lock. Managers only (route gate).
func (h *Handler) HandleLockSegments(w http.ResponseWriter, r *http.Request) {
	job, ids, ok := h.jobSelection(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	res, err := h.LockSelection(ctx, job, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), ids)
	if httputil.ServiceError(w, err, "lock", "translations") {
		return
	}
	httputil.Data(w, res)
}

// HandleUnlockSegments unlocks the editor's selection:
// POST /api/jobs/{jobID}/translations/unlock. Any member with access to the
// job — a translator unlocks a segment to edit it.
func (h *Handler) HandleUnlockSegments(w http.ResponseWriter, r *http.Request) {
	job, ids, ok := h.jobSelection(w, r)
	if !ok {
		return
	}
	res, err := h.UnlockSelection(r.Context(), job, auth.TenantIDFromContext(r.Context()), auth.UserIDFromContext(r.Context()), ids)
	if httputil.ServiceError(w, err, "unlock", "translations") {
		return
	}
	httputil.Data(w, res)
}

// routeJob resolves the route's job tenant-scoped. Writes the error response
// and returns false on failure.
func (h *Handler) routeJob(w http.ResponseWriter, r *http.Request) (db.Job, bool) {
	ctx := r.Context()
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return db.Job{}, false
	}
	job, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: auth.TenantIDFromContext(ctx)})
	if httputil.DBError(w, err, "job") {
		return db.Job{}, false
	}
	return job, true
}

// jobSelection resolves the route's job and decodes the selection body shared
// by the bulk segment routes.
func (h *Handler) jobSelection(w http.ResponseWriter, r *http.Request) (db.Job, []int64, bool) {
	job, ok := h.routeJob(w, r)
	if !ok {
		return db.Job{}, nil, false
	}
	var req segmentbulk.Request
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return db.Job{}, nil, false
	}
	return job, req.TranslationIDs, true
}
