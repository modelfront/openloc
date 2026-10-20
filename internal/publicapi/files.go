package publicapi

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

func (h Handlers) listFiles(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	_, err = h.Queries.GetProject(r.Context(), db.GetProjectParams{ID: projectID, TenantID: auth.TenantIDFromContext(r.Context())})
	if httputil.DBError(w, err, "project") {
		return
	}
	page := httputil.ParseCursor(r, listMaxLimit)
	rows, err := h.Queries.ListFilesPaginated(r.Context(), db.ListFilesPaginatedParams{
		ProjectID: projectID,
		Cursor:    page.Cursor,
		PageLimit: page.Limit + 1, // +1 to detect has_more
	})
	if err != nil {
		httputil.ServerError(w, "list", "files")
		return
	}
	writePage(w, rows, page.Limit, func(f db.File) int64 { return f.ID }, fileDTO)
}

func (h Handlers) uploadFile(w http.ResponseWriter, r *http.Request) {
	res, ok := h.Files.Upload(w, r)
	if !ok {
		return
	}
	resp := map[string]any{"data": fileDTO(res.File)}
	if len(res.Warnings) > 0 {
		resp["warnings"] = res.Warnings
	}
	httputil.JSON(w, http.StatusCreated, resp)
}

func (h Handlers) getFile(w http.ResponseWriter, r *http.Request) {
	httputil.Data(w, fileDTO(fileFromContext(r)))
}
