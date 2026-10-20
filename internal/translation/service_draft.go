package translation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/suggestion"
)

var (
	// ErrBadSuggestion: the draft's insert token does not verify for this
	// segment — tampered, expired, or issued for another job or source.
	ErrBadSuggestion = errors.New("the inserted suggestion does not belong to this segment")
	// ErrSuggestionGone: the TM entry or engine the token names was deleted.
	ErrSuggestionGone = errors.New("the inserted suggestion is no longer available")
)

// CodeSuggestionGone is the `error.code` of the 409 for ErrSuggestionGone, so
// the editor tells it apart from a version conflict.
const CodeSuggestionGone = "suggestion_gone"

// SetSuggestionSigner verifies the insert tokens of Resources-panel inserts.
// Nil refuses every token.
func (s *Service) SetSuggestionSigner(sg *suggestion.Signer) { s.suggestions = sg }

// DraftParams is one draft save.
type DraftParams struct {
	ID, UserID        int64
	HumanText         string
	HumanRichText     *string
	EditingTimeMs     int64
	ExpectedUpdatedAt pgtype.Timestamptz
	// Suggestion, when set, is the verified TM match or MT suggestion this
	// draft inserts; the save records it as a TM / MT event.
	Suggestion *suggestion.Claims
	StepID     *int64
}

// VerifySuggestion checks an insert token against the segment it is used on:
// issued to this tenant, for this segment's job and source text, and for this
// segment when the token names one.
func (s *Service) VerifySuggestion(ctx context.Context, tenantID, fileID int64, t db.Translation, token string) (suggestion.Claims, error) {
	if s.suggestions == nil {
		return suggestion.Claims{}, ErrBadSuggestion
	}
	c, err := s.suggestions.Verify(tenantID, token)
	if err != nil {
		return suggestion.Claims{}, ErrBadSuggestion
	}
	// fileID is already tenant-verified with the translation in it.
	job, err := s.queries.GetJobByFileAndLang(ctx, db.GetJobByFileAndLangParams{FileID: fileID, TargetLanguage: t.TargetLanguage})
	if err != nil {
		return suggestion.Claims{}, fmt.Errorf("job of translation %d: %w", t.ID, err)
	}
	seg, err := s.queries.GetSegment(ctx, t.SegmentID)
	if err != nil {
		return suggestion.Claims{}, fmt.Errorf("segment of translation %d: %w", t.ID, err)
	}
	if c.JobID != job.ID || c.Source != suggestion.SourceHash(seg.SourceText) ||
		(c.TranslationID != 0 && c.TranslationID != t.ID) {
		return suggestion.Claims{}, ErrBadSuggestion
	}
	return c, nil
}

// SaveDraft writes a draft. With a suggestion the text write, the TM / MT
// event and its lineage on the row commit together, and the row returned is
// the one after the lineage write, so its updated_at is the editor's next
// expected version.
func (s *Service) SaveDraft(ctx context.Context, p DraftParams) (db.Translation, error) {
	args := db.SaveTranslationDraftParams{
		ID:                 p.ID,
		HumanText:          &p.HumanText,
		HumanRichText:      p.HumanRichText,
		DeltaEditingTimeMs: p.EditingTimeMs,
		EditedBy:           &p.UserID,
		ExpectedUpdatedAt:  p.ExpectedUpdatedAt,
	}
	if p.Suggestion == nil {
		return s.queries.SaveTranslationDraft(ctx, args)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.Translation{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)
	if _, err := q.SaveTranslationDraft(ctx, args); err != nil {
		return db.Translation{}, err
	}
	if err := pipeline.EmitAutomationHistoryInTx(ctx, q, insertEvent(p)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return db.Translation{}, ErrSuggestionGone
		}
		return db.Translation{}, fmt.Errorf("record inserted suggestion: %w", err)
	}
	saved, err := q.GetTranslation(ctx, p.ID)
	if err != nil {
		return db.Translation{}, fmt.Errorf("reload translation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Translation{}, fmt.Errorf("commit draft: %w", err)
	}
	return saved, nil
}

// insertEvent is the TM / MT event of a panel insert, shaped like the one
// pre-translation records for the same suggestion.
func insertEvent(p DraftParams) pipeline.AutomationEmitOpts {
	c := p.Suggestion
	opts := pipeline.AutomationEmitOpts{
		TranslationID:       p.ID,
		Text:                &p.HumanText,
		RichText:            p.HumanRichText,
		CreatedBy:           &p.UserID,
		JobStepAssignmentID: p.StepID,
		Details:             map[string]any{"source": "panel_insert"},
	}
	if c.Kind == suggestion.KindTM {
		opts.Kind, opts.Origin = db.TranslationHistoryKindTm, db.TranslationOriginTm
		opts.TmID, opts.TmEntryID, opts.TmMatchPercent = &c.TMID, &c.TMEntryID, &c.Percent
		return opts
	}
	opts.Kind, opts.Origin = db.TranslationHistoryKindMt, db.TranslationOriginMt
	opts.MtEngineID, opts.AIEngineID = c.MTEngineID, c.AIEngineID
	if c.EngineName != "" {
		opts.Details["engine_name"] = c.EngineName
	}
	return opts
}
