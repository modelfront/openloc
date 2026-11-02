package segmentedit

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parser"
	"openloc/internal/ptr"
	"openloc/internal/taskedit"
	"openloc/internal/wordcount"
)

// Split cuts a row of the job in two at the offset. The row's translation
// moves to the first piece with its target, comments and history; the second
// piece gets a new, empty translation. Both need review. Only this job
// changes: a shared segment stays as it is for the file's other languages.
func (s *Service) Split(ctx context.Context, job db.Job, tenantID, userID int64, req SplitRequest) (Result, error) {
	decision, err := s.checkJob(ctx, job, tenantID)
	if err != nil {
		return Result{}, err
	}
	rows, err := s.q.ListJobEditRowsByTranslations(ctx, db.ListJobEditRowsByTranslationsParams{
		FileID: job.FileID, TargetLanguage: job.TargetLanguage, TenantID: tenantID,
		TranslationIds: []int64{req.TranslationID},
	})
	if err != nil {
		return Result{}, err
	}
	if len(rows) != 1 {
		return Result{}, pgx.ErrNoRows
	}
	r := rows[0]
	if err := s.checkRow(ctx, tenantID, userID, r); err != nil {
		return Result{}, err
	}
	key := exportKey(r)
	plural, err := s.isPluralForm(ctx, job, tenantID, r)
	if err != nil {
		return Result{}, err
	}
	if key == "" || plural {
		return Result{}, ErrSplitUnsupported
	}
	off, ok := utf16Offset(r.SourceText, req.Offset)
	if !ok {
		return Result{}, ErrSplitPosition
	}
	pieces, err := parser.SplitSourceAt(r.SourceText, ptr.Deref(r.SourceRichText), off)
	switch {
	case errors.Is(err, parser.ErrSplitTags):
		return Result{}, ErrSplitTags
	case errors.Is(err, parser.ErrSplitPosition):
		return Result{}, ErrSplitPosition
	}
	if err != nil {
		return Result{}, err
	}
	// Chip bases and the first gap are relative to the original key.
	pieces[0].ChipBase += int(r.ChipBase)
	pieces[1].ChipBase += int(r.ChipBase)
	pieces[0].Gap = ptr.Deref(r.GapBefore)

	label := Number(r.Position, r.SubPosition)
	var second db.Translation
	err = database.InJobsTx(ctx, s.pool, tenantID, []int64{job.ID}, func(tx pgx.Tx, q db.Querier) error {
		task, err := s.guard.ApplyInTx(ctx, tx, tenantID, decision)
		if err != nil {
			return err
		}
		var segs [2]db.Segment
		for i, p := range pieces {
			segs[i], err = q.CreateLanguageSegment(ctx, languageSegment(job, r, key, p))
			if err != nil {
				return err
			}
		}
		first, err := s.moveTranslation(ctx, q, tenantID, userID, r, segs[0].ID, versionOr(req.ExpectedUpdatedAt, r), r.ActiveText, r.ActiveRichText)
		if err != nil {
			return err
		}
		second, err = q.CreateTranslation(ctx, db.CreateTranslationParams{
			SegmentID: segs[1].ID, TargetLanguage: job.TargetLanguage, Status: db.TranslationStatusNeedsReview,
		})
		if err != nil {
			return err
		}
		if r.SegmentLanguage != nil {
			if err := q.DeleteLanguageSegments(ctx, []int64{r.SegmentID}); err != nil {
				return err
			}
		}
		if err := q.RenumberLanguageRows(ctx, db.RenumberLanguageRowsParams{
			FileID: job.FileID, TargetLanguage: job.TargetLanguage, Position: r.Position,
		}); err != nil {
			return err
		}
		if err := s.afterSourceChange(ctx, q, first.ID); err != nil {
			return err
		}
		details, err := historyDetails(map[string]any{"from_segment": label})
		if err != nil {
			return err
		}
		for _, t := range []db.Translation{first, second} {
			if _, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
				TranslationID: t.ID, Kind: db.TranslationHistoryKindSplit,
				Text: t.ActiveText, RichText: t.ActiveRichText,
				CreatedBy: &userID, JobStepAssignmentID: activeStep(task), Details: details,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{TranslationIDs: []int64{r.TranslationID, second.ID}}, nil
}

// isPluralForm reports whether r is one plural form of a PO entry ("#n" on
// its key), which is translated as a set and never split.
func (s *Service) isPluralForm(ctx context.Context, job db.Job, tenantID int64, r row) (bool, error) {
	if !strings.Contains(exportKey(r), "#") {
		return false, nil
	}
	f, err := s.q.GetFileTenant(ctx, db.GetFileTenantParams{ID: job.FileID, TenantID: tenantID})
	if err != nil {
		return false, err
	}
	return f.Format == db.FileFormatPo, nil
}

// languageSegment is the language row for piece p of r, at r's place.
func languageSegment(job db.Job, r row, key string, p parser.Piece) db.CreateLanguageSegmentParams {
	return db.CreateLanguageSegmentParams{
		FileID: job.FileID, TargetLanguage: job.TargetLanguage,
		SourceText: p.Text, SourceRichText: ptr.NilIfZero(p.Rich),
		ExportKey: key, Position: r.Position, SubPosition: r.SubPosition,
		Context: r.Context, WordCount: int32(wordcount.CountWords(p.Text)),
		GapBefore: ptr.NilIfZero(p.Gap), ChipBase: int32(p.ChipBase),
	}
}

// afterSourceChange drops what was worked out against the old source: the
// QA findings, and the text a step round ended with, so a rollback never
// restores a status the old source earned.
func (s *Service) afterSourceChange(ctx context.Context, q db.Querier, translationID int64) error {
	if err := q.PruneQAResultsForTranslation(ctx, db.PruneQAResultsForTranslationParams{
		TranslationID: translationID, KeepCheckTypes: []string{},
	}); err != nil {
		return err
	}
	return q.ForgetStepRoundTexts(ctx, []int64{translationID})
}

// activeStep is the job's in-progress task, which history rows name.
func activeStep(task taskedit.Result) *int64 {
	if task.ActiveID == 0 {
		return nil
	}
	return &task.ActiveID
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
