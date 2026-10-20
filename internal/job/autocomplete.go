package job

import (
	"context"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/project"
)

// reevaluateAutoComplete re-runs the owning project's auto-complete rule after
// this job's set or status changed. Takes a file id rather
// than a project id because jobs carry file_id, not project_id.
//
// Best-effort: a lookup failure leaves the project status alone rather than
// failing the caller's action.
//
// TODO(soft-delete): once jobs carry a tombstone, the
// delete caller moves to just after the deleted_at write, and the restore path
// needs a call of its own.
func (s *Service) reevaluateAutoComplete(ctx context.Context, fileID int64) {
	tenantID := auth.TenantIDFromContext(ctx)
	file, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{
		ID: fileID, TenantID: tenantID,
	})
	if err != nil {
		return
	}
	project.Reevaluate(ctx, s.queries, tenantID, file.ProjectID)
}
