package worker

// events.go — project_events emission for async pipeline outcomes.
// Worker contexts carry NO auth claims, so tenant and
// attribution come from the (validated) task payload, never from the context —
// auth.TenantIDFromContext would panic here.

import (
	"context"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/projectevents"
)

// emitJobPipelineEvent records a pipeline outcome for a job as a SYSTEM event
// (nil actor — the human trigger was already recorded when the run was
// requested; the triggering user's email rides in details for display).
// Best-effort throughout, on a fresh short-lived context: failure paths often
// run on an already-cancelled task context.
//
// Failure events come only from failRun, which emits on the final attempt: a
// task that fails transiently and succeeds on retry would otherwise leave a
// misleading job.pipeline_failed row per attempt next to the eventual
// job.pipeline_completed.
func (h *Handler) emitJobPipelineEvent(taskCtx context.Context, tenantID, jobID int64, eventType string, triggeredBy *int64, details map[string]any) {
	if h.queries == nil { // nil-safe like the other optional worker wiring
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(taskCtx), 5*time.Second)
	defer cancel()

	job, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return // audit-only lookup; the job may already be deleted
	}
	file, err := h.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: job.FileID, TenantID: tenantID})
	if err != nil {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	details["file_name"] = file.Name
	details["target_language"] = job.TargetLanguage
	if triggeredBy != nil {
		// GetUser is unscoped by design here: triggeredBy comes from a
		// server-generated task payload (never a client), and the lookup only
		// snapshots a name and email for display. The job/file lookups above are the
		// tenant gates.
		if u, uErr := h.queries.GetUser(ctx, *triggeredBy); uErr == nil {
			details["triggered_by_email"] = u.Email
			details["triggered_by_name"] = projectevents.PersonName(u)
		}
	}
	fileID := job.FileID
	jID := job.ID
	projectevents.Emit(ctx, h.queries, projectevents.Event{
		ProjectID: file.ProjectID,
		TenantID:  tenantID,
		Type:      eventType,
		FileID:    &fileID,
		JobID:     &jID,
		Details:   details,
	})
}
