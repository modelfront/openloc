package translation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/editor"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
	"openloc/internal/taskedit"
)

type propagateRequest struct {
	SourceText     string `json:"source_text"`
	TargetLanguage string `json:"target_language"`
	HumanText      string `json:"human_text"`
	// XLIFF rich-text form (with inline tag chips) — propagated alongside
	// human_text so identical-source siblings render the same chips
	// without a re-edit. Optional; legacy clients omit it.
	HumanRichText *string `json:"human_rich_text,omitempty"`
	ExcludeID     int64   `json:"exclude_id"` // the translation that was just saved
}

func decodePropagate(w http.ResponseWriter, r *http.Request) (propagateRequest, bool) {
	var req propagateRequest
	if err := httputil.Decode(r, &req); err != nil || req.SourceText == "" || req.HumanText == "" {
		httputil.BadRequest(w, "source_text, target_language, human_text are required")
		return req, false
	}
	return req, true
}

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

	req, ok := decodePropagate(w, r)
	if !ok {
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

	res, err := h.propagate(r.Context(), tenantID, req, lang, []int64{fileID}, jobIDs)
	if httputil.ServiceError(w, err, "propagate", "translation") {
		return
	}
	httputil.JSON(w, http.StatusOK, taskedit.WithReopened(map[string]any{"data": res}, reopened))
}

// HandlePropagateScope answers POST
// /api/projects/{projectID}/editor/propagate?jobs=: HandlePropagate across
// every file the editor has open. exclude_id names the confirmed translation,
// which must be in the scope; its job's task must take the edit, as in
// HandlePropagate. Every other file takes the siblings only when its task is
// editable without a reopen: those it refuses are left alone and their
// siblings counted in `not_editable`.
func (h *Handler) HandlePropagateScope(w http.ResponseWriter, r *http.Request) {
	scope, ok := editor.RequestScope(w, r, h.queries)
	if !ok {
		return
	}
	req, ok := decodePropagate(w, r)
	if !ok {
		return
	}
	lang := langcode.Normalize(req.TargetLanguage)
	if lang != scope.TargetLanguage || req.ExcludeID <= 0 {
		httputil.BadRequest(w, "exclude_id and the scope's target_language are required")
		return
	}
	ctx := r.Context()
	// The scope check comes between the decision and its write: a confirmed
	// translation outside the scope must not reopen its job's task.
	decision, ok := h.guard.CheckHTTP(w, r, taskedit.Ref{TranslationID: req.ExcludeID}, taskedit.Opts{})
	if !ok {
		return
	}
	if !slices.Contains(scope.JobIDs(), decision.JobID()) {
		httputil.NotFound(w, "translation")
		return
	}
	origin, ok := h.guard.ApplyHTTP(w, r, decision)
	if !ok {
		return
	}
	var others []int64
	for _, id := range scope.JobIDs() {
		if id != origin.JobID {
			others = append(others, id)
		}
	}
	blocks, err := h.guard.CheckJobs(ctx, scope.TenantID, others, taskedit.Opts{NoReopen: true})
	if httputil.ServiceError(w, err, "check", "task edit state") {
		return
	}
	var fileIDs, jobIDs, refusedFiles []int64
	for _, j := range scope.Jobs {
		if j.JobID == origin.JobID || blocks[j.JobID] == nil {
			fileIDs = append(fileIDs, j.FileID)
			jobIDs = append(jobIDs, j.JobID)
		} else {
			refusedFiles = append(refusedFiles, j.FileID)
		}
	}
	res, err := h.propagate(ctx, scope.TenantID, req, lang, fileIDs, jobIDs)
	if httputil.ServiceError(w, err, "propagate", "translation") {
		return
	}
	if len(refusedFiles) > 0 {
		actorID := auth.UserIDFromContext(ctx)
		skipped, err := h.queries.CountPropagationTargets(ctx, db.CountPropagationTargetsParams{
			ActorID: &actorID, FileIds: refusedFiles, SourceText: req.SourceText,
			TargetLanguage: lang, ExcludeID: req.ExcludeID,
		})
		if httputil.ServiceError(w, err, "count", "propagation targets") {
			return
		}
		res.NotEditable = int(skipped.Targets + skipped.Held)
	}
	httputil.JSON(w, http.StatusOK, taskedit.WithReopened(map[string]any{"data": res}, origin.Reopened))
}

// propagateResult is what a propagation did: `propagated` siblings written
// (each row in `siblings`), `held` skipped because another user holds them,
// `not_editable` skipped because their file's task takes no edit.
type propagateResult struct {
	Propagated  int              `json:"propagated"`
	Held        int32            `json:"held"`
	NotEditable int              `json:"not_editable"`
	Siblings    []db.Translation `json:"siblings"`
}

// propagate writes req onto the unlocked same-source siblings in fileIDs,
// whose jobs (jobIDs) the caller has checked take the edit, locking those
// jobs for the write.
func (h *Handler) propagate(ctx context.Context, tenantID int64, req propagateRequest, lang string, fileIDs, jobIDs []int64) (propagateResult, error) {
	propagatedBy := auth.UserIDFromContext(ctx)
	count, err := h.queries.CountPropagationTargets(ctx, db.CountPropagationTargetsParams{
		ActorID:        &propagatedBy,
		FileIds:        fileIDs,
		SourceText:     req.SourceText,
		TargetLanguage: lang,
		ExcludeID:      req.ExcludeID,
	})
	if err != nil {
		return propagateResult{}, err
	}
	res := propagateResult{Held: count.Held, Siblings: []db.Translation{}}
	if count.Targets == 0 {
		return res, nil
	}

	// RETURNING * gives each affected row so the frontend can patch its
	// React Query cache for the siblings synchronously; an async refetch
	// leaves a window where a re-activated sibling starts from a stale
	// baseline and its next save gets a false 409.
	var siblings []db.Translation
	err = database.InJobsTx(ctx, h.pool, tenantID, jobIDs, func(_ pgx.Tx, q db.Querier) error {
		var err error
		siblings, err = q.PropagateTranslation(ctx, db.PropagateTranslationParams{
			FileIds:        fileIDs,
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
		return propagateResult{}, err
	}

	if len(siblings) > 0 {
		siblingIDs := make([]int64, len(siblings))
		for i, s := range siblings {
			siblingIDs[i] = s.ID
		}
		// Each sibling's target was just replaced, so its stored QA findings
		// describe text that is gone — ignores included, since the ignore is
		// fingerprinted on a message derived from that text. Confirm re-runs
		// QA only for the segment being confirmed, so nothing else corrects
		// the siblings.
		if err := h.queries.DeleteQAResultsForTranslations(ctx, siblingIDs); err != nil {
			return propagateResult{}, err
		}
		h.recordPropagationHistory(ctx, req, siblingIDs, propagatedBy)
	}
	res.Propagated = len(siblings)
	res.Siblings = siblings
	return res, nil
}

// recordPropagationHistory writes one `propagation` history row per sibling,
// in one statement: a common string can have thousands of siblings across the
// open files. The user who confirmed the originating segment owns the event.
// translations.origin is not touched (sticky through propagation, like human
// edits). History is the audit trail of a write that already committed, so a
// failure is logged, as for every other history event.
func (h *Handler) recordPropagationHistory(ctx context.Context, req propagateRequest, siblingIDs []int64, by int64) {
	details, err := json.Marshal(map[string]any{"source_translation_id": req.ExcludeID})
	if err == nil {
		err = h.queries.InsertPropagationHistoryBatch(ctx, db.InsertPropagationHistoryBatchParams{
			TranslationIds: siblingIDs, Text: &req.HumanText, RichText: req.HumanRichText,
			CreatedBy: &by, Details: details,
		})
	}
	if err != nil {
		logging.Error(ctx, err, "propagation history not recorded", "propagation-history-failed",
			logging.TranslationID(req.ExcludeID))
	}
}
