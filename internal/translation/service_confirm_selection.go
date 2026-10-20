package translation

import (
	"context"
	"fmt"
	"slices"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/qa"
	"openloc/internal/segmentbulk"
	"openloc/internal/taskedit"
)

// ConfirmSelection confirms the given rows of the job with the text they hold
// now — the editor's bulk Confirm. The written rows get what a single confirm
// writes (history, TM write-back as the user), in set-based statements.
//
// Rows left alone: locked, held by another user (checked before QA runs on
// them), empty, already approved, auto-approved (approved by quality
// prediction: a person confirms those one by one, so a bulk confirm never
// sends machine-approved text to TM), with a QA error, or changed since they
// were read. A machine-approved row an earlier step auto-approved goes back to
// auto-approved for the same reason: no TM write, no history. The task guard is checked once for the job, and a
// completed task is reopened only when at least one row will be written.
//
// QA findings are stored for every row checked, so a row skipped for a QA
// error shows its findings in the editor.
func (s *Service) ConfirmSelection(ctx context.Context, job db.Job, tenantID, userID int64, ids []int64) (segmentbulk.Result, error) {
	ids, err := segmentbulk.NormalizeIDs(ids)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	sc, err := s.newConfirmScope(ctx, job, tenantID)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	return s.confirmRows(ctx, job, tenantID, userID, ids, sc)
}

// ConfirmJob confirms every translated, unapproved segment of the job — the
// Complete step dialog's "Approve all". Same per-row rules as
// ConfirmSelection; the rows go in batches of the selection cap, so a large
// job is approved in full. The caller decides what a skipped row (a QA error
// above all) means for completing the step.
func (s *Service) ConfirmJob(ctx context.Context, job db.Job, tenantID, userID int64) (segmentbulk.Result, error) {
	ids, err := s.queries.ListJobUnconfirmedTranslationIDs(ctx, db.ListJobUnconfirmedTranslationIDsParams{
		FileID: job.FileID, TargetLanguage: job.TargetLanguage,
	})
	if err != nil {
		return segmentbulk.Result{}, fmt.Errorf("list unconfirmed translations: %w", err)
	}
	sc, err := s.newConfirmScope(ctx, job, tenantID)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	res := segmentbulk.NewResult()
	for batch := range slices.Chunk(ids, segmentbulk.MaxIDs) {
		part, err := s.confirmRows(ctx, job, tenantID, userID, batch, sc)
		if err != nil {
			return segmentbulk.Result{}, err
		}
		res.Applied = append(res.Applied, part.Applied...)
		res.Skipped = append(res.Skipped, part.Skipped...)
		res.TaskReopened = res.TaskReopened || part.TaskReopened
	}
	return res, nil
}

// confirmScope is what every batch of one bulk confirm shares: the job's file
// and its QA checker, which holds the project's rules and glossary.
type confirmScope struct {
	file    db.File
	checker *qa.JobChecker
}

func (s *Service) newConfirmScope(ctx context.Context, job db.Job, tenantID int64) (confirmScope, error) {
	// Unscoped by signature; the job was loaded tenant-scoped.
	file, err := s.queries.GetFile(ctx, job.FileID)
	if err != nil {
		return confirmScope{}, fmt.Errorf("get file: %w", err)
	}
	sc := confirmScope{file: file}
	if s.qaChecker != nil {
		if sc.checker, err = s.qaChecker.JobChecker(ctx, job, tenantID); err != nil {
			return confirmScope{}, err
		}
	}
	return sc, nil
}

// confirmRows is the shared body of ConfirmSelection and ConfirmJob for ids
// already normalized and within the selection cap.
func (s *Service) confirmRows(ctx context.Context, job db.Job, tenantID, userID int64, ids []int64, sc confirmScope) (segmentbulk.Result, error) {
	rows, err := segmentbulk.JobRows(ctx, s.queries, job, userID, ids)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	// Per batch: a reopen by an earlier batch changes what the next one sees.
	edit, err := s.guard.Check(ctx, tenantID, taskedit.Ref{JobID: job.ID}, taskedit.Opts{})
	if err != nil {
		return segmentbulk.Result{}, err
	}

	reasons := make(map[int64]segmentbulk.SkipReason, len(rows))
	var candidates []segmentbulk.Row
	for _, id := range ids {
		r, ok := rows[id]
		if !ok {
			continue
		}
		switch {
		case r.Locked:
			reasons[id] = segmentbulk.Locked
		case r.HeldByOther:
			reasons[id] = segmentbulk.Held
		case segmentbulk.IsEmpty(r):
			reasons[id] = segmentbulk.Empty
		case r.Status == db.TranslationStatusApproved:
			reasons[id] = segmentbulk.Unchanged
		case r.Status == db.TranslationStatusAutoApproved:
			reasons[id] = segmentbulk.AutoApproved
		case len(foreignTagIssues(sc.file, r.SourceText, r.SourceRichText, *r.ActiveText, r.ActiveRichText)) > 0:
			reasons[id] = segmentbulk.TagNotInSource
		default:
			candidates = append(candidates, r)
		}
	}
	blocked, err := s.checkSelectedRows(ctx, sc.checker, candidates)
	if err != nil {
		return segmentbulk.Result{}, err
	}
	var machine, human []segmentbulk.Row
	for _, r := range candidates {
		switch {
		case blocked[r.ID]:
			reasons[r.ID] = segmentbulk.QAError
		case r.MachineApproved:
			machine = append(machine, r)
		default:
			human = append(human, r)
		}
	}

	var written []int64
	var task taskedit.Result
	if len(machine)+len(human) > 0 {
		if task, err = s.guard.Apply(ctx, tenantID, edit); err != nil {
			return segmentbulk.Result{}, err
		}
		reapproved, err := s.reapproveRows(ctx, job, tenantID, machine, reasons)
		if err != nil {
			return segmentbulk.Result{}, err
		}
		confirmed, err := s.confirmRowsBatch(ctx, job, tenantID, userID, human, task, reasons)
		if err != nil {
			return segmentbulk.Result{}, err
		}
		written = append(reapproved, confirmed...)
	}

	res := segmentbulk.Split(ids, written, rows, func(r segmentbulk.Row) segmentbulk.SkipReason { return reasons[r.ID] })
	res.TaskReopened = task.Reopened
	return res, nil
}
