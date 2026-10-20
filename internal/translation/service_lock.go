package translation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/segmentbulk"
)

// ErrLockEmpty refuses a lock on a segment with no translation: a locked
// segment is always Approved or Auto-approved, and empty text cannot be
// approved.
var ErrLockEmpty = errors.New("translate the segment before locking it")

// A lock is a manager's decision that the text is final. Locking approves the
// segment like a confirm — history entry under the locker, TM write-back —
// unless QE+APE already auto-approved it, which stays as it is. The QA gate
// does not apply: the lock overrides it. Locked segments are read-only to
// every write until someone unlocks them; unlocking keeps the status.
// The routes and the MCP tool allow locking to managers only. A lock by
// anyone but an owner or admin leaves alone a segment another user holds.

// LockOne locks and approves one segment of the file. Locking a locked
// segment changes nothing. via is ConfirmedViaUser, or ConfirmedViaAssistant
// for the assistant acting on a user's request (kept out of TM). A segment
// another user holds is refused with a *hold.HeldError.
func (s *Service) LockOne(ctx context.Context, tenantID, userID int64, t db.Translation, fileID int64, via string) error {
	if t.Locked {
		return nil
	}
	if t.ActiveText == nil || *t.ActiveText == "" {
		return ErrLockEmpty
	}
	skipHeld := lockSkipsHeld(ctx)
	locked, err := s.lock(ctx, tenantID, userID, fileID, t.TargetLanguage, []int64{t.ID}, via, skipHeld)
	if err != nil {
		return err
	}
	if len(locked) == 0 {
		if skipHeld {
			if err := CheckHeld(ctx, s.queries, tenantID, t.ID, userID); err != nil {
				return err
			}
		}
		return ErrConflict
	}
	return nil
}

// LockSelection locks and approves the given rows of the job. Already locked
// rows come back unchanged, empty rows as empty, rows another user holds as
// held.
func (s *Service) LockSelection(ctx context.Context, job db.Job, tenantID, userID int64, ids []int64) (segmentbulk.Result, error) {
	ids, err := segmentbulk.NormalizeIDs(ids)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	rows, err := segmentbulk.JobRows(ctx, s.queries, job, userID, ids)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	skipHeld := lockSkipsHeld(ctx)
	written, err := s.lock(ctx, tenantID, userID, job.FileID, job.TargetLanguage, ids, ConfirmedViaUser, skipHeld)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	return segmentbulk.Split(ids, written, rows, func(r segmentbulk.Row) segmentbulk.SkipReason {
		switch {
		case r.Locked:
			return segmentbulk.Unchanged
		case segmentbulk.IsEmpty(r):
			return segmentbulk.Empty
		case skipHeld && r.HeldByOther:
			return segmentbulk.Held
		default:
			return segmentbulk.Conflict
		}
	}), nil
}

// UnlockSelection unlocks the given rows of the job; their status stays.
func (s *Service) UnlockSelection(ctx context.Context, job db.Job, tenantID, userID int64, ids []int64) (segmentbulk.Result, error) {
	ids, err := segmentbulk.NormalizeIDs(ids)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	rows, err := segmentbulk.JobRows(ctx, s.queries, job, userID, ids)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	var written []int64
	err = database.InJobsTx(ctx, s.pool, tenantID, []int64{job.ID}, func(_ pgx.Tx, q db.Querier) error {
		var err error
		written, err = q.UnlockJobTranslations(ctx, db.UnlockJobTranslationsParams{
			FileID: job.FileID, TargetLanguage: job.TargetLanguage, Ids: ids,
		})
		return err
	})
	if err != nil {
		return segmentbulk.Result{}, fmt.Errorf("unlock translations: %w", err)
	}
	return segmentbulk.Split(ids, written, rows, func(segmentbulk.Row) segmentbulk.SkipReason {
		return segmentbulk.Unchanged
	}), nil
}

// lock runs the lock-and-approve write, then records each approval it made:
// an `approved` history entry in the job's current step, and the TM
// write-back for a person's approval. Both are best-effort, as on a confirm.
// With skipHeld, rows a user other than userID holds are left alone.
func (s *Service) lock(ctx context.Context, tenantID, userID, fileID int64, lang string, ids []int64, via string, skipHeld bool) ([]int64, error) {
	if via == "" {
		via = ConfirmedViaUser
	}
	jobIDs, err := database.FileJobIDs(ctx, s.queries, fileID, lang)
	if err != nil {
		return nil, err
	}
	var rows []db.LockApproveTranslationsRow
	var stepID *int64
	if err := database.InJobsTx(ctx, s.pool, tenantID, jobIDs, func(_ pgx.Tx, q db.Querier) error {
		var err error
		if rows, err = q.LockApproveTranslations(ctx, db.LockApproveTranslationsParams{
			ConfirmedVia: via, FileID: fileID, TargetLanguage: lang, Ids: ids,
			SkipHeld: skipHeld, ActorID: &userID,
		}); err != nil {
			return err
		}
		// Read under the job lock: the step the approvals belong to.
		job, err := q.GetJobByFileAndLang(ctx, db.GetJobByFileAndLangParams{FileID: fileID, TargetLanguage: lang})
		switch {
		case err == nil:
			stepID = job.CurrentStepID
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("get job step: %w", err)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("lock translations: %w", err)
	}
	locked := make([]int64, 0, len(rows))
	for _, r := range rows {
		locked = append(locked, r.ID)
		if !approvedByLock(r) {
			continue
		}
		details := map[string]any{"source": "lock"}
		if r.ConfirmedVia == ConfirmedViaAssistant {
			details["confirmed_via"] = ConfirmedViaAssistant
		}
		pipeline.EmitHumanHistory(ctx, s.queries, pipeline.HumanEmitOpts{
			TranslationID:       r.ID,
			Kind:                db.TranslationHistoryKindApproved,
			CreatedBy:           &userID,
			JobStepAssignmentID: stepID,
			Details:             details,
		})
		if s.tmWriter != nil && r.ConfirmedVia == ConfirmedViaUser {
			if _, err := s.tmWriter.SaveToTM(ctx, tenantID, r.ID); err != nil {
				logging.Warn(ctx, err, "TM write-back after a lock failed", "lock-tm-writeback-failed", logging.TranslationID(r.ID))
			}
		}
	}
	return locked, nil
}

// lockSkipsHeld reports whether a lock by the caller leaves alone the segments
// another user holds: everyone but owner and admin.
func lockSkipsHeld(ctx context.Context) bool {
	return !auth.IsAdminRole(auth.RoleFromContext(ctx))
}

// approvedByLock reports whether the lock made this approval: the row is
// approved now and was not already approved by the same kind of decision.
func approvedByLock(r db.LockApproveTranslationsRow) bool {
	if r.Status != db.TranslationStatusApproved {
		return false
	}
	return r.PriorStatus != db.TranslationStatusApproved || r.PriorConfirmedVia != r.ConfirmedVia
}
