package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/notification"
	"openloc/internal/projectevents"
	"openloc/internal/stepdef"
	"openloc/internal/stepround"
)

// RejectOpts says where a rejection sends the job.
type RejectOpts struct {
	// Reason is shown to whoever works the reopened step; optional.
	Reason string
	// ToStepID is the step assignment to reopen: any earlier step of the
	// job's workflow except a pre-translation step. Nil reopens the nearest
	// earlier human or hybrid step.
	ToStepID *int64
	// RerunAI runs the reopened hybrid step's AI again. Reopening an AI step
	// always runs it again.
	RerunAI bool
}

// RejectResult is what a rejection reopened.
type RejectResult struct {
	ReopenedStepID int64
	// RerunAI is true when the reopened step's AI was queued to run again.
	RerunAI bool
}

// Reject rejects the job's active step and reopens an earlier one. The steps
// in between are not started again. The reopened step gets back the segment
// statuses it ended with; when its AI runs again, every translated, unlocked
// segment is reviewed again on its current text instead. Running the AI again
// needs a manager, as it is paid for.
func (s *Service) Reject(ctx context.Context, tenantID, jobID int64, actor Actor, opts RejectOpts) (RejectResult, error) {
	reason, err := cleanNote("reason", opts.Reason)
	if err != nil {
		return RejectResult{}, err
	}
	active, err := s.activeStepFor(ctx, tenantID, jobID, actor, "reject")
	if err != nil {
		return RejectResult{}, err
	}
	target, err := s.rollbackTarget(ctx, active.ID, opts.ToStepID)
	if err != nil {
		return RejectResult{}, err
	}
	review := target.StepType == stepdef.TypeAI || opts.RerunAI
	if opts.RerunAI && !(stepdef.RunsAI(target.StepType) && target.HasAi) {
		return RejectResult{}, httputil.NewClientError("that step has no AI to run again")
	}
	if review && !auth.IsManagerRole(actor.Role) {
		return RejectResult{}, httputil.NewForbiddenError("only a manager can run the AI step again")
	}
	mode := stepround.Restore
	if review {
		mode = stepround.Review
	}

	var dismissed *string
	// A deadlock victim runs again: the body resets what it sets.
	err = database.RetryConflicts(ctx, "step-reject-conflict-retried", func() error {
		dismissed = nil
		return s.tx.InTx(ctx, func(q db.Querier) error {
			if _, err := q.LockJobForTaskEdit(ctx, db.LockJobForTaskEditParams{JobID: jobID, TenantID: tenantID}); err != nil {
				return fmt.Errorf("lock job: %w", err)
			}
			if err := stepround.Close(ctx, q, active.ID, stepround.Rejected); err != nil {
				return err
			}
			if err := q.RejectStepAssignment(ctx, db.RejectStepAssignmentParams{ID: active.ID, RejectionReason: reason}); err != nil {
				return fmt.Errorf("reject step: %w", err)
			}
			if err := q.ResetStepsBetween(ctx, db.ResetStepsBetweenParams{
				CurrentStepID: active.ID, TargetStepOrder: target.StepOrder,
			}); err != nil {
				return fmt.Errorf("reset steps in between: %w", err)
			}
			if err := q.ReopenStepAssignment(ctx, target.ID); err != nil {
				return fmt.Errorf("reopen step: %w", err)
			}
			if err := stepround.Open(ctx, q, jobID, target.ID, mode); err != nil {
				return err
			}
			if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &target.ID}); err != nil {
				return fmt.Errorf("update job step: %w", err)
			}
			var err error
			dismissed, err = DismissFailure(ctx, q, jobID)
			return err
		})
	})
	if err != nil {
		return RejectResult{}, err
	}

	details := map[string]any{"from_step": active.StepName, "to_step": target.StepName}
	if dismissed != nil {
		details["error_dismissed"] = projectevents.ShortMessage(*dismissed)
	}
	if reason != "" {
		details["reason"] = reason
	}
	if review {
		details["rerun_ai"] = true
	}
	s.emitStepEvent(ctx, tenantID, projectevents.TypeStepRejected, jobID, details)

	if review && s.aiEnqueuer != nil {
		if err := s.aiEnqueuer.EnqueueAIStep(jobID, tenantID, &actor.UserID); err != nil {
			logging.Warn(ctx, err, "enqueue AI step on rollback failed", "workflow-reject-ai-enqueue-failed", logging.JobID(jobID))
		}
	}
	if target.AssignedTo != nil {
		s.notifyRejection(ctx, tenantID, jobID, actor, *target.AssignedTo, target.StepName, reason)
	}
	return RejectResult{ReopenedStepID: target.ID, RerunAI: review}, nil
}

// rollbackTarget resolves the step a rejection reopens: toStepID when given,
// else the nearest earlier human or hybrid step.
func (s *Service) rollbackTarget(ctx context.Context, activeID int64, toStepID *int64) (db.GetRollbackTargetRow, error) {
	id := toStepID
	if id == nil {
		prev, err := s.queries.GetPreviousStepAssignment(ctx, activeID)
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetRollbackTargetRow{}, httputil.NewClientError("cannot reject — no previous step")
		}
		if err != nil {
			return db.GetRollbackTargetRow{}, fmt.Errorf("get previous step: %w", err)
		}
		id = &prev.ID
	}
	target, err := s.queries.GetRollbackTarget(ctx, db.GetRollbackTargetParams{CurrentStepID: activeID, TargetID: *id})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GetRollbackTargetRow{}, httputil.NewClientError("the job can only be sent back to an earlier step of its workflow")
	}
	if err != nil {
		return db.GetRollbackTargetRow{}, fmt.Errorf("get rollback target: %w", err)
	}
	if target.StepType == stepdef.TypePretranslate {
		return db.GetRollbackTargetRow{}, httputil.NewClientError("the job cannot be sent back to the pre-translation step")
	}
	return target, nil
}

// notifyRejection tells the reopened step's assignee the job came back.
func (s *Service) notifyRejection(ctx context.Context, tenantID, jobID int64, actor Actor, recipient int64, stepName, reason string) {
	pID, pName, fName, tLang := s.resolveJobContext(ctx, jobID)
	actorName := ""
	if u, err := s.queries.GetUser(ctx, actor.UserID); err == nil {
		actorName = u.Name
	}
	s.onEvent(ctx, notification.Event{
		Type:        notification.EventRejection,
		TenantID:    tenantID,
		RecipientID: recipient,
		ActorID:     actor.UserID,
		ActorName:   actorName,
		JobID:       jobID,
		ProjectID:   pID,
		ProjectName: pName,
		FileName:    fName,
		TargetLang:  tLang,
		StepName:    stepName,
		Reason:      reason,
	})
}
