package translationmemory

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Bulk find-and-replace across TM entries.

// HandleTMFindReplace handles POST /api/tms/{tmID}/find-replace.
// Bulk find-and-replace across TM entries. Two modes:
//   - "find":        preview — one page of matching entries (after "cursor",
//     up to "limit") with computed replacements, "next_cursor" for the next
//     page, and the totals over the whole TM. No DB mutation.
//   - "replace_all": apply — updates every matching entry in one transaction
//     (history snapshot + update per chunk), so a partial failure rolls back
//     everything.
//
// Scope ("source" | "target" | "both", default "both") restricts which fields
// are searched. Optional source_language + target_language filters narrow to
// a specific pair (e.g., only en→es entries).
//
// Tenant + permission gating: route uses RequireTMOwnership + RequireRole
// ("owner", "admin"). Bulk mutations are admin-only — a member who can only
// read/inline-edit shouldn't be able to rewrite swaths of TM at once.
//
// Optimistic locking is intentionally NOT enforced for bulk: we don't want
// the entire batch to 409 because one row was edited concurrently. The
// trade-off is "last bulk wins"; the per-entry inline edit still has its
// own per-row optimistic lock for fine-grained safety.
func (h *Handler) HandleTMFindReplace(w http.ResponseWriter, r *http.Request) {
	tmID, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}

	var req struct {
		Find           string `json:"find"`
		Replace        string `json:"replace"`
		CaseSensitive  bool   `json:"case_sensitive"`
		Mode           string `json:"mode"`            // "find" | "replace_all"
		Scope          string `json:"scope"`           // "source" | "target" | "both"
		SourceLanguage string `json:"source_language"` // optional filter
		TargetLanguage string `json:"target_language"` // optional filter
		Cursor         int64  `json:"cursor"`          // find: entry id the page starts after
		Limit          int    `json:"limit"`           // find: page size (default 100, max 500)
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Find == "" {
		httputil.BadRequest(w, "find is required")
		return
	}
	if req.Mode == "" {
		req.Mode = "find"
	}
	if req.Mode != "find" && req.Mode != "replace_all" {
		httputil.BadRequest(w, "mode must be 'find' or 'replace_all'")
		return
	}
	if req.Scope == "" {
		req.Scope = "both"
	}
	if req.Scope != "source" && req.Scope != "target" && req.Scope != "both" {
		httputil.BadRequest(w, "scope must be 'source', 'target', or 'both'")
		return
	}

	// Verify the TM exists in the caller's tenant. RequireTMOwnership has
	// already done this for the route, but defense-in-depth: this also pins
	// the tenant for the in-flight handler logic.
	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetTMCollection(r.Context(), db.GetTMCollectionParams{
		ID: tmID, TenantID: tenantID,
	}); httputil.DBError(w, err, "TM") {
		return
	}

	fq := FindReplaceQuery{
		TMID: tmID, Find: req.Find, Replace: req.Replace, CaseSensitive: req.CaseSensitive,
		Scope: req.Scope, SourceLanguage: req.SourceLanguage, TargetLanguage: req.TargetLanguage,
	}
	if req.Mode == "find" {
		preview, err := h.PreviewFindReplace(r.Context(), fq, req.Cursor, req.Limit)
		if err != nil {
			logging.Error(r.Context(), err, "TM find-replace preview failed", "tm-find-replace-preview-failed", logging.TMID(tmID))
			httputil.ServerError(w, "search", "TM entries")
			return
		}
		httputil.Data(w, preview)
		return
	}

	userID := auth.UserIDFromContext(r.Context())
	user, err := h.queries.GetUser(r.Context(), userID)
	if err != nil {
		httputil.ServerError(w, "load", "user")
		return
	}
	updatedByLabel := user.Email
	if updatedByLabel == "" {
		updatedByLabel = user.Name
	}
	updated, replaced, err := h.ApplyFindReplace(r.Context(), fq, userID, updatedByLabel)
	if err != nil {
		logging.Error(r.Context(), err, "TM find-replace failed", "tm-find-replace-failed", logging.TMID(tmID))
		httputil.ServerError(w, "update", "TM entries")
		return
	}
	httputil.Data(w, map[string]any{
		"entries_updated":    updated,
		"total_replacements": replaced,
	})
}
