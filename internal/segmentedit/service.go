// Package segmentedit splits and joins segments in one job (file + target
// language). A segment shared by every language of the file is never changed:
// the job gets language rows of its own (segments.target_language) and its
// translation moves onto them, so the file's other languages keep the import
// segmentation. Export folds the rows back into the file's original keys.
package segmentedit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/hold"
	"openloc/internal/pipeline"
	"openloc/internal/taskedit"
)

// Service splits and joins the rows of a job.
type Service struct {
	q     db.Querier
	pool  *pgxpool.Pool
	guard *taskedit.Guard
	// storage holds the uploaded originals, which a join across sentences
	// reads as export does. nil refuses such a join.
	storage pipeline.FileStorage
}

func NewService(q db.Querier, pool *pgxpool.Pool, storage pipeline.FileStorage) *Service {
	return &Service{q: q, pool: pool, guard: taskedit.New(q, pool), storage: storage}
}

type row = db.ListJobEditRowsByTranslationsRow

// Number is a row's segment number: the import number, and the piece of a
// split after a dot ("3", "3.1").
func Number(position, subPosition int32) string {
	if subPosition == 0 {
		return fmt.Sprint(position + 1)
	}
	return fmt.Sprintf("%d.%d", position+1, subPosition)
}

// checkJob refuses an edit the pipeline or the job's current task does not
// allow. It returns the task the edit is recorded under.
func (s *Service) checkJob(ctx context.Context, job db.Job, tenantID int64) (taskedit.Decision, error) {
	if pipeline.IsPipelineRunning(job.Status, job.CurrentStep) {
		return taskedit.Decision{}, ErrPipelineRunning
	}
	return s.guard.Check(ctx, tenantID, taskedit.Ref{JobID: job.ID}, taskedit.Opts{NoReopen: true})
}

// checkRow refuses a row that is locked, being pre-translated or held by
// another user.
func (s *Service) checkRow(ctx context.Context, tenantID, userID int64, r row) error {
	switch {
	case r.Locked:
		return ErrLocked
	case r.InFlightPhase.Valid:
		return ErrInFlight
	}
	held, err := hold.HeldByOther(ctx, s.q, tenantID, r.TranslationID, userID)
	if err != nil {
		return err
	}
	if held != nil {
		return held
	}
	return nil
}

// explainMissedWrite says why a guarded write matched no row: another user
// holds it, or it changed since the editor read it.
func (s *Service) explainMissedWrite(ctx context.Context, tenantID, userID, translationID int64) error {
	held, err := hold.HeldByOther(ctx, s.q, tenantID, translationID, userID)
	if err != nil {
		return err
	}
	if held != nil {
		return held
	}
	return ErrConflict
}

// moveTranslation moves a translation onto segmentID with text, or explains
// why the guarded write matched nothing.
func (s *Service) moveTranslation(ctx context.Context, q db.Querier, tenantID, userID int64, r row, segmentID int64, expected time.Time, text, rich *string) (db.Translation, error) {
	if err := q.SnapshotHistoryCounts(ctx, r.TranslationID); err != nil {
		return db.Translation{}, err
	}
	t, err := q.MoveEditedTranslation(ctx, db.MoveEditedTranslationParams{
		SegmentID: segmentID, ActiveText: text, ActiveRichText: rich, ActorID: &userID,
		ID: r.TranslationID, ExpectedUpdatedAt: timestamptz(expected),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Translation{}, s.explainMissedWrite(ctx, tenantID, userID, r.TranslationID)
	}
	return t, err
}

// versionOr is the version a write checks: the one the caller read, or with
// none (an API client that does not send one) the row's version now.
func versionOr(expected time.Time, r row) time.Time {
	if expected.IsZero() {
		return r.UpdatedAt.Time
	}
	return expected
}

// exportKey is the original key a row rebuilds: its own key for a shared
// segment, the key it came from for a language row.
func exportKey(r row) string {
	if r.ExportKey != nil {
		return *r.ExportKey
	}
	if r.SegmentKey != nil {
		return *r.SegmentKey
	}
	return ""
}

// utf16Offset turns an offset in UTF-16 code units, as the editor counts, into
// a byte offset of s. ok is false when it falls outside s or inside a
// character.
func utf16Offset(s string, units int) (int, bool) {
	n := 0
	for i, r := range s {
		switch {
		case n == units:
			return i, true
		case n > units:
			return 0, false
		}
		n += utf16.RuneLen(r)
	}
	return len(s), n == units
}

// targetSeparator is what joins two translations: nothing next to a script
// written without spaces between words, or where either side already has the
// space; else one space.
func targetSeparator(before, after string) string {
	last, _ := utf8.DecodeLastRuneInString(before)
	first, _ := utf8.DecodeRuneInString(after)
	if unicode.IsSpace(last) || unicode.IsSpace(first) || unspaced(last) || unspaced(first) {
		return ""
	}
	return " "
}

func unspaced(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana,
		unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar)
}

func historyDetails(v map[string]any) ([]byte, error) {
	return json.Marshal(v)
}
