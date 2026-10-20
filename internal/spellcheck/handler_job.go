package spellcheck

import (
	"net/http"
	"unicode/utf16"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
	"openloc/internal/timing"
)

// maxTranslationsPerRequest bounds one chunk of a document-wide check. The
// client walks a job in chunks so it can render progress and let the user
// cancel; this is the server-side guard against a caller asking for a
// 20,000-segment file in a single request.
const maxTranslationsPerRequest = 200

type jobCheckRequest struct {
	TranslationIDs []int64 `json:"translation_ids"`
	// IncludeLocked checks locked segments too — the "All segments (includes
	// locked)" scope. Off by default, so every other scope skips them.
	IncludeLocked bool `json:"include_locked"`
}

type segmentMatches struct {
	TranslationID int64   `json:"translation_id"`
	Matches       []Match `json:"matches"`
}

// HandleCheckJob checks a caller-supplied set of a job's translations.
// POST /api/jobs/{jobID}/spellcheck { translation_ids: [...] }
//
// The scope (non-locked / all segments / current filter / selection) is resolved on
// the client, which already holds the translation list and the active filter —
// so this endpoint takes an explicit id list rather than duplicating the filter
// predicates server-side.
func (h *Handler) HandleCheckJob(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}

	var req jobCheckRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if len(req.TranslationIDs) == 0 {
		httputil.BadRequest(w, "translation_ids is required")
		return
	}
	if len(req.TranslationIDs) > maxTranslationsPerRequest {
		httputil.BadRequest(w, "too many translation_ids in one request")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	job, err := h.queries.GetJobTenant(r.Context(), db.GetJobTenantParams{
		ID: jobID, TenantID: tenantID,
	})
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}

	// The file gives us the project, which scopes the dictionary.
	file, err := h.queries.GetFileTenant(r.Context(), db.GetFileTenantParams{
		ID: job.FileID, TenantID: tenantID,
	})
	if err != nil {
		httputil.NotFound(w, "file")
		return
	}

	language := langcode.Normalize(job.TargetLanguage)

	// Only the requested rows of the job; ids outside it are not returned.
	rows, err := h.queries.ListJobTranslationsByIDs(r.Context(), db.ListJobTranslationsByIDsParams{
		FileID:         job.FileID,
		TargetLanguage: job.TargetLanguage,
		Ids:            req.TranslationIDs,
	})
	if err != nil {
		httputil.ServerError(w, "load", "translations")
		return
	}
	byRowID := make(map[int64]db.ListJobTranslationsByIDsRow, len(rows))
	for _, t := range rows {
		byRowID[t.ID] = t
	}

	// Results follow the request order. Locked segments are skipped unless the
	// caller asks for them.
	var items []BatchItem
	seen := make(map[int64]bool, len(req.TranslationIDs))
	for _, id := range req.TranslationIDs {
		t, ok := byRowID[id]
		if !ok || seen[id] || (t.Locked && !req.IncludeLocked) {
			continue
		}
		seen[id] = true
		if t.ActiveText == nil || *t.ActiveText == "" {
			continue
		}
		items = append(items, BatchItem{ID: t.ID, Text: *t.ActiveText})
	}

	if len(items) == 0 {
		httputil.Data(w, map[string]any{"results": []segmentMatches{}, "failed": 0})
		return
	}

	dictionary, err := h.loadDictionary(r.Context(), file.ProjectID, tenantID, language)
	if err != nil {
		httputil.ServerError(w, "load", "dictionary")
		return
	}

	byID := make(map[int64]string, len(items))
	for _, item := range items {
		byID[item.ID] = item.Text
	}

	// The LanguageTool round trip only. The client walks a
	// document chunk by chunk (SPELLCHECK_CHUNK_SIZE in web/src/lib/spellcheck-run.ts),
	// so n ends up as chunks requested and the aggregate answers "how much of a
	// document-wide check is LanguageTool". Only the
	// job-scoped check is tracked — the live per-segment underline check
	// (HandleCheck) has no job to attribute to and fires on every keystroke.
	ctx, rec := timing.WithRecorder(r.Context())
	checkCtx, doneCheck := timing.Start(ctx, timing.StageQASpellcheck)
	batchResults := h.client.CheckBatch(checkCtx, items, language)
	doneCheck(nil)
	defer rec.Flush(ctx, h.queries, tenantID, jobID)

	results := make([]segmentMatches, 0, len(items))
	failed := 0
	for _, res := range batchResults {
		if res.Err != nil {
			failed++
			logging.Warn(ctx, res.Err, "spell check failed for a translation", "spellcheck-translation-failed", logging.JobID(jobID), logging.TranslationID(res.ID))
			continue
		}
		kept := filterDictionary(byID[res.ID], res.Matches, dictionary)
		if len(kept) == 0 {
			continue
		}
		results = append(results, segmentMatches{TranslationID: res.ID, Matches: kept})
	}

	httputil.Data(w, map[string]any{
		"results": results,
		"failed":  failed,
	})
}

// filterDictionary drops matches whose flagged text is in the project
// dictionary. Comparison is case-sensitive by design — see the migration
// comment: in German `adresse` vs `Adresse` is the error itself.
func filterDictionary(text string, matches []Match, dictionary map[string]bool) []Match {
	if len(dictionary) == 0 {
		return matches
	}
	kept := make([]Match, 0, len(matches))
	for _, m := range matches {
		word, ok := flaggedText(text, m.Offset, m.Length)
		if ok && dictionary[word] {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

// flaggedText extracts the substring a match points at.
//
// LanguageTool reports offsets in UTF-16 code units (it's a Java service), so
// they cannot index a Go string's bytes. Slicing `text[offset:offset+length]`
// directly is correct only for pure-ASCII text and silently wrong for every
// target language with accents or umlauts — which is most of them. Encoding to
// UTF-16 first is what makes the dictionary match "Adresse" in "die Adresse
// enthält…" rather than a byte-shifted fragment.
func flaggedText(text string, offset, length int) (string, bool) {
	if offset < 0 || length <= 0 {
		return "", false
	}
	units := utf16.Encode([]rune(text))
	if offset+length > len(units) {
		return "", false
	}
	return string(utf16.Decode(units[offset : offset+length])), true
}
