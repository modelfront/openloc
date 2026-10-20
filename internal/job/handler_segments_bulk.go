package job

import (
	"net/http"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/segmentbulk"
)

// decodeBulkSegments resolves the job and reads the id list. Writes the error
// response and returns false on failure.
func (h *Handler) decodeBulkSegments(w http.ResponseWriter, r *http.Request) (db.Job, []int64, bool) {
	job, _, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return db.Job{}, nil, false
	}
	var req segmentbulk.Request
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return db.Job{}, nil, false
	}
	return job, req.TranslationIDs, true
}

// HandleClearSegments clears the selected rows. Same gate as the job-wide
// clear: managers and the job's assigned linguist, first step only.
func (h *Handler) HandleClearSegments(w http.ResponseWriter, r *http.Request) {
	job, ids, ok := h.decodeBulkSegments(w, r)
	if !ok {
		return
	}
	res, err := h.ClearSegments(r.Context(), job, ids)
	if httputil.ServiceError(w, err, "clear", "translations") {
		return
	}
	httputil.Data(w, res)
}
