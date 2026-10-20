package pipeline

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/projectevents"
)

// jobExportedEvent is the job.exported history event for one delivered export
// of file in targetLang: format "source" is the final file, "xliff" the
// bilingual hand-off. jobID is nil when the file has no job for the language.
// The caller sets the actor.
func jobExportedEvent(tenantID int64, file db.File, jobID *int64, targetLang, format string) projectevents.Event {
	fileID := file.ID
	return projectevents.Event{
		ProjectID: file.ProjectID,
		TenantID:  tenantID,
		Type:      projectevents.TypeJobExported,
		FileID:    &fileID,
		JobID:     jobID,
		Details: map[string]any{
			"format":          format,
			"file_name":       file.Name,
			"target_language": targetLang,
		},
	}
}

// RecordFileExported records a served export in the project history, as the
// job export and the batch download do: the export route, and an export an AI
// tool reads as content. Best-effort like every job.exported event:
// the file has been exported either way, so a failed job lookup only leaves
// the event without its job.
func (s *Service) RecordFileExported(ctx context.Context, file db.File, lang, format string) {
	var jobID *int64
	job, err := s.queries.GetJobByFileAndLang(ctx, db.GetJobByFileAndLangParams{FileID: file.ID, TargetLanguage: lang})
	switch {
	case err == nil:
		jobID = &job.ID
	case !errors.Is(err, pgx.ErrNoRows):
		logging.Warn(ctx, err, "export history event has no job: job lookup failed", "file-export-event-job-lookup-failed",
			logging.FileID(file.ID), logging.TargetLang(lang))
	}
	ev := jobExportedEvent(auth.TenantIDFromContext(ctx), file, jobID, lang, format)
	ev.ActorID, ev.ActorName = projectevents.ActorFromContext(ctx)
	projectevents.Emit(ctx, s.queries, ev)
}
