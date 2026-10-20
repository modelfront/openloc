package project

// Auto-complete: a project with the option on tracks its
// jobs, flipping to 'completed' when they are all done and back to
// 'in_progress' when that stops holding.
//
// The whole rule lives in ReevaluateProjectAutoComplete so two concurrent job
// completions can't race a read-modify-write, and so "nothing should change" is
// a missing row rather than a condition duplicated in Go.

import (
	"context"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/projectevents"
)

// Reevaluate re-runs the auto-complete rule for one project and records the
// resulting status change in project history, attributed to the actor in ctx.
//
// Best-effort by design: a status/audit update must never fail the user action
// that triggered it (completing a job, deleting a file), so failures are logged
// and swallowed rather than returned.
func Reevaluate(ctx context.Context, q db.Querier, tenantID, projectID int64) {
	if q == nil || projectID == 0 {
		return
	}

	updated, err := q.ReevaluateProjectAutoComplete(ctx, db.ReevaluateProjectAutoCompleteParams{
		ProjectID: projectID,
		TenantID:  tenantID,
	})
	if err != nil {
		logging.Warn(ctx, err, "project auto-complete re-evaluation failed", "project-autocomplete-failed",
			logging.ProjectID(projectID))
		return
	}
	if len(updated) == 0 {
		return // option off, or the status already matches
	}

	status := updated[0]
	eventType := projectevents.TypeAutoReopened
	if status == db.ProjectStatusCompleted {
		eventType = projectevents.TypeAutoCompleted
	}
	actorID, actorName := projectevents.ActorFromContext(ctx)
	projectevents.Emit(ctx, q, projectevents.Event{
		ProjectID: projectID,
		TenantID:  tenantID,
		Type:      eventType,
		ActorID:   actorID,
		ActorName: actorName,
		Details:   map[string]any{"status": string(status)},
	})
}
