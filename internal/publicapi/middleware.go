package publicapi

import (
	"context"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

type fileCtxKey struct{}

// requireFileInProject makes /projects/{projectID}/files/{fileID} answer 404
// unless the file belongs to that project in the caller's tenant, and hands
// the file row to the handler.
func (h Handlers) requireFileInProject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectID, err := httputil.ParseID(r, "projectID")
		if err != nil {
			httputil.BadRequest(w, "invalid project ID")
			return
		}
		fileID, err := httputil.ParseID(r, "fileID")
		if err != nil {
			httputil.BadRequest(w, "invalid file ID")
			return
		}
		f, err := h.Queries.GetFileTenant(r.Context(), db.GetFileTenantParams{ID: fileID, TenantID: auth.TenantIDFromContext(r.Context())})
		if httputil.DBError(w, err, "file") {
			return
		}
		if f.ProjectID != projectID {
			httputil.NotFound(w, "file")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), fileCtxKey{}, f)))
	})
}

func fileFromContext(r *http.Request) db.File {
	f, _ := r.Context().Value(fileCtxKey{}).(db.File)
	return f
}
