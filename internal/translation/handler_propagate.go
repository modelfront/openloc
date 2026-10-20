package translation

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/pipeline"
	"openloc/internal/taskedit"
)

// HandlePropagate auto-propagates a translation to all unlocked segments with
// the same source text. A sibling another user holds is skipped and counted
// in `held`.
//
// Tenant isolation: GetFileTenant is the guard. Without it the underlying
// PropagateTranslation query is scoped only by file_id, which is sequential
// and guessable — any authenticated tenant member could overwrite another
// tenant's translations en masse.
func (h *Handler) HandlePropagate(w http.ResponseWriter, r *http.Request) {
	fileID, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetFileTenant(r.Context(), db.GetFileTenantParams{
		ID: fileID, TenantID: tenantID,
	}); err != nil {
		if httputil.DBError(w, err, "file") {
			return
		}
		httputil.ServerError(w, "verify", "file ownership")
		return
	}

	var req struct {
		SourceText     string `json:"source_text"`
		TargetLanguage string `json:"target_language"`
		HumanText      string `json:"human_text"`
		// XLIFF rich-text form (with inline tag chips) — propagated alongside
		// human_text so identical-source siblings render the same chips
		// without a re-edit. Optional; legacy clients omit it.
		HumanRichText *string `json:"human_rich_text,omitempty"`
		ExcludeID     int64   `json:"exclude_id"` // the translation that was just saved
	}
	if err := httputil.Decode(r, &req); err != nil || req.SourceText == "" || req.HumanText == "" {
		httputil.BadRequest(w, "source_text, target_language, human_text are required")
		return
	}

	// The siblings are this file's rows in this language: that job's current
	// task must take edits. Keyed on the verified file, not on exclude_id,
	// which the client may leave at 0.
	lang := langcode.Normalize(req.TargetLanguage)
	if !auth.LinguistFileLanguageAccess(w, r, h.queries, fileID, lang) {
		return
	}
	reopened := false
	var jobIDs []int64
	job, err := h.queries.GetJobByFileAndLang(r.Context(), db.GetJobByFileAndLangParams{
		FileID: fileID, TargetLanguage: lang,
	})
	switch {
	case err == nil:
		task, ok := h.guard.EnsureHTTP(w, r, taskedit.Ref{JobID: job.ID}, taskedit.Opts{})
		if !ok {
			return
		}
		reopened = task.Reopened
		jobIDs = []int64{job.ID}
	case !errors.Is(err, pgx.ErrNoRows):
		httputil.ServerError(w, "resolve", "job")
		return
	}

	propagatedBy := auth.UserIDFromContext(r.Context())
	count, err := h.queries.CountPropagationTargets(r.Context(), db.CountPropagationTargetsParams{
		ActorID:        &propagatedBy,
		FileID:         fileID,
		SourceText:     req.SourceText,
		TargetLanguage: lang,
		ExcludeID:      req.ExcludeID,
	})
	if err != nil {
		httputil.ServerError(w, "count", "propagation targets")
		return
	}

	if count.Targets == 0 {
		httputil.JSON(w, http.StatusOK, taskedit.WithReopened(map[string]any{
			"data": map[string]any{"propagated": 0, "held": count.Held, "siblings": []db.Translation{}},
		}, reopened))
		return
	}

	// RETURNING * gives each affected row so the frontend can patch its
	// React Query cache for the siblings synchronously; an async refetch
	// leaves a window where a re-activated sibling starts from a stale
	// baseline and its next save gets a false 409.
	var siblings []db.Translation
	err = database.InJobsTx(r.Context(), h.pool, tenantID, jobIDs, func(_ pgx.Tx, q db.Querier) error {
		var err error
		siblings, err = q.PropagateTranslation(r.Context(), db.PropagateTranslationParams{
			FileID:         fileID,
			SourceText:     req.SourceText,
			TargetLanguage: lang,
			HumanText:      &req.HumanText,
			HumanRichText:  req.HumanRichText,
			ActorID:        &propagatedBy,
			ExcludeID:      req.ExcludeID,
		})
		return err
	})
	if err != nil {
		httputil.ServerError(w, "propagate", "translation")
		return
	}

	// Each sibling's target was just replaced, so its stored QA findings
	// describe text that is gone — ignores included, since the ignore is
	// fingerprinted on a message derived from that text. Confirm re-runs QA
	// only for the segment being confirmed, so nothing else corrects the
	// siblings.
	if len(siblings) > 0 {
		siblingIDs := make([]int64, len(siblings))
		for i, s := range siblings {
			siblingIDs[i] = s.ID
		}
		if err := h.queries.DeleteQAResultsForTranslations(r.Context(), siblingIDs); err != nil {
			httputil.ServerError(w, "propagate", "QA findings")
			return
		}
	}

	// Emit `propagation` history per affected sibling. The
	// sibling rows are propagation targets — the user who confirmed the
	// originating segment owns the propagation event. Does NOT touch
	// translations.origin (sticky through propagation, like human edits).
	for _, sibling := range siblings {
		sibText := req.HumanText
		details := map[string]any{
			"source_translation_id": req.ExcludeID,
		}
		pipeline.EmitHumanHistory(r.Context(), h.queries, pipeline.HumanEmitOpts{
			TranslationID: sibling.ID,
			Kind:          db.TranslationHistoryKindPropagation,
			Text:          &sibText,
			RichText:      req.HumanRichText,
			CreatedBy:     &propagatedBy,
			Details:       details,
		})
	}

	httputil.JSON(w, http.StatusOK, taskedit.WithReopened(map[string]any{
		"data": map[string]any{"propagated": len(siblings), "held": count.Held, "siblings": siblings},
	}, reopened))
}
