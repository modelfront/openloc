package translation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/hold"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/taskedit"
)

// Batch draft save — apply many text corrections to a file's translations in
// one request. Built for the spell check review dialog's "Change all", but
// deliberately generic: it's the correct write path for any bulk text edit.
//
// Why draft semantics and not `UpdateTranslationHuman`: a spelling correction
// is an edit, and an edit downgrades status to `needs_review`. `UpdateTranslationHuman` sets
// `status = 'approved'` unconditionally, so reusing it would silently confirm
// every segment a bulk fix happened to touch.
//
// A locked segment is not written unless the request sets `include_locked`
// and the caller is an owner or admin; otherwise its id comes back in `locked`.
// A segment another user holds is never written; its id comes back in `held`.
//
// Why not one transaction: these are N independent draft saves, not a
// multi-step invariant. If one row lost an optimistic-lock race because a
// colleague edited it, rolling back the other 39 corrections would force the
// linguist to redo the entire review. Each row therefore reports its own
// outcome and the caller decides what to retry.

// MaxBatchDraftFixes caps one batch.
const MaxBatchDraftFixes = 500

type batchDraftFix struct {
	TranslationID     int64      `json:"translation_id"`
	HumanText         string     `json:"human_text"`
	HumanRichText     *string    `json:"human_rich_text"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at"`
}

type batchDraftRequest struct {
	Fixes []batchDraftFix `json:"fixes"`
	// IncludeLocked writes locked segments too. Honoured for owners and admins
	// only — the roles that may unlock (api.go); for any other role it is read
	// as false and the locked rows come back in `locked`.
	//
	// Off by default: a lock means "don't touch," so silently writing locked
	// rows would remove that gate for every caller. Spell check's "All segments
	// (includes locked)" scope opts in explicitly, which keeps the bypass
	// visible in the request rather than buried in the handler.
	IncludeLocked bool `json:"include_locked"`
}

// BatchDraftResult reports each id's outcome so a partial failure is visible
// rather than reported as success.
type BatchDraftResult struct {
	Applied   []db.Translation `json:"applied"`
	Conflicts []int64          `json:"conflicts"`
	Locked    []int64          `json:"locked"`
	// Held: another user has the segment open.
	Held     []int64 `json:"held"`
	Rejected []int64 `json:"rejected"`
	// TaskReopened: the batch reopened a completed task. Sent beside the
	// result (task_reopened), not inside it.
	TaskReopened bool `json:"-"`

	holders map[int64]*hold.HeldError
}

// HeldError is the refusal for an id in Held, naming its holder.
func (r BatchDraftResult) HeldError(id int64) *hold.HeldError {
	return r.holders[id]
}

// DraftFix is one draft text update in a batch.
type DraftFix struct {
	TranslationID     int64
	HumanText         string
	HumanRichText     *string
	ExpectedUpdatedAt pgtype.Timestamptz
}

// HandleBatchDraft applies a set of draft text updates.
// PUT /api/files/{fileID}/translations/batch-draft
//
//	{ applied: [Translation], conflicts: [id], locked: [id], held: [id], rejected: [id] }
//
// Open to every member who reaches the file (a linguist through an assigned
// job); writing through a lock with `include_locked` is owner/admin only.
func (h *Handler) HandleBatchDraft(w http.ResponseWriter, r *http.Request) {
	fileID, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}

	var req batchDraftRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if len(req.Fixes) == 0 {
		httputil.BadRequest(w, "fixes is required")
		return
	}
	if len(req.Fixes) > MaxBatchDraftFixes {
		httputil.BadRequest(w, "too many fixes in one request")
		return
	}
	fixes := make([]DraftFix, len(req.Fixes))
	for i, f := range req.Fixes {
		fixes[i] = DraftFix{
			TranslationID:     f.TranslationID,
			HumanText:         f.HumanText,
			HumanRichText:     f.HumanRichText,
			ExpectedUpdatedAt: expectedUpdatedAtArg(f.ExpectedUpdatedAt),
		}
	}
	// Only a user who may write a locked segment (MayWriteLocked, as in find
	// and replace) writes through a lock; for anyone else the flag is read as
	// false and the rows are reported in `locked`, not refused.
	includeLocked := req.IncludeLocked && MayWriteLocked(r.Context())
	res, err := h.BatchDraft(r.Context(), auth.TenantIDFromContext(r.Context()),
		auth.UserIDFromContext(r.Context()), fileID, fixes, includeLocked)
	if httputil.ServiceError(w, err, "save", "drafts") {
		return
	}
	httputil.JSON(w, http.StatusOK, taskedit.WithReopened(map[string]any{"data": res}, res.TaskReopened))
}

// BatchDraft applies draft text updates to translations of one file. The
// caller has authorised fileID; each id is re-checked against that file in
// the tenant, and a linguist's against the jobs they hold, so a linguist
// cannot write into a sibling file or another vendor's language. Locked rows are
// reported, not written, unless includeLocked — find and replace or spell
// check for owner/admin — in which case the draft unlocks them. Rows another
// user holds are reported and never written. A refusal from the task edit
// guard rejects the whole batch with no write.
func (s *Service) BatchDraft(ctx context.Context, tenantID, userID, fileID int64, fixes []DraftFix, includeLocked bool) (BatchDraftResult, error) {
	res := BatchDraftResult{
		Applied:   make([]db.Translation, 0, len(fixes)),
		Conflicts: make([]int64, 0),
		Locked:    make([]int64, 0),
		Held:      make([]int64, 0),
		Rejected:  make([]int64, 0),
		holders:   map[int64]*hold.HeldError{},
	}
	type pendingFix struct {
		fix      DraftFix
		existing db.Translation
	}
	pending := make([]pendingFix, 0, len(fixes))
	// A linguist writes only the languages whose job they hold.
	langAllowed := map[string]bool{}
	for _, fix := range fixes {
		if fix.HumanText == "" {
			res.Rejected = append(res.Rejected, fix.TranslationID)
			continue
		}
		existing, err := s.queries.GetTranslationInFileTenant(ctx, db.GetTranslationInFileTenantParams{
			ID: fix.TranslationID, FileID: fileID, TenantID: tenantID,
		})
		if err != nil {
			res.Rejected = append(res.Rejected, fix.TranslationID)
			continue
		}
		allowed, seen := langAllowed[existing.TargetLanguage]
		if !seen {
			allowed, err = auth.LinguistMayAccessFileLanguage(ctx, s.queries, fileID, existing.TargetLanguage)
			if err != nil {
				return BatchDraftResult{}, fmt.Errorf("verify language assignment: %w", err)
			}
			langAllowed[existing.TargetLanguage] = allowed
		}
		if !allowed {
			res.Rejected = append(res.Rejected, fix.TranslationID)
			continue
		}
		if existing.Locked && !includeLocked {
			res.Locked = append(res.Locked, fix.TranslationID)
			continue
		}
		// Held rows leave the batch before the task guard, so a batch that
		// writes nothing reopens nothing.
		held, err := hold.HeldByOther(ctx, s.queries, tenantID, fix.TranslationID, userID)
		if err != nil {
			return BatchDraftResult{}, err
		}
		if held != nil {
			res.Held = append(res.Held, fix.TranslationID)
			res.holders[fix.TranslationID] = held
			continue
		}
		pending = append(pending, pendingFix{fix: fix, existing: existing})
	}

	// Each target language is its own job. Check every job's current task
	// before anything is written, so a refusal rejects the whole batch, and
	// reopen a completed task only once every job has passed.
	decisions := map[string]taskedit.Decision{}
	for _, p := range pending {
		if _, seen := decisions[p.existing.TargetLanguage]; seen {
			continue
		}
		d, err := s.guard.Check(ctx, tenantID, taskedit.Ref{TranslationID: p.fix.TranslationID}, taskedit.Opts{})
		if err != nil {
			return BatchDraftResult{}, err
		}
		decisions[p.existing.TargetLanguage] = d
	}
	tasks := make(map[string]taskedit.Result, len(decisions))
	for lang, d := range decisions {
		task, err := s.guard.Apply(ctx, tenantID, d)
		if err != nil {
			return BatchDraftResult{}, err
		}
		tasks[lang] = task
		res.TaskReopened = res.TaskReopened || task.Reopened
	}

	for _, p := range pending {
		fix := p.fix
		t, err := s.queries.SaveTranslationDraft(ctx, db.SaveTranslationDraftParams{
			ID:                 fix.TranslationID,
			HumanText:          &fix.HumanText,
			HumanRichText:      fix.HumanRichText,
			DeltaEditingTimeMs: 0,
			EditedBy:           &userID,
			ExpectedUpdatedAt:  fix.ExpectedUpdatedAt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// The same disambiguation HandleSaveDraft makes.
			err = ExplainNoRows(ctx, s.queries, tenantID, fix.TranslationID, userID, fix.ExpectedUpdatedAt.Valid)
		}
		var held *hold.HeldError
		switch {
		case errors.As(err, &held):
			res.Held = append(res.Held, fix.TranslationID)
			res.holders[fix.TranslationID] = held
			continue
		case errors.Is(err, ErrConflict):
			res.Conflicts = append(res.Conflicts, fix.TranslationID)
			continue
		case err != nil:
			logging.Error(ctx, err, "batch draft save failed", "batch-draft-save-failed", logging.FileID(fileID), logging.TranslationID(fix.TranslationID))
			res.Rejected = append(res.Rejected, fix.TranslationID)
			continue
		}
		s.emitUnapproval(ctx, fix.TranslationID, p.existing.Status, userID, activeStep(tasks[p.existing.TargetLanguage]))
		res.Applied = append(res.Applied, t)
	}
	return res, nil
}
