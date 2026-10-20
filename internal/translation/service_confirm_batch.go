package translation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/hold"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/ptr"
	"openloc/internal/qa"
	"openloc/internal/segmentbulk"
	"openloc/internal/strdist"
	"openloc/internal/taskedit"
)

// checkSelectedRows runs the confirm-time QA gate on the rows' current text
// and stores their findings, in one batch. It returns the rows a QA error
// blocks. A checker failure is an error, never a pass.
func (s *Service) checkSelectedRows(ctx context.Context, checker *qa.JobChecker, rows []segmentbulk.Row) (map[int64]bool, error) {
	blocked := map[int64]bool{}
	if checker == nil || len(rows) == 0 {
		return blocked, nil
	}
	in := make([]qa.CheckRow, len(rows))
	for i, r := range rows {
		in[i] = qa.CheckRow{ID: r.ID, Source: r.SourceText, SourceRich: ptr.Deref(r.SourceRichText),
			Target: *r.ActiveText, TargetRich: ptr.Deref(r.ActiveRichText)}
	}
	results, err := checker.CheckAndPersist(ctx, in)
	if err != nil {
		return nil, err
	}
	for id, res := range results {
		if res.HasErrors() {
			blocked[id] = true
		}
	}
	return blocked, nil
}

// reapproveRows puts machine-approved rows of the job back to auto_approved in
// one statement, under the job lock. A row changed since it was read is a
// conflict.
func (s *Service) reapproveRows(ctx context.Context, job db.Job, tenantID int64, rows []segmentbulk.Row, reasons map[int64]segmentbulk.SkipReason) ([]int64, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(rows))
	expected := make([]pgtype.Timestamptz, len(rows))
	for i, r := range rows {
		ids[i], expected[i] = r.ID, r.UpdatedAt
	}
	var written []int64
	if err := database.InJobsTx(ctx, s.pool, tenantID, []int64{job.ID}, func(_ pgx.Tx, q db.Querier) error {
		var err error
		written, err = q.ReapproveMachineTranslations(ctx, db.ReapproveMachineTranslationsParams{Ids: ids, ExpectedUpdatedAt: expected})
		return err
	}); err != nil {
		return nil, fmt.Errorf("reapprove translations: %w", err)
	}
	done := idSet(written)
	for _, id := range ids {
		if !done[id] {
			reasons[id] = segmentbulk.Conflict
		}
	}
	return written, nil
}

// confirmRowsBatch is commitConfirm for many rows of one job with the text
// they hold, in a few statements: one read of the edit-distance baselines, one
// update and one history insert in a transaction under the job lock, then one
// TM write. A row changed since it was read is a conflict, a row another user
// holds is held.
func (s *Service) confirmRowsBatch(ctx context.Context, job db.Job, tenantID, userID int64, rows []segmentbulk.Row, task taskedit.Result, reasons map[int64]segmentbulk.SkipReason) ([]int64, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	latest, err := s.queries.ListLatestHistoryTexts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list history baselines: %w", err)
	}
	baselines := make(map[int64]string, len(latest))
	for _, l := range latest {
		baselines[l.TranslationID] = l.Text
	}

	up := db.ConfirmTranslationsHumanParams{EditedBy: userID}
	hist := db.InsertHumanHistoryBatchParams{CreatedBy: userID, JobStepAssignmentID: activeStep(task)}
	for _, r := range rows {
		text := *r.ActiveText
		// The baseline is the newest history text, else the text the row holds.
		baseline, ok := baselines[r.ID]
		if !ok {
			baseline = text
		}
		var editDist int32
		if baseline != "" {
			editDist = int32(strdist.Levenshtein(baseline, text))
		}
		rich, hasRich := ptr.Deref(r.ActiveRichText), r.ActiveRichText != nil
		up.Ids = append(up.Ids, r.ID)
		up.Texts = append(up.Texts, text)
		up.RichTexts = append(up.RichTexts, rich)
		up.HasRich = append(up.HasRich, hasRich)
		up.EditDistances = append(up.EditDistances, editDist)
		up.ExpectedUpdatedAt = append(up.ExpectedUpdatedAt, r.UpdatedAt)

		kind := string(db.TranslationHistoryKindHuman)
		if text == baseline {
			kind = string(db.TranslationHistoryKindApproved)
		}
		hist.Ids = append(hist.Ids, r.ID)
		hist.Kinds = append(hist.Kinds, kind)
		hist.Texts = append(hist.Texts, text)
		hist.RichTexts = append(hist.RichTexts, rich)
		hist.HasRich = append(hist.HasRich, hasRich)
		hist.EditDistances = append(hist.EditDistances, editDist)
	}

	var written []int64
	if err := database.InJobsTx(ctx, s.pool, tenantID, []int64{job.ID}, func(_ pgx.Tx, q db.Querier) error {
		var err error
		if written, err = q.ConfirmTranslationsHuman(ctx, up); err != nil {
			return fmt.Errorf("confirm translations: %w", err)
		}
		keep := onlyWritten(hist, idSet(written))
		if len(keep.Ids) == 0 {
			return nil
		}
		if err := q.InsertHumanHistoryBatch(ctx, keep); err != nil {
			return fmt.Errorf("record confirm history: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	done := idSet(written)
	for _, id := range ids {
		if done[id] {
			continue
		}
		err := ExplainNoRows(ctx, s.queries, tenantID, id, userID, true)
		var held *hold.HeldError
		switch {
		case errors.As(err, &held):
			reasons[id] = segmentbulk.Held
		case errors.Is(err, ErrConflict):
			reasons[id] = segmentbulk.Conflict
		case err != nil:
			// The other rows have committed; this one is reported as changed.
			logging.Error(ctx, err, "bulk confirm could not tell why a row was not written", "bulk-confirm-explain-failed",
				logging.TranslationID(id))
			reasons[id] = segmentbulk.Conflict
		default:
			reasons[id] = segmentbulk.Conflict
		}
	}

	// TM write-back is best-effort, as for a single confirm: a TM failure must
	// not fail confirms that have already committed.
	if len(written) > 0 {
		if s.tmWriter == nil {
			logging.Warn(ctx, nil, "no TM writer wired; confirmed translations not saved to TM", "tm-write-no-writer")
		} else if _, err := s.tmWriter.SaveTranslationsToTM(ctx, tenantID, written, pipeline.TMReasonCATConfirm); err != nil {
			logging.Error(ctx, err, "auto-save of confirmed translations to TM failed", "tm-write-auto-save-failed")
		}
	}
	return written, nil
}

// onlyWritten keeps the history rows of the ids the update wrote.
func onlyWritten(h db.InsertHumanHistoryBatchParams, written map[int64]bool) db.InsertHumanHistoryBatchParams {
	out := db.InsertHumanHistoryBatchParams{CreatedBy: h.CreatedBy, JobStepAssignmentID: h.JobStepAssignmentID}
	for i, id := range h.Ids {
		if !written[id] {
			continue
		}
		out.Ids = append(out.Ids, id)
		out.Kinds = append(out.Kinds, h.Kinds[i])
		out.Texts = append(out.Texts, h.Texts[i])
		out.RichTexts = append(out.RichTexts, h.RichTexts[i])
		out.HasRich = append(out.HasRich, h.HasRich[i])
		out.EditDistances = append(out.EditDistances, h.EditDistances[i])
	}
	return out
}

func idSet(ids []int64) map[int64]bool {
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}
