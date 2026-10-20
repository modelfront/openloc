package workflow

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// HandleListProjectTasks returns every step assignment of every job in the
// project — one row per (job, workflow step). It backs the project page's
// task matrix, which needs every step at once, while the jobs list only
// carries each job's active step.
//
// Access: managers (owner / admin / project manager), gated at the route like
// the sibling GET /api/projects/{projectID}/jobs. The project is resolved
// tenant-scoped first so a foreign id is a 404, not an empty list.
func (h *Handler) HandleListProjectTasks(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
		ID: projectID, TenantID: tenantID,
	}); err != nil {
		if httputil.DBError(w, err, "project") {
			return
		}
		httputil.ServerError(w, "get", "project")
		return
	}

	tasks, err := h.queries.ListProjectTasks(r.Context(), db.ListProjectTasksParams{
		ProjectID: projectID, TenantID: tenantID,
	})
	if err != nil {
		httputil.ServerError(w, "list", "project tasks")
		return
	}
	httputil.Data(w, tasks)
}
