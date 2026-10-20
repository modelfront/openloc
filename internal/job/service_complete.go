package job

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/projectevents"
)

// CompleteResult is what completing a job did.
type CompleteResult struct {
	ApprovedCount int  `json:"approved_count"`
	TMSavedCount  int  `json:"tm_saved_count"`
	NoWriteTMs    bool `json:"no_write_tms"`
}

// CompleteJob marks a job completed — the only place a job becomes
// "completed". It writes the human-approved segments to the write TMs.
// With a workflow, only the final step's assignee may
// complete, and only on the final step (or after it closed).
func (s *Service) CompleteJob(ctx context.Context, tenantID, userID int64, job db.Job, sourceLang string) (CompleteResult, error) {
	if job.Status == "completed" {
		return CompleteResult{}, httputil.NewConflictError("job is already completed")
	}
	if job.CurrentStepID != nil {
		active, err := s.queries.GetCurrentStepForJob(ctx, *job.CurrentStepID)
		if err == nil {
			if _, nextErr := s.queries.GetNextStepAssignment(ctx, active.ID); !errors.Is(nextErr, pgx.ErrNoRows) {
				return CompleteResult{}, httputil.NewClientError("cannot complete job — workflow is not on the final step")
			}
			if active.AssignedTo == nil || *active.AssignedTo != userID {
				return CompleteResult{}, httputil.NewForbiddenError("only the assigned user can complete the final step")
			}
		}
	}

	tm, err := s.pipeline.SaveApprovedToTM(ctx, tenantID, job.FileID, sourceLang, job.TargetLanguage)
	if err != nil {
		logging.Error(ctx, err, "TM write-back failed during job completion", "job-complete-tm-writeback-failed", logging.JobID(job.ID))
		return CompleteResult{}, fmt.Errorf("tm write-back: %w", err)
	}

	s.setJobStatus(job.ID, "completed", "completed")
	s.emitJobEvent(ctx, projectevents.TypeJobCompleted, job, map[string]any{
		"approved_count": tm.ApprovedCount, "tm_saved_count": tm.TMSavedCount,
	})
	// Completes the project if this was its last unfinished job.
	s.reevaluateAutoComplete(ctx, job.FileID)
	return CompleteResult{ApprovedCount: tm.ApprovedCount, TMSavedCount: tm.TMSavedCount, NoWriteTMs: tm.NoWriteTMs}, nil
}
