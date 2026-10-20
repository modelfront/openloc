package translationmemory

import (
	"fmt"
	"net/http"
	"strconv"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
	"openloc/internal/suggestion"
)

const (
	matchesPerTMCandidateLimit = 30 // stage-1 candidates per TM before rescore
	maxMatchesInResponse       = 10
)

// Concordance + per-job TM matches.

// HandleConcordanceSearch searches all TMs assigned to a project (or template) for substring matches.
// Accepts project_id (preferred) or template_id (backwards compat).
// Both resolve to a settings_profile_id and use the unified SettingsConcordanceSearch query.
func (h *Handler) HandleConcordanceSearch(w http.ResponseWriter, r *http.Request) {
	projectIDStr := r.URL.Query().Get("project_id")
	templateIDStr := r.URL.Query().Get("template_id")

	srcLang := langcode.Normalize(r.URL.Query().Get("source_language"))
	tgtLang := langcode.Normalize(r.URL.Query().Get("target_language"))
	query := r.URL.Query().Get("q")
	if query == "" {
		httputil.BadRequest(w, "q (search query) is required")
		return
	}

	limit := int32(20)
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 32); err == nil && n > 0 && n <= 100 {
			limit = int32(n)
		}
	}

	// Resolve settings profile ID from either project or template
	var settingsProfileID int64
	if projectIDStr != "" {
		projectID, err := strconv.ParseInt(projectIDStr, 10, 64)
		if err != nil || projectID == 0 {
			httputil.BadRequest(w, "invalid project_id")
			return
		}
		tenantID := auth.TenantIDFromContext(r.Context())
		project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
			ID: projectID, TenantID: tenantID,
		})
		if err != nil {
			httputil.NotFound(w, "project")
			return
		}
		settingsProfileID = project.SettingsProfileID
	} else if templateIDStr != "" {
		templateID, err := strconv.ParseInt(templateIDStr, 10, 64)
		if err != nil || templateID == 0 {
			httputil.BadRequest(w, "invalid template_id")
			return
		}
		tenantID := auth.TenantIDFromContext(r.Context())
		tmpl, err := h.queries.GetTemplate(r.Context(), db.GetTemplateParams{
			ID: templateID, TenantID: tenantID,
		})
		if err != nil {
			httputil.NotFound(w, "template")
			return
		}
		settingsProfileID = tmpl.SettingsProfileID
	} else {
		httputil.BadRequest(w, "project_id or template_id is required")
		return
	}

	// Search TM and terminology in parallel
	type tmResult struct {
		data []db.SettingsConcordanceSearchRow
		err  error
	}
	type termResult struct {
		data []db.SettingsTermConcordanceSearchRow
		err  error
	}

	tmCh := make(chan tmResult, 1)
	termCh := make(chan termResult, 1)

	// Both concordance queries run in parallel goroutines, with `recover()`
	// guarding against a panic deadlocking the receiving handler. Without the
	// recover, a panic in either goroutine never sends on its channel and the
	// `<-tmCh` / `<-termCh` blocks until the request context times out.
	// Same shape as the notification-send
	// recover in api.go.
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logging.Error(r.Context(), fmt.Errorf("%v", rec), "concordance TM goroutine panicked", "concordance-tm-panic")
				// The channel send below turns this into a 500, which the HTTP
				// middleware reports — but as HTTPServerError with no cause and
				// no stack. Reporting here is what names the panicking line.
				errtrack.CapturePanic(r.Context(), rec, "translationmemory", "concordance-tm")
				tmCh <- tmResult{err: fmt.Errorf("concordance tm panic: %v", rec)}
			}
		}()
		results, err := h.queries.SettingsConcordanceSearch(r.Context(), db.SettingsConcordanceSearchParams{
			SettingsProfileID: settingsProfileID,
			SourceLanguage:    srcLang,
			TargetLanguage:    tgtLang,
			Query:             &query,
			EntryLimit:        limit,
		})
		tmCh <- tmResult{data: results, err: err}
	}()

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logging.Error(r.Context(), fmt.Errorf("%v", rec), "concordance term goroutine panicked", "concordance-term-panic")
				errtrack.CapturePanic(r.Context(), rec, "translationmemory", "concordance-term")
				termCh <- termResult{err: fmt.Errorf("concordance term panic: %v", rec)}
			}
		}()
		results, err := h.queries.SettingsTermConcordanceSearch(r.Context(), db.SettingsTermConcordanceSearchParams{
			SettingsProfileID: settingsProfileID,
			SourceLanguage:    srcLang,
			TargetLanguage:    tgtLang,
			Query:             &query,
			EntryLimit:        limit,
		})
		termCh <- termResult{data: results, err: err}
	}()

	tm := <-tmCh
	term := <-termCh

	if tm.err != nil {
		httputil.ServerError(w, "run", "concordance search")
		return
	}

	response := map[string]any{
		"tm_matches": tm.data,
	}
	if term.err == nil {
		response["term_matches"] = term.data
	} else {
		response["term_matches"] = []any{}
	}
	httputil.JSON(w, http.StatusOK, response)
}

// HandleTMMatches returns fuzzy TM matches for a given source text, using all TMs assigned
// to the job's project. Used by the editor sidebar for real-time TM lookup.
// GET /api/jobs/{jobID}/tm-matches?source_text=...[&translation_id=...]
func (h *Handler) HandleTMMatches(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	sourceText := r.URL.Query().Get("source_text")
	if sourceText == "" {
		httputil.BadRequest(w, "source_text is required")
		return
	}
	// translation_id (optional) names the segment, for in-context matches.
	var translationID int64
	if tid, err := strconv.ParseInt(r.URL.Query().Get("translation_id"), 10, 64); err == nil {
		translationID = tid
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	matches, err := h.JobTMMatches(r.Context(), tenantID, jobID, sourceText, translationID)
	if httputil.ServiceError(w, err, "look up", "TM matches") {
		return
	}
	out := make([]panelTMMatch, len(matches))
	for i, m := range matches {
		out[i] = panelTMMatch{JobTMMatch: m}
		if h.suggestions == nil {
			continue
		}
		token, err := h.suggestions.Sign(tenantID, suggestion.Claims{
			Kind: suggestion.KindTM, JobID: jobID, Source: suggestion.SourceHash(sourceText), TranslationID: translationID,
			TMID: m.TMID, TMEntryID: m.TMEntryID, Percent: m.Percent,
		})
		if err != nil {
			httputil.ServerError(w, "sign", "TM matches")
			return
		}
		out[i].InsertToken = token
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"matches": out})
}

// panelTMMatch is a TM match as the Resources panel gets it: with the token
// that lets inserting it record the match on the segment.
type panelTMMatch struct {
	JobTMMatch
	InsertToken string `json:"insert_token,omitempty"`
}
