// Package stepround records how each workflow step round leaves every segment
// and sets the live segment statuses a new round starts with.
package stepround

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
)

// Each start or reopen of a step assignment is a round. A round records, per
// segment, the status it starts and ends with in translation_step_statuses, so
// later steps can see how every earlier step left a segment.

// Mode says what a newly opened round does to the live segment statuses.
type Mode int

const (
	// Keep leaves the statuses as they are (the first step of a job).
	Keep Mode = iota
	// Reset takes away the previous step's approvals, auto-approvals and
	// drafts: a new step confirms its own work. Locked segments keep their
	// status.
	Reset
	// Restore makes a reopened step look as its last round left it:
	// statuses, drafts and the round's AI verdicts. Text changed since comes
	// back as a draft without a verdict, and a verdict a later step's AI gave
	// is dropped.
	Restore
	// Resume starts a step the job moves forward into: as Reset, then a step
	// that ran before (the job was sent back past it) gets its last round's
	// result back on every segment whose text has not changed since.
	Resume
	// Review reopens a step whose AI runs again: every translated, unlocked
	// segment starts unconfirmed and without a verdict, and the round's AI
	// run reviews them all on their current text, people's text included.
	Review
)

// End reasons stored in translation_step_statuses.end_reason.
const (
	Completed = "completed"
	Rejected  = "rejected"
)

// Open starts the next round of a step assignment of jobID on q, which
// should be the transaction that also moves the job onto the step. The caller
// has tenant-verified the job.
func Open(ctx context.Context, q db.Querier, jobID, assignmentID int64, mode Mode) error {
	// Verdicts are settled before the round opens, so the round records the
	// ones it really starts with.
	switch mode {
	case Restore:
		if err := q.ClearUndoneVerdicts(ctx, assignmentID); err != nil {
			return fmt.Errorf("clear undone verdicts: %w", err)
		}
	case Review:
		if err := q.ResetForAIReview(ctx, jobID); err != nil {
			return fmt.Errorf("reset for AI review: %w", err)
		}
	}
	if _, err := q.BumpStepRound(ctx, db.BumpStepRoundParams{ID: assignmentID, AiReviewAll: mode == Review}); err != nil {
		return fmt.Errorf("bump step round: %w", err)
	}
	if err := q.OpenStepRound(ctx, assignmentID); err != nil {
		return fmt.Errorf("open step round: %w", err)
	}
	switch mode {
	case Reset, Resume:
		if err := q.ResetStepStatuses(ctx, jobID); err != nil {
			return fmt.Errorf("reset step statuses: %w", err)
		}
		if mode == Resume {
			if err := q.RestoreStepStatuses(ctx, db.RestoreStepStatusesParams{AssignmentID: assignmentID, OnlyUnchanged: true}); err != nil {
				return fmt.Errorf("restore unchanged step statuses: %w", err)
			}
		}
	case Restore:
		// Clear first: segments with no recorded round start without edits,
		// the rest take theirs back from the round.
		if err := q.ClearStepEdited(ctx, jobID); err != nil {
			return fmt.Errorf("clear step edits: %w", err)
		}
		if err := q.RestoreStepStatuses(ctx, db.RestoreStepStatusesParams{AssignmentID: assignmentID}); err != nil {
			return fmt.Errorf("restore step statuses: %w", err)
		}
		if err := q.CarryStepPredictions(ctx, assignmentID); err != nil {
			return fmt.Errorf("carry step predictions: %w", err)
		}
	case Keep, Review:
		if err := q.ClearStepEdited(ctx, jobID); err != nil {
			return fmt.Errorf("clear step edits: %w", err)
		}
	}
	return nil
}

// Close records how the assignment's current round ends for every
// segment of its job. reason is Completed or Rejected.
func Close(ctx context.Context, q db.Querier, assignmentID int64, reason string) error {
	if err := q.CloseStepRound(ctx, db.CloseStepRoundParams{AssignmentID: assignmentID, EndReason: reason}); err != nil {
		return fmt.Errorf("close step round: %w", err)
	}
	return nil
}

// Entry is how one step round left one segment.
type Entry struct {
	StepName  string `json:"step_name"`
	StepOrder int32  `json:"step_order"`
	StepType  string `json:"step_type"`
	Round     int32  `json:"round"`
	// EndReason is Completed or Rejected (the step was sent back).
	EndReason string `json:"end_reason"`
	Status    string `json:"status"`
	// AIOutcome is the QE/APE verdict produced in this round, if any.
	AIOutcome *string `json:"ai_outcome"`
	// Edited is true when a person changed the text during the round.
	Edited  bool   `json:"edited"`
	EndedAt string `json:"ended_at"`
}

// NewEntry builds an Entry from the columns every step-status query returns.
func NewEntry(stepName string, stepOrder int32, stepType string, round int32, endedAt pgtype.Timestamptz,
	endReason *string, status db.NullTranslationStatus, ai db.NullPredictOutcome, edited *bool) Entry {
	e := Entry{
		StepName: stepName, StepOrder: stepOrder, StepType: stepType, Round: round,
		Status:  string(status.TranslationStatus),
		Edited:  edited != nil && *edited,
		EndedAt: endedAt.Time.UTC().Format(time.RFC3339),
	}
	if endReason != nil {
		e.EndReason = *endReason
	}
	if ai.Valid {
		o := string(ai.PredictOutcome)
		e.AIOutcome = &o
	}
	return e
}

// PreviousByTranslation returns, per translation id, the most recently closed
// step round. Translations no step has closed on yet are absent.
func PreviousByTranslation(ctx context.Context, q db.Querier, tenantID int64, ids []int64) (map[int64]Entry, error) {
	out := make(map[int64]Entry, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.ListPreviousStepStatusesByTranslations(ctx, db.ListPreviousStepStatusesByTranslationsParams{
		TranslationIds: ids, TenantID: tenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("list previous step statuses: %w", err)
	}
	for _, r := range rows {
		out[r.TranslationID] = NewEntry(r.StepName, r.StepOrder, r.StepType, r.Round, r.EndedAt,
			r.EndReason, r.Status, r.PredictOutcome, r.HumanEdited)
	}
	return out, nil
}
