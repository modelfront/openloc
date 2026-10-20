package spellcheck

import (
	"net/http"
	"strings"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
)

// Per-project spell check dictionary — what "Ignore all" writes to.

type dictionaryWordRequest struct {
	Language string `json:"language"`
	Word     string `json:"word"`
}

// HandleListDictionary lists dictionary words for a project + language.
// GET /api/projects/{projectID}/spellcheck-dictionary?language=de
func (h *Handler) HandleListDictionary(w http.ResponseWriter, r *http.Request) {
	projectID, tenantID, ok := h.projectScope(w, r)
	if !ok {
		return
	}

	language := langcode.Normalize(r.URL.Query().Get("language"))
	if language == "" {
		httputil.BadRequest(w, "language is required")
		return
	}

	entries, err := h.queries.ListSpellcheckDictionary(r.Context(), db.ListSpellcheckDictionaryParams{
		ProjectID: projectID, TenantID: tenantID, Language: language,
	})
	if err != nil {
		httputil.ServerError(w, "load", "dictionary")
		return
	}
	if entries == nil {
		entries = []db.SpellcheckDictionary{}
	}
	httputil.Data(w, entries)
}

// HandleAddDictionaryWord adds a word. Managers only (owner/admin/PM), via the
// /api/projects group's RequireManager.
// POST /api/projects/{projectID}/spellcheck-dictionary { language, word }
func (h *Handler) HandleAddDictionaryWord(w http.ResponseWriter, r *http.Request) {
	projectID, tenantID, ok := h.projectScope(w, r)
	if !ok {
		return
	}

	var req dictionaryWordRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	// Only surrounding whitespace is trimmed — the word's own casing is
	// preserved because lookups are case-sensitive on purpose.
	word := strings.TrimSpace(req.Word)
	language := langcode.Normalize(req.Language)
	if word == "" || language == "" {
		httputil.BadRequest(w, "language and word are required")
		return
	}
	if strings.ContainsAny(word, " \t\n") {
		httputil.BadRequest(w, "word must be a single token")
		return
	}

	userID := auth.UserIDFromContext(r.Context())
	entry, err := h.queries.AddSpellcheckDictionaryWord(r.Context(), db.AddSpellcheckDictionaryWordParams{
		TenantID:  tenantID,
		ProjectID: projectID,
		Language:  language,
		Word:      word,
		CreatedBy: &userID,
	})
	if err != nil {
		httputil.ServerError(w, "add", "dictionary word")
		return
	}
	httputil.Created(w, entry)
}

// HandleDeleteDictionaryWord removes a word. Gated to owner/admin at the route:
// un-ignoring re-opens findings for everyone on the project, which is a config
// change rather than an editing decision.
// DELETE /api/projects/{projectID}/spellcheck-dictionary/{wordID}
func (h *Handler) HandleDeleteDictionaryWord(w http.ResponseWriter, r *http.Request) {
	projectID, tenantID, ok := h.projectScope(w, r)
	if !ok {
		return
	}
	wordID, err := httputil.ParseID(r, "wordID")
	if err != nil {
		httputil.BadRequest(w, "invalid word ID")
		return
	}

	n, err := h.queries.SoftDeleteSpellcheckDictionaryWord(r.Context(), db.SoftDeleteSpellcheckDictionaryWordParams{
		ID: wordID, TenantID: tenantID, ProjectID: projectID,
	})
	if httputil.ServiceError(w, err, "delete", "dictionary word") {
		return
	}
	if n == 0 {
		httputil.NotFound(w, "dictionary word")
		return
	}
	httputil.NoContent(w)
}

// projectScope parses the project id and verifies it belongs to the caller's
// tenant. Returns false having already written the response on failure.
func (h *Handler) projectScope(w http.ResponseWriter, r *http.Request) (projectID, tenantID int64, ok bool) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return 0, 0, false
	}
	tenantID = auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
		ID: projectID, TenantID: tenantID,
	}); err != nil {
		httputil.NotFound(w, "project")
		return 0, 0, false
	}
	return projectID, tenantID, true
}
