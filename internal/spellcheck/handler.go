package spellcheck

import (
	"context"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
)

// Handler serves spell check for the CAT editor: a single-segment endpoint that
// backs the live underlines, and a job-scoped endpoint that backs the review
// dialog.
type Handler struct {
	queries db.Querier
	client  *Client
}

func NewHandler(q db.Querier, client *Client) *Handler {
	return &Handler{queries: q, client: client}
}

type checkRequest struct {
	Text     string `json:"text"`
	Language string `json:"language"`
	// Optional. When set, the job's project dictionary filters the matches so
	// the live underlines agree with the review dialog. Verified against the
	// caller's tenant, and for linguists against their job assignments, before
	// it's used for anything — same gate as the /api/jobs/{jobID} routes.
	JobID *int64 `json:"job_id"`
}

// HandleCheck checks a single text — the live-underline path as the user types.
// POST /api/spellcheck { text: "...", language: "de", job_id?: 12 } → { matches: [...] }
func (h *Handler) HandleCheck(w http.ResponseWriter, r *http.Request) {
	var req checkRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Text == "" || req.Language == "" {
		httputil.BadRequest(w, "text and language are required")
		return
	}

	dictionary := map[string]bool{}
	if req.JobID != nil {
		tenantID := auth.TenantIDFromContext(r.Context())
		job, err := h.queries.GetJobTenant(r.Context(), db.GetJobTenantParams{ID: *req.JobID, TenantID: tenantID})
		if err != nil {
			httputil.NotFound(w, "job")
			return
		}
		if !auth.LinguistJobAccess(w, r, h.queries, job.ID) {
			return
		}
		file, err := h.queries.GetFileTenant(r.Context(), db.GetFileTenantParams{ID: job.FileID, TenantID: tenantID})
		if err != nil {
			httputil.NotFound(w, "file")
			return
		}
		dictionary, err = h.loadDictionary(r.Context(), file.ProjectID, tenantID, langcode.Normalize(job.TargetLanguage))
		if err != nil {
			httputil.ServerError(w, "load", "dictionary")
			return
		}
	}

	matches, err := h.client.Check(r.Context(), req.Text, req.Language)
	if err != nil {
		// Log the detail, hand the client a generic failure — the underlying
		// error can carry the request text back from LanguageTool.
		logging.Error(r.Context(), err, "spell check failed", "spellcheck-failed")
		httputil.Error(w, http.StatusBadGateway, "spell check service unavailable")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]any{"matches": filterDictionary(req.Text, matches, dictionary)})
}

// loadDictionary returns the project's ignored words for one target language
// as a set, keyed exactly as stored (case-sensitive, see filterDictionary).
func (h *Handler) loadDictionary(ctx context.Context, projectID, tenantID int64, language string) (map[string]bool, error) {
	words, err := h.queries.ListSpellcheckDictionaryWords(ctx, db.ListSpellcheckDictionaryWordsParams{
		ProjectID: projectID, TenantID: tenantID, Language: language,
	})
	if err != nil {
		return nil, err
	}
	dictionary := make(map[string]bool, len(words))
	for _, word := range words {
		dictionary[word] = true
	}
	return dictionary, nil
}
