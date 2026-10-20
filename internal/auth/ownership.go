package auth

import (
	"context"
	"net/http"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// RequireProjectOwnership returns middleware that verifies the {projectID} URL param
// belongs to the current tenant. On success, stores the project in the request context
// so handlers can read it via ProjectFromContext() without re-fetching.
//
// Apply to route groups that contain {projectID}:
//
//	r.Route("/api/projects/{projectID}", func(r chi.Router) {
//	    r.Use(auth.RequireProjectOwnership(queries))
//	    // all routes here are guaranteed to be tenant-scoped
//	})
func RequireProjectOwnership(queries db.Querier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			projectID, err := httputil.ParseID(r, "projectID")
			if err != nil {
				httputil.BadRequest(w, "invalid project ID")
				return
			}
			tenantID := TenantIDFromContext(r.Context())
			project, err := queries.GetProject(r.Context(), db.GetProjectParams{
				ID: projectID, TenantID: tenantID,
			})
			if err != nil {
				httputil.Error(w, http.StatusNotFound, "project not found")
				return
			}
			ctx := context.WithValue(r.Context(), projectKey{}, &project)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ProjectFromContext returns the verified project from the context.
// Only available after RequireProjectOwnership middleware has run.
func ProjectFromContext(ctx context.Context) *db.Project {
	p, _ := ctx.Value(projectKey{}).(*db.Project)
	return p
}

// RequireTMOwnership returns middleware that verifies the {tmID} URL param
// belongs to the current tenant.
func RequireTMOwnership(queries db.Querier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tmID, err := httputil.ParseID(r, "tmID")
			if err != nil {
				httputil.BadRequest(w, "invalid TM ID")
				return
			}
			tenantID := TenantIDFromContext(r.Context())
			_, err = queries.GetTMCollection(r.Context(), db.GetTMCollectionParams{
				ID: tmID, TenantID: tenantID,
			})
			if err != nil {
				httputil.Error(w, http.StatusNotFound, "TM not found")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireFileOwnership returns middleware that verifies the {fileID} URL param
// belongs to the current tenant (via GetFileTenant).
func RequireFileOwnership(queries db.Querier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fileID, err := httputil.ParseID(r, "fileID")
			if err != nil {
				httputil.BadRequest(w, "invalid file ID")
				return
			}
			tenantID := TenantIDFromContext(r.Context())
			file, err := queries.GetFileTenant(r.Context(), db.GetFileTenantParams{
				ID: fileID, TenantID: tenantID,
			})
			if err != nil {
				httputil.Error(w, http.StatusNotFound, "file not found")
				return
			}
			// The row is already in hand for the ownership check, so recording
			// the project it belongs to is free. Without it a notice
			// for /api/files/{fileID}/* names the file and nothing around it.
			logging.Attach(r.Context(), logging.ProjectID(file.ProjectID))
			next.ServeHTTP(w, r)
		})
	}
}

// RequireJobOwnership returns middleware that verifies the {jobID} URL param
// belongs to the current tenant (via GetJobTenant — joins through files +
// projects to projects.tenant_id).
//
// Apply to route groups that contain {jobID}:
//
//	r.Route("/api/jobs/{jobID}", func(r chi.Router) {
//	    r.Use(auth.RequireJobOwnership(queries))
//	    // all routes here are guaranteed to be tenant-scoped
//	})
//
// Pairs with handler-level GetJobTenant calls (defense-in-depth — matches the
// established RequireTMOwnership + inline-GetTMCollection pattern in the TM
// handler).
func RequireJobOwnership(queries db.Querier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jobID, err := httputil.ParseID(r, "jobID")
			if err != nil {
				httputil.BadRequest(w, "invalid job ID")
				return
			}
			tenantID := TenantIDFromContext(r.Context())
			// GetJobOwnershipContext rather than GetJobTenant: identical joins
			// and identical cost, but it also returns the project and file the
			// job hangs off, which /api/jobs/{jobID}/* otherwise never reveals
			job, err := queries.GetJobOwnershipContext(r.Context(), db.GetJobOwnershipContextParams{
				ID: jobID, TenantID: tenantID,
			})
			if err != nil {
				httputil.Error(w, http.StatusNotFound, "job not found")
				return
			}
			logging.Attach(r.Context(),
				logging.ProjectID(job.ProjectID),
				logging.FileID(job.FileID),
				logging.TargetLang(job.TargetLanguage),
			)
			next.ServeHTTP(w, r)
		})
	}
}

// VerifyProjectOwnership is a helper for endpoints that take project_id as a query parameter
// (not a URL param). Returns the verified project or writes 404 and returns nil.
func VerifyProjectOwnership(w http.ResponseWriter, r *http.Request, queries db.Querier, projectID int64) *db.Project {
	tenantID := TenantIDFromContext(r.Context())
	project, err := queries.GetProject(r.Context(), db.GetProjectParams{
		ID: projectID, TenantID: tenantID,
	})
	if err != nil {
		httputil.Error(w, http.StatusNotFound, "project not found")
		return nil
	}
	return &project
}
