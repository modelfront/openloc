package publicapi

import (
	"net/http"

	"openloc/internal/auth"
	"openloc/internal/httputil"
)

func (h Handlers) getJob(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	h.writeJob(w, r, id)
}

func (h Handlers) assignJob(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := httputil.Decode(r, &req); err != nil || req.UserID == 0 {
		httputil.FieldError(w, http.StatusBadRequest, "user_id", "user_id is required")
		return
	}
	err = h.Jobs.AssignJob(r.Context(), auth.TenantIDFromContext(r.Context()), id, req.UserID)
	if httputil.ServiceError(w, err, "assign", "job") {
		return
	}
	h.writeJob(w, r, id)
}

// writeJob writes the v1 job; an unknown or foreign job is 404.
func (h Handlers) writeJob(w http.ResponseWriter, r *http.Request, id int64) {
	view, err := h.Jobs.Detail(r.Context(), auth.TenantIDFromContext(r.Context()), id)
	if httputil.DBError(w, err, "job") {
		return
	}
	httputil.Data(w, jobDTO(view))
}
