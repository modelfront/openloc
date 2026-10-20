// Package projectevents is the project-level audit log: one row in
// project_events per user-visible project mutation (creation, metadata and
// settings changes, assignment swaps, file lifecycle, job/workflow actions).
//
// Segment-level edits stay in translation_history — never duplicate
// them here. Rows CASCADE with project deletion, so there is deliberately no
// "project.deleted" event. TRANSIENT worker-side status flips (queued/
// running/progress) are not logged either: the user-triggered enqueue and the
// terminal outcome (job.pipeline_completed / job.pipeline_failed) are each
// one event; the churn in between lives on jobs.status.
//
// Two emit flavors, mirroring pipeline/translation_events.go:
//
//   - EmitInTx: caller already owns a tx; the audit row commits atomically
//     with the parent write and the error propagates so the caller can roll
//     back on emission failure.
//   - Emit: best-effort for call sites without a transaction; logs +
//     swallows.
package projectevents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// Event type taxonomy — the source of truth for project_events.event_type.
// Text column, no enum: adding a type is a constant + an emission site, not a
// migration. Naming: "<entity>.<past-tense action>".
const (
	TypeProjectCreated     = "project.created"
	TypeProjectPublished   = "project.published" // a draft published by someone other than its starter
	TypeMetadataUpdated    = "project.metadata_updated"
	TypeSettingsUpdated    = "project.settings_updated"
	TypeTMAssignmentsSet   = "project.tm_assignments_set"
	TypeMTAssignmentsSet   = "project.mt_assignments_set"
	TypeTermAssignmentsSet = "project.terminology_assignments_set"
	TypeWorkflowStepsSet   = "project.workflow_steps_set"
	TypeTemplateApplied    = "project.template_applied"
	TypeSavedAsTemplate    = "project.saved_as_template"
	// Library workflow copied onto the project / project steps promoted into
	// the library.
	TypeWorkflowApplied     = "project.workflow_applied"
	TypeWorkflowSavedAs     = "project.workflow_saved_as"
	TypeWorkflowDetached    = "project.workflow_detached"
	TypeTranslationsCleared = "project.translations_cleared"
	TypePretranslateStarted = "project.pretranslate_started"
	// Status changes made by the auto-complete rule rather than by a human
	// picking a status. Attributed to whoever performed
	// the action that triggered the re-evaluation.
	TypeAutoCompleted = "project.auto_completed"
	TypeAutoReopened  = "project.auto_reopened"

	TypeFileUploaded   = "file.uploaded"
	TypeFileRenamed    = "file.renamed"
	TypeFileDeleted    = "file.deleted"
	TypeFileReimported = "file.reimported"
	// Download of the untouched upload (the "Source file" option in the
	// Download menu). Distinct from job.exported, which covers deliverables
	// reconstructed from translations.
	TypeFileDownloaded = "file.downloaded"

	TypeJobTMApplyStarted = "job.tm_apply_started"
	TypeJobMTApplyStarted = "job.mt_apply_started"
	TypeJobAIApplyStarted = "job.ai_apply_started"
	TypeJobCleared        = "job.translations_cleared"
	TypeJobDeleted        = "job.deleted"
	TypeJobAssigned       = "job.assigned"
	// Async pipeline outcomes, emitted by the worker (system events — the
	// human trigger is already recorded by the upload / apply / bulk
	// pretranslate event; details.triggered_by_email carries the attribution).
	// Transient queued/running flips stay on jobs.status, not here.
	TypeJobPipelineCompleted = "job.pipeline_completed"
	TypeJobPipelineFailed    = "job.pipeline_failed"
	// A run ended early because a user asked it to (POST /jobs/{id}/stop).
	// details: phase, translated_count, total_count. Saved work is kept.
	TypeJobPipelineStopped = "job.pipeline_stopped"
	// The stop request itself — a user action, so the actor is the requester.
	TypeJobStopRequested = "job.stop_requested"
	// The user-facing "Complete" action (the sole completed-flip).
	TypeJobCompleted = "job.completed"
	// Deliverable download — details.format distinguishes the final-file
	// export ("source") from the bilingual XLIFF hand-off ("xliff").
	TypeJobExported = "job.exported"
	// A failed job's automation was queued to run again.
	TypeJobRerunStarted = "job.rerun_started"
	// A manager skipped a failed AI run on a hybrid step: the error is
	// cleared and the person works the step without the AI pass.
	TypeJobAIStepSkipped = "job.ai_step_skipped"

	TypeStepAdvanced = "workflow.step_advanced"
	// An `ai` step completed itself after its QE+APE run.
	TypeStepAutoCompleted = "workflow.step_auto_completed"
	TypeStepRejected      = "workflow.step_rejected"
	TypeStepAssigned      = "workflow.step_assigned"
	TypeStepDeadlineSet   = "workflow.step_deadline_set"
	// A manager edited a segment on a completed task, which put it back in
	// progress (and reopened the job when it was completed).
	TypeStepReopened = "workflow.step_reopened"

	TypeAnalysisCreated = "analysis.created"
	TypeAnalysisDeleted = "analysis.deleted"
)

// Event is one audit entry. FileID/JobID are correlation ids (no FK — the
// event must survive entity deletion), so Details must always carry the
// human-readable snapshots needed to render the event later (file_name,
// target_language, ...).
type Event struct {
	ProjectID int64
	TenantID  int64
	Type      string
	ActorID   *int64 // nil = system action
	// ActorName is snapshotted into details.actor_name so events still render
	// after the user row is deleted (the actor_id FK is ON DELETE SET NULL).
	ActorName string
	FileID    *int64
	JobID     *int64
	Details   map[string]any // nil → '{}'
}

// ActorFromContext extracts the audit actor from a request context: the user
// id from auth claims (nil when absent — a system action) and the email as
// the render-safe name snapshot. Never panics, so it is also safe from MCP /
// worker contexts where claims may be missing.
func ActorFromContext(ctx context.Context) (*int64, string) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, ""
	}
	uid := claims.UserID
	return &uid, auth.UserEmailFromContext(ctx)
}

// PersonName is how History names a user: their display name, or their email
// when they have none. Events snapshot it, so the row reads the same after the
// user changes their name.
func PersonName(u db.User) string {
	if name := strings.TrimSpace(u.Name); name != "" {
		return name
	}
	return u.Email
}

// EmitInTx inserts the audit row on the caller's tx-bound Querier and returns
// the error so the caller can roll back the parent write on failure — inside
// a tx, audit+write atomicity is one `if err` away, so take it.
func EmitInTx(ctx context.Context, q db.Querier, ev Event) error {
	if q == nil {
		return fmt.Errorf("nil querier")
	}
	return insert(ctx, q, ev)
}

// Emit is best-effort for call sites without a transaction: a failed audit
// insert after an already-successful canonical write shouldn't fail the
// request. Logs + swallows.
func Emit(ctx context.Context, q db.Querier, ev Event) {
	if q == nil {
		logging.Warn(ctx, fmt.Errorf("nil querier"), "project event emit skipped", "project-event-emit-failed",
			logging.ProjectID(ev.ProjectID), logging.Status(ev.Type))
		return
	}
	if err := insert(ctx, q, ev); err != nil {
		logging.Warn(ctx, err, "project event emit failed", "project-event-emit-failed",
			logging.ProjectID(ev.ProjectID), logging.Status(ev.Type))
	}
}

func insert(ctx context.Context, q db.Querier, ev Event) error {
	details := ev.Details
	if ev.ActorName != "" {
		if details == nil {
			details = map[string]any{}
		} else {
			// Copy so we never mutate a caller-owned map.
			copied := make(map[string]any, len(details)+1)
			for k, v := range details {
				copied[k] = v
			}
			details = copied
		}
		details["actor_name"] = ev.ActorName
	}
	detailsBytes, err := marshalDetails(details)
	if err != nil {
		return fmt.Errorf("marshal details: %w", err)
	}
	if _, err := q.InsertProjectEvent(ctx, db.InsertProjectEventParams{
		ProjectID: ev.ProjectID,
		TenantID:  ev.TenantID,
		EventType: ev.Type,
		ActorID:   ev.ActorID,
		FileID:    ev.FileID,
		JobID:     ev.JobID,
		Details:   detailsBytes,
	}); err != nil {
		return fmt.Errorf("insert project event: %w", err)
	}
	return nil
}

func marshalDetails(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil // sqlc COALESCEs to '{}'
	}
	return json.Marshal(m)
}

// CompleteCreated replaces the details of the project's project.created event,
// keeping its actor and its actor_name / via snapshots. Reports false when the
// project has no such event.
func CompleteCreated(ctx context.Context, q db.Querier, projectID, tenantID int64, details map[string]any) (bool, error) {
	b, err := json.Marshal(details)
	if err != nil {
		return false, fmt.Errorf("marshal details: %w", err)
	}
	n, err := q.CompleteProjectCreatedEvent(ctx, db.CompleteProjectCreatedEventParams{
		Details: b, ProjectID: projectID, TenantID: tenantID,
	})
	if err != nil {
		return false, fmt.Errorf("complete project.created: %w", err)
	}
	return n > 0, nil
}
