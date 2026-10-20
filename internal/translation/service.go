package translation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/ptr"
	"openloc/internal/qa"
	"openloc/internal/sliceutil"
	"openloc/internal/strdist"
	"openloc/internal/suggestion"
	"openloc/internal/taskedit"
)

// Service holds the segment writes shared by the REST handlers and the MCP
// tools: confirm, clear, batch draft, history. Every method resolves the
// translation tenant-scoped first; pgx.ErrNoRows means "not in your tenant".
type Service struct {
	queries   db.Querier
	pool      *pgxpool.Pool
	tmWriter  TMWriter  // nil = no auto-TM save
	qaChecker QAChecker // nil = QA enforcement disabled (legacy/test paths)
	guard     *taskedit.Guard
	// suggestions verifies Resources-panel insert tokens; nil refuses them.
	suggestions *suggestion.Signer
}

// NewService builds the service. tw and qc may be nil (see Service fields).
func NewService(q db.Querier, pool *pgxpool.Pool, tw TMWriter, qc QAChecker) *Service {
	return &Service{queries: q, pool: pool, tmWriter: tw, qaChecker: qc, guard: taskedit.New(q, pool)}
}

// Who confirmed an approved segment (translations.confirmed_via).
const (
	ConfirmedViaUser      = "user"
	ConfirmedViaAssistant = "assistant"
)

var (
	// ErrLocked: the segment is locked and must be unlocked first.
	ErrLocked = errors.New("segment is locked")
	// ErrConflict: expected_updated_at no longer matches — someone else wrote.
	ErrConflict = errors.New("translation was updated by another user")
	// ErrNotFirstStep: clear is only available in the first workflow step.
	ErrNotFirstStep = errors.New("clear is only available in the first workflow step")
)

// QABlockedError is returned when QA errors block a confirm.
type QABlockedError struct{ Issues []qa.Issue }

func (e *QABlockedError) Error() string { return "fix QA errors before confirming" }

// KeptRichText is the rich text a write of text sends for t: its own inline
// tags when text is its current text unchanged, none for new text. API and
// assistant writes carry plain text only.
func KeptRichText(t db.Translation, text string) *string {
	if t.ActiveText != nil && *t.ActiveText == text {
		return t.ActiveRichText
	}
	return nil
}

// ConfirmParams is one confirm (Cmd+Enter in the editor).
type ConfirmParams struct {
	ID, TenantID, UserID int64
	// FileID, when set, requires the translation to belong to this file
	// (pgx.ErrNoRows otherwise). The /api/files/{fileID} routes set it.
	FileID        int64
	HumanText     string
	HumanRichText *string
	EditingTimeMs int64
	// ExpectedUpdatedAt: when valid, the write applies only if updated_at
	// still matches (optimistic locking); otherwise ErrConflict.
	ExpectedUpdatedAt pgtype.Timestamptz
	// Via is ConfirmedViaUser or ConfirmedViaAssistant. An assistant confirm
	// writes nothing to translation memory (pipeline.SaveToTM skips it).
	Via string
}

// ConfirmResult is the confirmed row, the TMs that received it, and whether
// the confirm reopened the job's completed task.
type ConfirmResult struct {
	Translation  db.Translation
	UpdatedTMIDs []int64
	TaskReopened bool
}

// Confirm approves a segment's text: locked / task / QA gates, the write, the
// history row and the TM write-back. A manager's confirm on a completed task
// reopens it — after QA, so a blocked confirm reopens nothing.
func (s *Service) Confirm(ctx context.Context, p ConfirmParams) (ConfirmResult, error) {
	if p.Via == "" {
		p.Via = ConfirmedViaUser
	}
	old, edit, err := s.preflightConfirm(ctx, p)
	if err != nil {
		return ConfirmResult{}, err
	}
	task, err := s.guard.Apply(ctx, p.TenantID, edit)
	if err != nil {
		return ConfirmResult{}, err
	}
	return s.commitConfirm(ctx, p, old.ActiveText, task)
}

// commitConfirm writes a confirm whose gates have passed and whose task guard
// has been applied: the update, the history row and the TM write-back.
// prevText is the row's active_text before the write.
func (s *Service) commitConfirm(ctx context.Context, p ConfirmParams, prevText *string, task taskedit.Result) (ConfirmResult, error) {
	// Edit distance measures how much this confirm changed what came before —
	// the machine's output on a first edit, the previous editor's text on a
	// later one. The baseline is the newest history row, NOT active_text: draft
	// autosaves overwrite active_text without emitting history, so by the time
	// a confirm lands it already holds the text being confirmed. Falls back to
	// active_text when the segment has no history yet.
	var editDist int32
	baseline := ""
	if prev, err := s.queries.GetLatestHistoryTextForTranslation(ctx, p.ID); err == nil && prev != nil {
		baseline = *prev
	} else if prevText != nil {
		baseline = *prevText
	}
	if baseline != "" {
		editDist = int32(strdist.Levenshtein(baseline, p.HumanText))
	}
	deltaMs := max(p.EditingTimeMs, 0)

	via := p.Via
	t, err := s.queries.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:                 p.ID,
		HumanText:          &p.HumanText,
		HumanRichText:      p.HumanRichText,
		DeltaEditingTimeMs: deltaMs,
		EditDistance:       editDist,
		EditedBy:           &p.UserID,
		ExpectedUpdatedAt:  p.ExpectedUpdatedAt,
		ConfirmedVia:       &via,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ConfirmResult{}, ExplainNoRows(ctx, s.queries, p.TenantID, p.ID, p.UserID, p.ExpectedUpdatedAt.Valid)
	}
	if err != nil {
		return ConfirmResult{}, err
	}

	// The history row. Does NOT touch translations.origin / tm_match_percent
	// (sticky through human edits). A confirm that leaves the text
	// as it was is an approval, not an edit: it records `approved` with no
	// text, so it neither mislabels the panel nor becomes the next baseline.
	// The active workflow step is recorded for the per-step productivity report.
	editingDelta := deltaMs
	opts := pipeline.HumanEmitOpts{
		TranslationID:       p.ID,
		Kind:                db.TranslationHistoryKindHuman,
		Text:                &p.HumanText,
		RichText:            p.HumanRichText,
		CreatedBy:           &p.UserID,
		EditDistance:        &editDist,
		EditingTimeMs:       &editingDelta,
		JobStepAssignmentID: activeStep(task),
	}
	if p.HumanText == baseline {
		opts.Kind = db.TranslationHistoryKindApproved
		opts.Text, opts.RichText, opts.EditDistance = nil, nil, nil
	}
	if p.Via == ConfirmedViaAssistant {
		opts.Details = map[string]any{"confirmed_via": ConfirmedViaAssistant}
	}
	pipeline.EmitHumanHistory(ctx, s.queries, opts)

	// TM write-back is best-effort: a TM failure must not fail the confirm,
	// which has already committed. SaveToTM itself skips assistant confirms.
	var updatedTMIDs []int64
	if s.tmWriter != nil {
		ids, err := s.tmWriter.SaveToTM(ctx, p.TenantID, p.ID)
		if err != nil {
			logging.Error(ctx, err, "auto-save to TM failed", "tm-write-auto-save-failed", logging.TranslationID(p.ID))
		}
		updatedTMIDs = ids
	} else {
		logging.Warn(ctx, nil, "no TM writer wired; translation not saved to TM", "tm-write-no-writer", logging.TranslationID(p.ID))
	}
	return ConfirmResult{Translation: t, UpdatedTMIDs: sliceutil.NonNil(updatedTMIDs), TaskReopened: task.Reopened}, nil
}

// PreflightConfirm runs every gate Confirm runs — tenant, lock, another
// user's hold, the job's current task, QA — without writing, and returns the
// current row. A preview uses it so it refuses exactly what the confirm would
// refuse.
func (s *Service) PreflightConfirm(ctx context.Context, p ConfirmParams) (db.Translation, error) {
	old, _, err := s.preflightConfirm(ctx, p)
	return old, err
}

func (s *Service) preflightConfirm(ctx context.Context, p ConfirmParams) (db.Translation, taskedit.Decision, error) {
	var old db.Translation
	var err error
	if p.FileID != 0 {
		old, err = s.queries.GetTranslationInFileTenant(ctx, db.GetTranslationInFileTenantParams{ID: p.ID, FileID: p.FileID, TenantID: p.TenantID})
	} else {
		old, err = s.queries.GetTranslationTenant(ctx, db.GetTranslationTenantParams{ID: p.ID, TenantID: p.TenantID})
	}
	if err != nil {
		return db.Translation{}, taskedit.Decision{}, err
	}
	if err := requireLinguistLanguage(ctx, s.queries, old.ID); err != nil {
		return db.Translation{}, taskedit.Decision{}, err
	}
	if old.Locked {
		return db.Translation{}, taskedit.Decision{}, ErrLocked
	}
	if err := CheckHeld(ctx, s.queries, p.TenantID, p.ID, p.UserID); err != nil {
		return db.Translation{}, taskedit.Decision{}, err
	}
	edit, err := s.guard.Check(ctx, p.TenantID, taskedit.Ref{TranslationID: p.ID}, taskedit.Opts{})
	if err != nil {
		return db.Translation{}, taskedit.Decision{}, err
	}
	if err := s.checkSourceTags(ctx, old, p.HumanText, p.HumanRichText); err != nil {
		return db.Translation{}, taskedit.Decision{}, err
	}
	if err := s.checkQA(ctx, p.ID, p.TenantID, p.HumanText, p.HumanRichText); err != nil {
		return db.Translation{}, taskedit.Decision{}, err
	}
	return old, edit, nil
}

// checkQA runs the save-time QA gate on a proposed confirm. nil means allowed:
// no checker wired, no QA profile on the project, or no error-severity issue.
// A checker failure is an error, never a pass — a flaky term lookup must not
// open a window around enforcement. Drafts never call this.
func (s *Service) checkQA(ctx context.Context, translationID, tenantID int64, text string, rich *string) error {
	if s.qaChecker == nil {
		return nil
	}
	result, err := s.qaChecker.CheckTranslationProposal(ctx, qa.CheckProposalParams{
		TranslationID:    translationID,
		TenantID:         tenantID,
		ProposedText:     text,
		ProposedRichText: ptr.Deref(rich),
	})
	if err != nil {
		return fmt.Errorf("qa check: %w", err)
	}
	if result.HasErrors() {
		return &QABlockedError{Issues: result.Issues}
	}
	return nil
}

// ClearResult is the cleared row, and whether the clear reopened the job's
// completed task.
type ClearResult struct {
	Translation  db.Translation
	TaskReopened bool
}

// Clear reverts one translation to untranslated. fileID must be the file the
// translation's segment belongs to — the workflow gate checks that file's job.
func (s *Service) Clear(ctx context.Context, tenantID, userID, fileID, id int64) (ClearResult, error) {
	_, edit, err := s.preflightClear(ctx, tenantID, userID, fileID, id)
	if err != nil {
		return ClearResult{}, err
	}
	task, err := s.guard.Apply(ctx, tenantID, edit)
	if err != nil {
		return ClearResult{}, err
	}

	// Blanking the target and dropping its QA findings and AI remarks are one
	// mutation: a clear is an UPDATE, so nothing cascades.
	// A human comment is about the segment and stays.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClearResult{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)
	cleared, err := q.ClearTranslation(ctx, db.ClearTranslationParams{ID: id, ActorID: &userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ClearResult{}, ExplainNoRows(ctx, s.queries, tenantID, id, userID, false)
	}
	if err != nil {
		return ClearResult{}, fmt.Errorf("clear translation: %w", err)
	}
	if err := q.DeleteQAResultsForTranslations(ctx, []int64{id}); err != nil {
		return ClearResult{}, fmt.Errorf("clear QA findings: %w", err)
	}
	if err := q.DeleteAICommentsForTranslations(ctx, []int64{id}); err != nil {
		return ClearResult{}, fmt.Errorf("clear AI comments: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ClearResult{}, fmt.Errorf("commit clear: %w", err)
	}

	// `cleared` is a text-less sentinel event; origin stays sticky.
	pipeline.EmitHumanHistory(ctx, s.queries, pipeline.HumanEmitOpts{
		TranslationID: id,
		Kind:          db.TranslationHistoryKindCleared,
		CreatedBy:     &userID,
	})
	return ClearResult{Translation: cleared, TaskReopened: task.Reopened}, nil
}

// PreflightClear runs every gate Clear runs without writing and returns the
// current row. The tenant check runs first, so a cross-tenant probe cannot
// tell "locked" from "doesn't exist".
func (s *Service) PreflightClear(ctx context.Context, tenantID, userID, fileID, id int64) (db.Translation, error) {
	existing, _, err := s.preflightClear(ctx, tenantID, userID, fileID, id)
	return existing, err
}

func (s *Service) preflightClear(ctx context.Context, tenantID, userID, fileID, id int64) (db.Translation, taskedit.Decision, error) {
	existing, err := s.queries.GetTranslationTenant(ctx, db.GetTranslationTenantParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.Translation{}, taskedit.Decision{}, err
	}
	// The segment must belong to fileID — otherwise a step-locked translation
	// could be cleared through another (step-0) file's URL.
	seg, err := s.queries.GetSegment(ctx, existing.SegmentID)
	if err != nil || seg.FileID != fileID {
		return db.Translation{}, taskedit.Decision{}, pgx.ErrNoRows
	}
	if err := requireLinguistLanguage(ctx, s.queries, existing.ID); err != nil {
		return db.Translation{}, taskedit.Decision{}, err
	}
	if existing.Locked {
		return db.Translation{}, taskedit.Decision{}, ErrLocked
	}
	if err := CheckHeld(ctx, s.queries, tenantID, id, userID); err != nil {
		return db.Translation{}, taskedit.Decision{}, err
	}
	edit, err := s.guard.Check(ctx, tenantID, taskedit.Ref{TranslationID: id}, taskedit.Opts{})
	if err != nil {
		return db.Translation{}, taskedit.Decision{}, err
	}
	if !edit.InFirstStep() {
		return db.Translation{}, taskedit.Decision{}, ErrNotFirstStep
	}
	return existing, edit, nil
}

// emitUnapproval records the moment a segment loses its approval, from either
// draft path. Keyed on the PRIOR status, so only the first draft after a
// confirm writes a row. Carries no text: a draft is work in progress, and a
// text row would become the next confirm's edit-distance baseline.
func (s *Service) emitUnapproval(ctx context.Context, translationID int64, prior db.TranslationStatus, userID int64, stepID *int64) {
	if prior != db.TranslationStatusApproved && prior != db.TranslationStatusAutoApproved {
		return
	}
	pipeline.EmitHumanHistory(ctx, s.queries, pipeline.HumanEmitOpts{
		TranslationID:       translationID,
		Kind:                db.TranslationHistoryKindUnconfirmed,
		CreatedBy:           &userID,
		JobStepAssignmentID: stepID,
	})
}
