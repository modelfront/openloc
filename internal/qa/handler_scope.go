package qa

import (
	"context"
	"errors"
	"io"
	"net/http"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/editor"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/segmentbulk"
)

// HandleScopeQAReport answers GET /api/projects/{projectID}/editor/qa-report?jobs=:
// the stored findings of every file of the scope in one report, the same
// shape as a job's report. editor.RequestScope checks tenant, project and a
// linguist's assignments; the findings are read by the files it resolved.
func (h *Handler) HandleScopeQAReport(w http.ResponseWriter, r *http.Request) {
	scope, ok := editor.RequestScope(w, r, h.queries)
	if !ok {
		return
	}
	ctx := r.Context()
	rows, err := h.queries.ListQAResultsForFiles(ctx, db.ListQAResultsForFilesParams{
		FileIds: scope.FileIDs(), TargetLanguage: scope.TargetLanguage, TenantID: scope.TenantID,
	})
	if err != nil {
		logging.Error(ctx, err, "QA report load failed", "qa.scope_report", logging.ProjectID(scope.ProjectID))
		httputil.ServerError(w, "get", "QA results")
		return
	}
	// The same columns as a job's report rows.
	jobRows := make([]db.ListQAResultsForJobRow, len(rows))
	for i, r := range rows {
		jobRows[i] = db.ListQAResultsForJobRow(r)
	}
	results := jobRowsToDTO(jobRows)
	// Every job of a scope is in one project, which has one QA profile.
	profileAssigned, err := h.scopeHasProfile(ctx, scope)
	if err != nil {
		logging.Error(ctx, err, "QA profile lookup failed", "qa.scope_report_profile", logging.ProjectID(scope.ProjectID))
		httputil.ServerError(w, "get", "QA profile")
		return
	}
	httputil.Data(w, ReportDTO{Summary: summaryOf(results), Results: results, ProfileAssigned: profileAssigned})
}

// HandleScopeRunQA answers POST /api/projects/{projectID}/editor/run-qa?jobs=:
// QA on every job of the scope (or of `job_ids`), or on the selected rows when
// the body carries `translation_ids` (each job checks those of its own file).
// It answers 200 with an outcome per job, in scope order; one job that fails
// leaves the others checked.
func (h *Handler) HandleScopeRunQA(w http.ResponseWriter, r *http.Request) {
	scope, ok := editor.RequestScope(w, r, h.queries)
	if !ok {
		return
	}
	var req struct {
		segmentbulk.Request
		// JobIDs narrows the run to these jobs of the scope; empty is all.
		JobIDs []int64 `json:"job_ids"`
	}
	if err := httputil.Decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	scope, err := scope.Narrow(req.JobIDs)
	if httputil.ServiceError(w, err, "narrow", "editor scope") {
		return
	}
	out, err := h.service.RunAndPersistScope(r.Context(), scope, req.TranslationIDs)
	if err != nil {
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		logging.Error(r.Context(), err, "QA scope run failed", "qa.run_scope", logging.ProjectID(scope.ProjectID))
		httputil.ServerError(w, "run", "QA check")
		return
	}
	httputil.Data(w, out)
}

// scopeHasProfile reports whether the scope's project has a QA profile, read
// through its first job.
func (h *Handler) scopeHasProfile(ctx context.Context, scope editor.Scope) (bool, error) {
	job, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: scope.Jobs[0].JobID, TenantID: scope.TenantID})
	if err != nil {
		return false, err
	}
	return h.service.JobHasQAProfile(ctx, job, scope.TenantID)
}

// summaryOf counts findings into buckets as CountQAIssuesByJob does: an
// ignored finding leaves its severity and counts as ignored.
func summaryOf(results []ResultDTO) ReportSummary {
	var s ReportSummary
	for _, r := range results {
		switch {
		case r.Ignored:
			s.Ignored++
		case r.Severity == "error":
			s.Error++
		case r.Severity == "warning":
			s.Warning++
		case r.Severity == "info":
			s.Info++
		}
	}
	return s
}
