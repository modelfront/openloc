package translationmemory

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/suggestion"
)

type Handler struct {
	*Service
	scorer Scorer // stage-2 rescorer for TM matches; default LevenshteinScorer{}
	// suggestions signs the insert token on each panel TM match. Nil: matches
	// carry no token, and inserting one records no TM event.
	suggestions *suggestion.Signer
}

// SetSuggestionSigner signs panel TM matches, so inserting one records a TM event.
func (h *Handler) SetSuggestionSigner(s *suggestion.Signer) { h.suggestions = s }

func NewHandler(q db.Querier, dbtx db.DBTX, pool *pgxpool.Pool) *Handler {
	return &Handler{Service: NewService(q, dbtx, pool), scorer: LevenshteinScorer{}}
}

// --- TM Entries ---
// (Collection CRUD lives in handler_collection.go.
//  TMX import/export + concordance live in their own *_import.go / *_concordance.go files.)

func (h *Handler) HandleListEntries(w http.ResponseWriter, r *http.Request) {
	tmID, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}

	// Verify TM belongs to tenant
	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetTMCollection(r.Context(), db.GetTMCollectionParams{
		ID:       tmID,
		TenantID: tenantID,
	}); httputil.DBError(w, err, "TM") {
		return
	}

	qs := r.URL.Query()
	f := EntryFilter{
		SourceLanguage: qs.Get("source_language"),
		TargetLanguage: qs.Get("target_language"),
		Query:          qs.Get("q"),
		Scope:          qs.Get("scope"),
		Exact:          qs.Get("exact") == "true",
		CaseSensitive:  qs.Get("case_sensitive") == "true",
		Regex:          qs.Get("regex") == "true",
	}
	if l := qs.Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 32); err == nil && n > 0 && n <= 500 {
			f.Limit = int32(n)
		}
	}
	if c := qs.Get("cursor"); c != "" {
		if n, err := strconv.ParseInt(c, 10, 64); err == nil && n >= 0 {
			f.Cursor = n
		}
	}

	page, err := h.ListEntries(r.Context(), tmID, f)
	var verr ValidationError
	if errors.As(err, &verr) {
		httputil.BadRequest(w, verr.Msg)
		return
	}
	if err != nil {
		httputil.ServerError(w, "list", "TM entries")
		return
	}
	if f.Query != "" {
		httputil.Paginated(w, page.Matches, page.NextCursor, page.HasMore, page.Total)
		return
	}
	httputil.Paginated(w, page.Entries, page.NextCursor, page.HasMore, page.Total)
}

func (h *Handler) HandleListLanguagePairs(w http.ResponseWriter, r *http.Request) {
	tmID, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetTMCollection(r.Context(), db.GetTMCollectionParams{
		ID:       tmID,
		TenantID: tenantID,
	}); httputil.DBError(w, err, "TM") {
		return
	}

	pairs, err := h.queries.ListTMLanguagePairs(r.Context(), tmID)
	if err != nil {
		httputil.ServerError(w, "list", "language pairs")
		return
	}
	httputil.Data(w, pairs)
}

// HandleDeleteEntry handles DELETE /api/tms/{tmID}/entries/{entryID}.
// DeleteTMEntry matches `WHERE id = $1 AND tm_id = $2` —
// a 0-rows-affected result means the entry doesn't belong to the URL's TM (either
// cross-tenant attack or same-tenant cross-TM URL tamper) → 404.
// RequireTMOwnership middleware has already validated that {tmID} belongs
// to the caller's tenant, so the tm_id filter alone is sufficient.
func (h *Handler) HandleDeleteEntry(w http.ResponseWriter, r *http.Request) {
	tmID, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}
	id, err := httputil.ParseID(r, "entryID")
	if err != nil {
		httputil.BadRequest(w, "invalid entry ID")
		return
	}

	rows, err := h.queries.DeleteTMEntry(r.Context(), db.DeleteTMEntryParams{
		ID:   id,
		TmID: tmID,
	})
	if err != nil {
		httputil.ServerError(w, "delete", "TM entry")
		return
	}
	if rows == 0 {
		httputil.Error(w, http.StatusNotFound, "TM entry not found")
		return
	}
	httputil.NoContent(w)
}

// HandleUpdateEntry handles PATCH /api/tms/{tmID}/entries/{entryID}.
// Inline edit from the TM Browser. Updates plain + rich text for both
// source and target, recomputes hashes, appends a snapshot to the history
// table BEFORE the update (atomic — both writes share a transaction).
//
// Optimistic locking (mirrors translations):
// the caller passes `expected_updated_at` (the row's `updated_at` they last
// saw). If the DB's current `updated_at` differs, the UPDATE matches zero
// rows; sqlc returns ErrNoRows; we re-fetch to disambiguate "another user
// edited the row" (→ 409 Conflict) from "the row was deleted" (→ 404).
//
// Tenant + TM scoping: every entry-touching query is gated by
// (entry.id, entry.tm_id, tenant_id). RequireTMOwnership has already
// validated that {tmID} belongs to the caller's tenant, but we also enforce
// that the entry actually lives in the URL's TM — cross-TM URL tampering
// returns 404 instead of letting the operation through. UpdateTMEntry's
// SQL also includes `AND tm_id = $tm_id` for defense-in-depth.
func (h *Handler) HandleUpdateEntry(w http.ResponseWriter, r *http.Request) {
	tmID, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}
	entryID, err := httputil.ParseID(r, "entryID")
	if err != nil {
		httputil.BadRequest(w, "invalid entry ID")
		return
	}

	var req struct {
		SourceText     string `json:"source_text"`
		TargetText     string `json:"target_text"`
		SourceRichText string `json:"source_rich_text"` // "" → NULL via NULLIF in SQL
		TargetRichText string `json:"target_rich_text"`
		// Omit (or null) to opt out of the version check (legacy / scripted callers only).
		ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
		// Free-form short label stored on the history row; the inline edit sends "edit".
		ChangeReason string `json:"change_reason"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	updated, err := h.UpdateEntry(r.Context(), tenantID, auth.UserIDFromContext(r.Context()), tmID, entryID, UpdateEntryInput{
		SourceText:        req.SourceText,
		TargetText:        req.TargetText,
		SourceRichText:    req.SourceRichText,
		TargetRichText:    req.TargetRichText,
		ExpectedUpdatedAt: req.ExpectedUpdatedAt,
		ChangeReason:      req.ChangeReason,
	})
	var verr ValidationError
	switch {
	case err == nil:
		httputil.Data(w, updated)
	case errors.As(err, &verr):
		httputil.BadRequest(w, verr.Msg)
	case errors.Is(err, ErrConflict):
		h.writeTMConflictResponse(r.Context(), w, entryID, tmID, tenantID)
	default:
		httputil.DBError(w, err, "TM entry")
	}
}

// writeTMConflictResponse emits a 409 with both the legacy `{error: ...}`
// shape and a `conflict` payload describing the TM entry's CURRENT
// server-side state, so the frontend can show both versions side by side
// without a second request. If the conflict-details query fails (extremely
// rare; the row was just verified to exist via GetTMEntryScoped), we fall
// back to the plain-error 409 so the client at least gets the legacy signal.
func (h *Handler) writeTMConflictResponse(ctx context.Context, w http.ResponseWriter, id, tmID, tenantID int64) {
	row, err := h.queries.GetTMEntryConflictDetails(ctx, db.GetTMEntryConflictDetailsParams{
		ID: id, TmID: tmID, TenantID: tenantID,
	})
	if err != nil {
		httputil.Error(w, http.StatusConflict, "TM entry was updated by another user — reload and try again")
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
			"message": "TM entry was updated by another user — reload and try again",
		},
		"conflict": map[string]any{
			"id":                 row.ID,
			"source_text":        row.SourceText,
			"target_text":        row.TargetText,
			"source_rich_text":   row.SourceRichText,
			"target_rich_text":   row.TargetRichText,
			"updated_at":         updatedAt,
			"updated_by":         row.UpdatedBy,
			"updated_by_user_id": row.UpdatedByUserID,
			"updated_by_name":    row.UpdatedByName,
		},
	})
}

// expectedUpdatedAtArg converts the optional *time.Time into the pgtype value
// sqlc expects. nil means "skip the version check" (legacy contract).
// Mirrors internal/translation/handler.go's helper of the same name — kept
// local to this package to avoid a cross-package import for one function.
func expectedUpdatedAtArg(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// HandleListEntryHistory handles GET /api/tms/{tmID}/entries/{entryID}/history.
// Returns the change history for a TM entry, newest first. Feeds the TM
// entry history dialog.
//
// Tenant + TM scoping: GetTMEntryScoped enforces both that the
// entry exists in the caller's tenant AND that it belongs to the URL's TM,
// closing the same cross-TM URL-tampering gap as HandleUpdateEntry.
func (h *Handler) HandleListEntryHistory(w http.ResponseWriter, r *http.Request) {
	tmID, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}
	entryID, err := httputil.ParseID(r, "entryID")
	if err != nil {
		httputil.BadRequest(w, "invalid entry ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetTMEntryScoped(r.Context(), db.GetTMEntryScopedParams{
		ID:       entryID,
		TmID:     tmID,
		TenantID: tenantID,
	}); httputil.DBError(w, err, "TM entry") {
		return
	}

	history, err := h.queries.ListTMEntryHistory(r.Context(), entryID)
	if err != nil {
		httputil.ServerError(w, "list", "TM entry history")
		return
	}
	httputil.Data(w, history)
}
