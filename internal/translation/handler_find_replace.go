package translation

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/taskedit"
)

// HandleFindReplace finds, or finds and replaces, text across a job's
// translations: POST /api/jobs/{jobID}/find-replace. `translation_ids` narrows
// it to the editor's selection; `include_locked` adds locked segments. Same
// gate as any segment edit: job access. Writing a locked segment also needs
// owner/admin, checked in the service.
func (h *Handler) HandleFindReplace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	var req struct {
		Find           string  `json:"find"`
		Replace        string  `json:"replace"`
		CaseSensitive  bool    `json:"case_sensitive"`
		Mode           string  `json:"mode"`            // "find", "replace_one", or "replace_all"
		TranslationID  int64   `json:"translation_id"`  // required for "replace_one"
		TranslationIDs []int64 `json:"translation_ids"` // optional: only these rows
		IncludeLocked  bool    `json:"include_locked"`  // optional: locked rows too (written by owner/admin only)
	}
	if err := httputil.Decode(r, &req); err != nil || req.Find == "" {
		httputil.BadRequest(w, "find is required")
		return
	}
	tenantID := auth.TenantIDFromContext(ctx)
	job, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if httputil.DBError(w, err, "job") {
		return
	}
	res, err := h.FindReplace(ctx, tenantID, auth.UserIDFromContext(ctx), job, FindReplaceParams{
		Find: req.Find, Replace: req.Replace, CaseSensitive: req.CaseSensitive,
		Mode: req.Mode, TranslationID: req.TranslationID, TranslationIDs: req.TranslationIDs,
		IncludeLocked: req.IncludeLocked,
	})
	if httputil.ServiceError(w, err, "update", "job") {
		return
	}
	if req.Mode == FindModeFind {
		ids := make([]int64, 0, len(res.Found))
		for _, m := range res.Found {
			ids = append(ids, m.TranslationID)
		}
		httputil.Data(w, map[string]any{"matches": res.Matches, "segments": res.Segments, "translation_ids": ids})
		return
	}
	httputil.JSON(w, http.StatusOK, taskedit.WithReopened(map[string]any{
		"data": map[string]any{
			"replaced": res.Replaced, "segments": res.Segments,
			"locked": res.Locked, "held": res.Held, "skipped_tags": res.SkippedTags,
			"conflicts": res.Conflicts, "rejected": res.Rejected, "failed": res.Failed,
		},
	}, res.TaskReopened))
}
