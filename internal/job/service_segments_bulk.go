package job

import (
	"context"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/projectevents"
	"openloc/internal/segmentbulk"
)

// ClearSegments reverts the given rows of the job to untranslated, whatever
// their status. Same gates and transaction as a job-wide clear; locked and
// empty rows, and rows another user holds, are skipped, and each cleared row
// gets a `cleared` history event.
func (s *Service) ClearSegments(ctx context.Context, job db.Job, ids []int64) (segmentbulk.Result, error) {
	ids, err := segmentbulk.NormalizeIDs(ids)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	if err := s.preflightClearJob(ctx, job); err != nil {
		return segmentbulk.Result{}, err
	}
	userID := auth.UserIDFromContext(ctx)
	rows, err := segmentbulk.JobRows(ctx, s.queries, job, userID, ids)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	free := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !rows[id].HeldByOther {
			free = append(free, id)
		}
	}
	cleared, written, err := s.clearRows(ctx, job, clearRowsOpts{scopeAll: true, ids: free, historyBy: &userID})
	if err != nil {
		return segmentbulk.Result{}, err
	}
	res := segmentbulk.Split(ids, written, rows, func(r segmentbulk.Row) segmentbulk.SkipReason {
		switch {
		case r.HeldByOther:
			return segmentbulk.Held
		case r.Locked:
			return segmentbulk.Locked
		case r.ActiveText == nil:
			return segmentbulk.Empty
		default:
			// Locked or emptied between the read and the write.
			return segmentbulk.Conflict
		}
	})
	res.WorkflowReopened = cleared.WorkflowReopened
	s.emitJobEvent(ctx, projectevents.TypeJobCleared, job, map[string]any{
		"scope": "selection", "cleared": cleared.Cleared, "workflow_reopened": cleared.WorkflowReopened,
	})
	return res, nil
}
