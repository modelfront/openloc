package translation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
	"openloc/internal/suggestion"
	"openloc/internal/taskedit"
)

// TMWriter saves a confirmed translation to Translation Memory.
//
// Returns the IDs of TMs that successfully received the write (or nil if
// nothing was written — no human_text, no configured write TMs, etc.).
// HandleUpdateHuman echoes these IDs back to the FE in `updated_tm_ids` so
// the TM browser can invalidate the right caches without a heuristic guess.
//
// tenantID is the explicit scope — SaveToTM uses it as the entry guard for
// the translation lookup.
type TMWriter interface {
	SaveToTM(ctx context.Context, tenantID, translationID int64) ([]int64, error)
	SaveTranslationsToTM(ctx context.Context, tenantID int64, translationIDs []int64, reason string) (int, error)
}

// Handler serves the translation routes over the shared Service.
type Handler struct {
	*Service
}

// NewHandler constructs the translation handler with the writer + checker
// dependencies.
//
// `qaChecker` may be nil — older test routers and any caller that doesn't
// want save-time QA enforcement just skips the check. Production wiring
// (api.go) always passes a real `qa.Service`.
func NewHandler(q db.Querier, pool *pgxpool.Pool, tw TMWriter, qc QAChecker) *Handler {
	return &Handler{Service: NewService(q, pool, tw, qc)}
}

func (h *Handler) HandleListByFile(w http.ResponseWriter, r *http.Request) {
	fileID, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}

	lang := langcode.Normalize(r.URL.Query().Get("lang"))
	if lang == "" {
		httputil.BadRequest(w, "lang query parameter is required")
		return
	}
	if !auth.LinguistFileLanguageAccess(w, r, h.queries, fileID, lang) {
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	translations, err := h.queries.ListTranslationsByFileTenant(r.Context(), db.ListTranslationsByFileTenantParams{
		FileID:         fileID,
		TargetLanguage: lang,
		TenantID:       tenantID,
	})
	if err != nil {
		httputil.ServerError(w, "list", "translations")
		return
	}
	httputil.Data(w, translations)
}

// routeIDs parses the {fileID} and {translationID} route params.
func routeIDs(w http.ResponseWriter, r *http.Request) (fileID, id int64, ok bool) {
	fileID, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return 0, 0, false
	}
	id, err = httputil.ParseID(r, "translationID")
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return 0, 0, false
	}
	return fileID, id, true
}

// routeTranslation loads the route's translation, scoped to the tenant AND to
// the route's file — the file is what the /api/files/{fileID} middleware
// authorised. A translation of another file answers 404, like a missing one;
// one of a language whose job the linguist does not hold answers 403.
func (h *Handler) routeTranslation(w http.ResponseWriter, r *http.Request) (db.Translation, bool) {
	t, _, ok := h.routeTranslationInFile(w, r)
	return t, ok
}

// routeTranslationInFile is routeTranslation that also returns the file id.
func (h *Handler) routeTranslationInFile(w http.ResponseWriter, r *http.Request) (db.Translation, int64, bool) {
	fileID, id, ok := routeIDs(w, r)
	if !ok {
		return db.Translation{}, 0, false
	}
	t, err := h.queries.GetTranslationInFileTenant(r.Context(), db.GetTranslationInFileTenantParams{
		ID:       id,
		FileID:   fileID,
		TenantID: auth.TenantIDFromContext(r.Context()),
	})
	if httputil.DBError(w, err, "translation") {
		return db.Translation{}, 0, false
	}
	if err := requireLinguistLanguage(r.Context(), h.queries, t.ID); err != nil {
		httputil.ServiceError(w, err, "verify", "language assignment")
		return db.Translation{}, 0, false
	}
	return t, fileID, true
}

// requireLinguistLanguage refuses a linguist who does not hold the job of the
// translation's file and target language. The /api/files/{fileID} gate passes
// on any job of the file, and a file's languages are often different vendors'.
func requireLinguistLanguage(ctx context.Context, q db.Querier, translationID int64) error {
	ok, err := auth.LinguistMayAccessTranslation(ctx, q, translationID)
	if err != nil {
		return fmt.Errorf("verify language assignment: %w", err)
	}
	if !ok {
		return httputil.NewForbiddenError(errLanguageNotAssigned)
	}
	return nil
}

const errLanguageNotAssigned = "this language is not assigned to you"

func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	t, ok := h.routeTranslation(w, r)
	if !ok {
		return
	}
	httputil.Data(w, t)
}

type updateHumanRequest struct {
	HumanText     string  `json:"human_text"`
	HumanRichText *string `json:"human_rich_text,omitempty"` // text with XLIFF inline tags for round-trip export
	EditingTimeMs int64   `json:"editing_time_ms"`           // delta in ms to add
	// ExpectedUpdatedAt (Section 5.1 — optimistic locking): the updated_at value
	// the client last saw for this translation. When non-null, the UPDATE only
	// applies if the DB's updated_at still matches — otherwise the handler returns
	// 409 Conflict and the client should reload. Omitting the field preserves the
	// legacy "last writer wins" semantics for callers that don't track version.
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

// expectedUpdatedAtArg converts the optional *time.Time into the pgtype value
// sqlc expects. nil means "skip the version check".
func expectedUpdatedAtArg(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// writeConflictResponse emits a 409 with both the legacy `{error: ...}` shape
// and a `conflict` payload describing the row's CURRENT server-side state, so
// the frontend can show both versions side by side without a second
// round-trip. If the conflict-details query fails (the row was deleted between
// the failed save and this lookup, or a cross-tenant access tried to slip
// through), we fall back to the plain-error 409.
func (h *Handler) writeConflictResponse(ctx context.Context, w http.ResponseWriter, id, tenantID int64) {
	row, err := h.queries.GetTranslationConflictDetails(ctx, db.GetTranslationConflictDetailsParams{
		ID:       id,
		TenantID: tenantID,
	})
	if err != nil {
		httputil.Error(w, http.StatusConflict, "translation was updated by another user — reload and try again")
		return
	}
	var updatedAt *time.Time
	if row.UpdatedAt.Valid {
		t := row.UpdatedAt.Time
		updatedAt = &t
	}
	httputil.JSON(w, http.StatusConflict, map[string]any{
		"error": map[string]any{
			"code":    http.StatusText(http.StatusConflict),
			"message": "translation was updated by another user — reload and try again",
		},
		"conflict": map[string]any{
			// The "their version" banner shows the current visible target
			// (active_text / active_rich_text).
			"id":               row.ID,
			"active_text":      row.ActiveText,
			"active_rich_text": row.ActiveRichText,
			"updated_at":       updatedAt,
			"edited_by":        row.EditedBy,
			"edited_by_name":   row.EditedByName,
		},
	})
}

func (h *Handler) HandleUpdateHuman(w http.ResponseWriter, r *http.Request) {
	fileID, id, ok := routeIDs(w, r)
	if !ok {
		return
	}

	var req updateHumanRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.HumanText == "" {
		httputil.BadRequest(w, "human_text is required")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	res, err := h.Confirm(r.Context(), ConfirmParams{
		ID:                id,
		FileID:            fileID,
		TenantID:          tenantID,
		UserID:            auth.UserIDFromContext(r.Context()),
		HumanText:         req.HumanText,
		HumanRichText:     req.HumanRichText,
		EditingTimeMs:     req.EditingTimeMs,
		ExpectedUpdatedAt: expectedUpdatedAtArg(req.ExpectedUpdatedAt),
		Via:               ConfirmedViaUser,
	})
	if err != nil {
		h.WriteSegmentWriteError(r.Context(), w, err, id, tenantID, "unlock before editing")
		return
	}

	// Always emit `updated_tm_ids` (empty array if no TMs received the
	// write) so the FE can invalidate exactly the TM caches that received
	// this entry, without null-checks.
	httputil.JSON(w, http.StatusOK, taskedit.WithReopened(map[string]any{
		"data":           res.Translation,
		"updated_tm_ids": res.UpdatedTMIDs,
	}, res.TaskReopened))
}

// WriteSegmentWriteError maps a Service error onto the REST response: the
// task edit guard's refusals with their own status and code, 404 not found,
// 409 locked / stale version, 422 QA block, 403 not in the first step, and
// anything else through httputil.ServiceError: a 500 whose cause reaches the
// request's log line. lockedHint completes the locked-segment message
// ("unlock before editing").
func (h *Handler) WriteSegmentWriteError(ctx context.Context, w http.ResponseWriter, err error, id, tenantID int64, lockedHint string) {
	var qaBlock *QABlockedError
	switch {
	case httputil.WriteCodedError(w, err):
	case errors.Is(err, pgx.ErrNoRows):
		httputil.NotFound(w, "translation")
	case errors.Is(err, ErrLocked):
		httputil.Error(w, http.StatusConflict, "segment is locked — "+lockedHint)
	case errors.Is(err, ErrConflict):
		h.writeConflictResponse(ctx, w, id, tenantID)
	case errors.Is(err, ErrNotFirstStep):
		httputil.Error(w, http.StatusForbidden, ErrNotFirstStep.Error())
	case errors.As(err, &qaBlock):
		writeQABlocked(w, qaBlock.Issues)
	default:
		httputil.ServiceError(w, err, "save", "translation")
	}
}

// saveDraftRequest is a draft save. Suggestion is the insert token of the
// Resources-panel TM match or MT suggestion the draft inserts.
type saveDraftRequest struct {
	updateHumanRequest
	Suggestion string `json:"suggestion,omitempty"`
}

// HandleSaveDraft saves translation text without promoting to 'approved'.
// Used by the auto-save feature (debounced, fires every 3s while editing) and
// by an insert from the Resources panel, which carries the insert token.
func (h *Handler) HandleSaveDraft(w http.ResponseWriter, r *http.Request) {
	existing, fileID, ok := h.routeTranslationInFile(w, r)
	if !ok {
		return
	}
	id := existing.ID

	var req saveDraftRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.HumanText == "" {
		httputil.BadRequest(w, "human_text is required")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if existing.Locked {
		httputil.Error(w, http.StatusConflict, "segment is locked — unlock before editing")
		return
	}
	userID := auth.UserIDFromContext(r.Context())
	// Before the task guard: a refused draft must not reopen a completed task.
	if err := CheckHeld(r.Context(), h.queries, tenantID, id, userID); err != nil {
		h.WriteSegmentWriteError(r.Context(), w, err, id, tenantID, "unlock before editing")
		return
	}

	var claims *suggestion.Claims
	if req.Suggestion != "" {
		c, err := h.VerifySuggestion(r.Context(), tenantID, fileID, existing, req.Suggestion)
		if errors.Is(err, ErrBadSuggestion) {
			httputil.BadRequest(w, err.Error())
			return
		}
		if err != nil {
			logging.Error(r.Context(), err, "verify inserted suggestion failed", "draft-suggestion-verify-failed", logging.TranslationID(id))
			httputil.ServerError(w, "save", "translation")
			return
		}
		claims = &c
	}

	task, ok := h.guard.EnsureHTTP(w, r, taskedit.Ref{TranslationID: id}, taskedit.Opts{})
	if !ok {
		return
	}

	t, err := h.SaveDraft(r.Context(), DraftParams{
		ID:                id,
		UserID:            userID,
		HumanText:         req.HumanText,
		HumanRichText:     req.HumanRichText,
		EditingTimeMs:     req.EditingTimeMs,
		ExpectedUpdatedAt: expectedUpdatedAtArg(req.ExpectedUpdatedAt),
		Suggestion:        claims,
		StepID:            activeStep(task),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ExplainNoRows(r.Context(), h.queries, tenantID, id, userID, req.ExpectedUpdatedAt != nil)
	}
	if err != nil {
		if errors.Is(err, ErrSuggestionGone) {
			httputil.ErrorCode(w, http.StatusConflict, CodeSuggestionGone, err.Error())
			return
		}
		h.WriteSegmentWriteError(r.Context(), w, err, id, tenantID, "unlock before editing")
		return
	}

	h.emitUnapproval(r.Context(), id, existing.Status, userID, activeStep(task))

	httputil.JSON(w, http.StatusOK, taskedit.WithReopened(map[string]any{"data": t}, task.Reopened))
}

// HandleLock locks and approves one segment. Managers only (route gate). A
// segment another user holds is refused (409 segment_held) unless the caller
// is an owner or admin. The tenant- and file-scoped lookup is the tenant guard.
func (h *Handler) HandleLock(w http.ResponseWriter, r *http.Request) {
	t, ok := h.routeTranslation(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	fileID, _, _ := routeIDs(w, r) // already validated by routeTranslation
	err := h.LockOne(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), t, fileID, ConfirmedViaUser)
	switch {
	case httputil.WriteCodedError(w, err):
		return
	case errors.Is(err, ErrLockEmpty):
		httputil.Error(w, http.StatusConflict, ErrLockEmpty.Error())
		return
	case errors.Is(err, ErrConflict):
		httputil.Error(w, http.StatusConflict, "segment changed, reload and try again")
		return
	case err != nil:
		logging.Error(ctx, err, "lock failed", "translation-lock-failed", logging.TranslationID(t.ID))
		httputil.ServerError(w, "lock", "translation")
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]string{"status": "locked"})
}

// HandleUnlock unlocks one segment so it can be edited; the status stays.
// Any member who may edit the file. Tenant guard as in HandleLock.
func (h *Handler) HandleUnlock(w http.ResponseWriter, r *http.Request) {
	t, ok := h.routeTranslation(w, r)
	if !ok {
		return
	}
	if err := h.queries.UnlockTranslation(r.Context(), t.ID); err != nil {
		httputil.ServerError(w, "unlock", "translation")
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]string{"status": "unlocked"})
}

// HandleClear reverts a single translation to untranslated.
// Tenant-ownership check runs FIRST so cross-tenant probes can't
// distinguish "locked" from "doesn't exist".
func (h *Handler) HandleClear(w http.ResponseWriter, r *http.Request) {
	fileID, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}
	id, err := httputil.ParseID(r, "translationID")
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	res, err := h.Clear(r.Context(), tenantID, auth.UserIDFromContext(r.Context()), fileID, id)
	if err != nil {
		h.WriteSegmentWriteError(r.Context(), w, err, id, tenantID, "unlock before clearing")
		return
	}
	httputil.JSON(w, http.StatusOK, taskedit.WithReopened(map[string]any{"data": res.Translation}, res.TaskReopened))
}
