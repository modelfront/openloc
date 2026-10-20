package file

import (
	"context"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/projectevents"
)

// Rename changes a file's display name and records a project.file_renamed
// event when it changed. Shared by the REST rename and the MCP rename_file
// tool. A blank name is an httputil.ClientError; an unknown or foreign file is
// pgx.ErrNoRows.
func Rename(ctx context.Context, q db.Querier, tenantID, id int64, name string) (db.File, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return db.File{}, httputil.NewClientError("name is required")
	}
	// The old name is the "from" side of the event and the tenant gate.
	old, err := q.GetFileTenant(ctx, db.GetFileTenantParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.File{}, err
	}
	f, err := q.RenameFileTenant(ctx, db.RenameFileTenantParams{ID: id, Name: name, TenantID: tenantID})
	if err != nil {
		return db.File{}, err
	}
	if old.Name != f.Name {
		actorID, actorName := projectevents.ActorFromContext(ctx)
		projectevents.Emit(ctx, q, projectevents.Event{
			ProjectID: f.ProjectID,
			TenantID:  tenantID,
			Type:      projectevents.TypeFileRenamed,
			ActorID:   actorID,
			ActorName: actorName,
			FileID:    &f.ID,
			Details: map[string]any{
				"changes": map[string]projectevents.Change{"name": {From: old.Name, To: f.Name}},
			},
		})
	}
	return f, nil
}
