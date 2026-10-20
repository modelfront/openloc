package segment

import (
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"net/http"
)

type Handler struct {
	queries db.Querier
}

func NewHandler(q db.Querier) *Handler {
	return &Handler{queries: q}
}

func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	fileID, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	segments, err := h.queries.ListSegmentsByFileTenant(r.Context(), db.ListSegmentsByFileTenantParams{
		FileID:   fileID,
		TenantID: tenantID,
	})
	if err != nil {
		httputil.ServerError(w, "list", "segments")
		return
	}
	httputil.Data(w, segments)
}

func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	fileID, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}
	id, err := httputil.ParseID(r, "segmentID")
	if err != nil {
		httputil.BadRequest(w, "invalid segment ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	seg, err := h.queries.GetSegmentInFileTenant(r.Context(), db.GetSegmentInFileTenantParams{
		ID:       id,
		FileID:   fileID,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "segment") {
		return
	}
	httputil.Data(w, seg)
}
