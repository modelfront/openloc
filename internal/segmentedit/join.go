package segmentedit

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/parser"
	"openloc/internal/ptr"
	"openloc/internal/taskedit"
)

// maxJoinRows caps one join: a split makes two rows, so a real join is a
// handful.
const maxJoinRows = 100

// Join puts rows that follow each other in the job into the first one: the
// pieces of one split segment, or sentences of one block (a paragraph, an
// element, a trans-unit's <mrk> sentences), whole. The first row's
// translation stays, its target the rows' targets in order; the other rows'
// comments and history move to it, labelled with the number of the row they
// belonged to, and their translations are deleted. The row needs review. A
// join of every piece of a split gives back the imported segment.
func (s *Service) Join(ctx context.Context, job db.Job, tenantID, userID int64, req JoinRequest) (Result, error) {
	if len(req.Rows) < 2 {
		return Result{}, ErrJoinCount
	}
	if len(req.Rows) > maxJoinRows {
		return Result{}, httputil.NewClientError("too many rows to join at once")
	}
	expected := make(map[int64]time.Time, len(req.Rows))
	ids := make([]int64, 0, len(req.Rows))
	for _, r := range req.Rows {
		if _, dup := expected[r.TranslationID]; dup {
			return Result{}, httputil.NewClientError("a row is listed twice")
		}
		expected[r.TranslationID] = r.ExpectedUpdatedAt
		ids = append(ids, r.TranslationID)
	}
	decision, err := s.checkJob(ctx, job, tenantID)
	if err != nil {
		return Result{}, err
	}
	rows, err := s.q.ListJobEditRowsByTranslations(ctx, db.ListJobEditRowsByTranslationsParams{
		FileID: job.FileID, TargetLanguage: job.TargetLanguage, TenantID: tenantID, TranslationIds: ids,
	})
	if err != nil {
		return Result{}, err
	}
	if len(rows) != len(ids) {
		return Result{}, pgx.ErrNoRows
	}
	between, err := s.rowsAround(ctx, job, tenantID, rows)
	if err != nil {
		return Result{}, err
	}
	if !followEachOther(rows, between) {
		return Result{}, ErrNotAdjacent
	}
	for _, r := range rows {
		if err := s.checkRow(ctx, tenantID, userID, r); err != nil {
			return Result{}, err
		}
	}
	var plan joinPlan
	if onePieceGroup(rows) {
		plan, err = s.planPieceJoin(ctx, job, rows, between)
	} else {
		plan, err = s.planSentenceJoin(ctx, job, tenantID, rows, between)
	}
	if err != nil {
		return Result{}, err
	}
	if err := s.commitJoin(ctx, job, tenantID, userID, decision, rows, expected, plan); err != nil {
		return Result{}, err
	}
	return Result{TranslationIDs: []int64{rows[0].TranslationID}}, nil
}

// joinPlan is what a join writes: the segment the kept translation moves to
// (the imported segment, or a new language row), and its target.
type joinPlan struct {
	sharedID   int64
	segment    db.CreateLanguageSegmentParams
	targetText *string
	targetRich *string
}

// rowsAround lists the job's rows from the first position of rows to the
// last, in document order, without reading the rows in between: rows that
// follow each other leave no other row strictly inside their span, so a count
// capped at one more than the named rows there refuses a gap, and the named
// rows stand for the inside.
func (s *Service) rowsAround(ctx context.Context, job db.Job, tenantID int64, rows []row) ([]row, error) {
	first, last := rows[0].Position, rows[len(rows)-1].Position
	var inside []row
	for _, r := range rows {
		if r.Position > first && r.Position < last {
			inside = append(inside, r)
		}
	}
	if last > first+1 {
		n, err := s.q.CountJobRowsInside(ctx, db.CountJobRowsInsideParams{
			FileID: job.FileID, TargetLanguage: job.TargetLanguage, TenantID: tenantID,
			FromPosition: first, ToPosition: last, RowCap: int32(len(inside) + 1),
		})
		if err != nil {
			return nil, err
		}
		if int(n) != len(inside) {
			return nil, ErrNotAdjacent
		}
	}
	out, err := s.rowsAt(ctx, job, tenantID, first)
	if err != nil {
		return nil, err
	}
	out = append(out, inside...)
	if last != first {
		tail, err := s.rowsAt(ctx, job, tenantID, last)
		if err != nil {
			return nil, err
		}
		out = append(out, tail...)
	}
	return out, nil
}

// rowsAt lists the job's rows at one position: a row and its pieces.
func (s *Service) rowsAt(ctx context.Context, job db.Job, tenantID int64, position int32) ([]row, error) {
	at, err := s.q.ListJobEditRowsAt(ctx, db.ListJobEditRowsAtParams{
		FileID: job.FileID, TargetLanguage: job.TargetLanguage, TenantID: tenantID, Position: position,
	})
	if err != nil {
		return nil, err
	}
	out := make([]row, len(at))
	for i, a := range at {
		out[i] = row(a)
	}
	return out, nil
}

// followEachOther reports whether rows are a run of between, in order.
func followEachOther(rows []row, between []row) bool {
	start := slices.IndexFunc(between, func(b row) bool { return b.TranslationID == rows[0].TranslationID })
	if start < 0 || start+len(rows) > len(between) {
		return false
	}
	for i, r := range rows {
		if between[start+i].TranslationID != r.TranslationID {
			return false
		}
	}
	return true
}

// onePieceGroup reports whether the rows are pieces of one split segment:
// language rows rebuilding the same original key.
func onePieceGroup(rows []row) bool {
	key := exportKey(rows[0])
	for _, r := range rows {
		if r.SegmentLanguage == nil || r.Position != rows[0].Position || exportKey(r) != key {
			return false
		}
	}
	return true
}

// planPieceJoin joins pieces of one split segment: their sources with the
// gaps between them, in the segment's chip numbering. Every piece gives back
// the imported segment.
func (s *Service) planPieceJoin(ctx context.Context, job db.Job, rows []row, between []row) (joinPlan, error) {
	first := rows[0]
	key := exportKey(first)
	source := make([]parser.Piece, len(rows))
	for i, r := range rows {
		source[i] = parser.Piece{Text: r.SourceText, Rich: ptr.Deref(r.SourceRichText), ChipBase: int(r.ChipBase), Gap: ptr.Deref(r.GapBefore)}
	}
	text, rich := parser.JoinPieces(source)
	base := 0
	if rich != "" {
		rich, base = parser.RenumberChips(rich)
	}
	plan := joinPlan{segment: languageSegment(job, first, key, parser.Piece{
		Text: text, Rich: rich, ChipBase: base, Gap: ptr.Deref(first.GapBefore),
	})}
	plan.targetText, plan.targetRich = joinTargets(rows, func(r row) int { return int(r.ChipBase) - base })
	if len(between) == len(rows) {
		shared, err := s.q.GetSharedSegmentByKey(ctx, db.GetSharedSegmentByKeyParams{
			FileID: job.FileID, SegmentKey: &key, Position: first.Position,
		})
		switch {
		case err == nil && shared.SourceText == text:
			plan.sharedID = shared.ID
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return joinPlan{}, err
		}
	}
	return plan, nil
}

// commitJoin writes a planned join: the kept translation moves onto the
// joined segment, the others fold into it, and the language rows it replaced
// go.
func (s *Service) commitJoin(ctx context.Context, job db.Job, tenantID, userID int64, decision taskedit.Decision, rows []row, expected map[int64]time.Time, plan joinPlan) error {
	first := rows[0]
	labels := make([]string, len(rows))
	var positions []int32
	for i, r := range rows {
		labels[i] = Number(r.Position, r.SubPosition)
		if !slices.Contains(positions, r.Position) {
			positions = append(positions, r.Position)
		}
	}
	return database.InJobsTx(ctx, s.pool, tenantID, []int64{job.ID}, func(tx pgx.Tx, q db.Querier) error {
		task, err := s.guard.ApplyInTx(ctx, tx, tenantID, decision)
		if err != nil {
			return err
		}
		for _, r := range rows[1:] {
			if _, err := q.LockRowForJoin(ctx, db.LockRowForJoinParams{
				ID: r.TranslationID, ExpectedUpdatedAt: timestamptz(versionOr(expected[r.TranslationID], r)), ActorID: userID,
			}); errors.Is(err, pgx.ErrNoRows) {
				return s.explainMissedWrite(ctx, tenantID, userID, r.TranslationID)
			} else if err != nil {
				return err
			}
		}
		segmentID := plan.sharedID
		if segmentID == 0 {
			seg, err := q.CreateLanguageSegment(ctx, plan.segment)
			if err != nil {
				return err
			}
			segmentID = seg.ID
		}
		kept, err := s.moveTranslation(ctx, q, tenantID, userID, first, segmentID, versionOr(expected[first.TranslationID], first), plan.targetText, plan.targetRich)
		if err != nil {
			return err
		}
		removed := make([]int64, 0, len(rows)-1)
		for i, r := range rows[1:] {
			if err := s.foldInto(ctx, q, kept.ID, r.TranslationID, labels[i+1]); err != nil {
				return err
			}
			removed = append(removed, r.TranslationID)
		}
		if err := q.DeleteJoinedTranslations(ctx, removed); err != nil {
			return err
		}
		segments := make([]int64, len(rows))
		for i, r := range rows {
			segments[i] = r.SegmentID
		}
		if err := q.DeleteLanguageSegments(ctx, segments); err != nil {
			return err
		}
		for _, p := range positions {
			if err := q.RenumberLanguageRows(ctx, db.RenumberLanguageRowsParams{
				FileID: job.FileID, TargetLanguage: job.TargetLanguage, Position: p,
			}); err != nil {
				return err
			}
		}
		if err := s.afterSourceChange(ctx, q, kept.ID); err != nil {
			return err
		}
		details, err := historyDetails(map[string]any{"joined_segments": labels})
		if err != nil {
			return err
		}
		// Written last, so it is the newest text: the baseline the next
		// confirm's edit distance is measured from.
		_, err = q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
			TranslationID: kept.ID, Kind: db.TranslationHistoryKindJoined,
			Text: kept.ActiveText, RichText: kept.ActiveRichText,
			CreatedBy: &userID, JobStepAssignmentID: activeStep(task), Details: details,
		})
		return err
	})
}

// foldInto moves a removed row's comments and history to the kept row,
// labelled with the removed row's number. The caller deletes its translation.
func (s *Service) foldInto(ctx context.Context, q db.Querier, keptID, removedID int64, label string) error {
	if err := q.MoveSegmentComments(ctx, db.MoveSegmentCommentsParams{
		ToTranslationID: keptID, FromSegment: label, FromTranslationID: removedID,
	}); err != nil {
		return err
	}
	return q.MoveTranslationHistory(ctx, db.MoveTranslationHistoryParams{
		ToTranslationID: keptID, FromSegment: label, FromTranslationID: removedID,
	})
}

// joinTargets joins the rows' translations in order, each translation's chip
// ids moved into the joined source's numbering by shift. Empty targets are
// skipped; nil when every target is empty.
func joinTargets(rows []row, shift func(row) int) (text, rich *string) {
	var pieces []parser.Piece
	prev := ""
	for _, r := range rows {
		t := ptr.Deref(r.ActiveText)
		if t == "" {
			continue
		}
		p := parser.Piece{Text: t, Rich: ptr.Deref(r.ActiveRichText), ChipBase: shift(r)}
		if len(pieces) > 0 {
			p.Gap = targetSeparator(prev, t)
		}
		pieces = append(pieces, p)
		prev = t
	}
	if len(pieces) == 0 {
		return nil, nil
	}
	joined, joinedRich := parser.JoinPieces(pieces)
	return &joined, ptr.NilIfZero(joinedRich)
}
