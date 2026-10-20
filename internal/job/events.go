package job

// events.go — project_events emission for job-scoped actions

import (
	"context"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/projectevents"
)

// emitJobEvent records a project_events row for a job-scoped action.
// Best-effort throughout: the file lookup only serves audit correlation
// (project id + file name), so a failed lookup skips the event and never
// blocks the canonical write.
func (s *Service) emitJobEvent(ctx context.Context, eventType string, job db.Job, details map[string]any) {
	tenantID := auth.TenantIDFromContext(ctx)
	file, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: job.FileID, TenantID: tenantID})
	if err != nil {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	details["file_name"] = file.Name
	details["target_language"] = job.TargetLanguage
	actorID, actorName := projectevents.ActorFromContext(ctx)
	jobID := job.ID
	fileID := job.FileID
	projectevents.Emit(ctx, s.queries, projectevents.Event{
		ProjectID: file.ProjectID,
		TenantID:  tenantID,
		Type:      eventType,
		ActorID:   actorID,
		ActorName: actorName,
		FileID:    &fileID,
		JobID:     &jobID,
		Details:   details,
	})
}
