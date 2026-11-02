package terminology

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
	"openloc/internal/settings"
)

// The editor's term quick-add. Both routes sit in the /api/jobs/{jobID} group,
// so any member the group admits may use them: a manager, or a linguist
// assigned to the job. The project's "Linguists can add terms" setting decides
// whether a linguist may add, and the status their term gets (pending or
// approved); a manager picks the status.

// JobTermBase is a term base the job's project uses for the job's pair.
type JobTermBase struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// JobTermBases is the answer to GET /api/jobs/{jobID}/term-bases. CanAdd is
// false for a linguist while the project does not let linguists add terms;
// LinguistStatus is the status a linguist's term gets.
type JobTermBases struct {
	TermBases      []JobTermBase `json:"term_bases"`
	CanAdd         bool          `json:"can_add"`
	LinguistStatus string        `json:"linguist_status"`
}

// mayAdd reports whether the caller may add terms to the job's project.
func mayAdd(r *http.Request, jc db.GetJobTermContextRow) bool {
	return auth.IsManager(r) || jc.LinguistTerms != settings.LinguistTermsOff
}

// linguistStatus is the status a linguist's term gets while they may add.
func linguistStatus(jc db.GetJobTermContextRow) string {
	if jc.LinguistTerms == settings.LinguistTermsApproved {
		return "approved"
	}
	return "pending"
}

// QuickAddTermRequest is the body of POST /api/jobs/{jobID}/terms.
type QuickAddTermRequest struct {
	TermBaseID int64  `json:"term_base_id"`
	SourceTerm string `json:"source_term"`
	TargetTerm string `json:"target_term"`
	Status     string `json:"status"`
	Definition string `json:"definition"`
	Notes      string `json:"notes"`
}

// jobTermContext resolves the route's job in the caller's tenant.
func (h *Handler) jobTermContext(w http.ResponseWriter, r *http.Request) (db.GetJobTermContextRow, bool) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return db.GetJobTermContextRow{}, false
	}
	jc, err := h.queries.GetJobTermContext(r.Context(), db.GetJobTermContextParams{
		JobID: jobID, TenantID: auth.TenantIDFromContext(r.Context()),
	})
	if httputil.DBError(w, err, "job") {
		return db.GetJobTermContextRow{}, false
	}
	return jc, true
}

func (h *Handler) jobTermBases(ctx context.Context, jc db.GetJobTermContextRow) ([]db.ListWritableTermBasesForPairRow, error) {
	return h.queries.ListWritableTermBasesForPair(ctx, db.ListWritableTermBasesForPairParams{
		TenantID:          auth.TenantIDFromContext(ctx),
		SettingsProfileID: jc.SettingsProfileID,
		SourceLanguage:    langcode.Normalize(jc.SourceLanguage),
		TargetLanguage:    langcode.Normalize(jc.TargetLanguage),
	})
}

// HandleListJobTermBases answers GET /api/jobs/{jobID}/term-bases: the term
// bases the job's project assigns to its language pair, best priority first.
func (h *Handler) HandleListJobTermBases(w http.ResponseWriter, r *http.Request) {
	jc, ok := h.jobTermContext(w, r)
	if !ok {
		return
	}
	rows, err := h.jobTermBases(r.Context(), jc)
	if err != nil {
		logging.Error(r.Context(), err, "list job term bases", "job-term-bases-list-failed", logging.JobID(jc.JobID))
		httputil.ServerError(w, "list", "term bases")
		return
	}
	out := make([]JobTermBase, 0, len(rows))
	for _, b := range rows {
		out = append(out, JobTermBase{ID: b.ID, Name: b.Name})
	}
	httputil.Data(w, JobTermBases{TermBases: out, CanAdd: mayAdd(r, jc), LinguistStatus: linguistStatus(jc)})
}

// HandleQuickAddJobTerm answers POST /api/jobs/{jobID}/terms: adds a
// source/target pair, in the job's languages, to a term base the job's
// project uses. A base the project does not use for the pair is not found.
func (h *Handler) HandleQuickAddJobTerm(w http.ResponseWriter, r *http.Request) {
	jc, ok := h.jobTermContext(w, r)
	if !ok {
		return
	}
	if !mayAdd(r, jc) {
		httputil.Error(w, http.StatusForbidden, "Adding terms from the editor is turned off for this project.")
		return
	}
	var req QuickAddTermRequest
	if err := httputil.Decode(r, &req); err != nil || req.TermBaseID == 0 {
		httputil.BadRequest(w, "term_base_id, source_term and target_term are required")
		return
	}
	ctx := r.Context()
	bases, err := h.jobTermBases(ctx, jc)
	if err != nil {
		logging.Error(ctx, err, "list job term bases", "job-term-bases-list-failed", logging.JobID(jc.JobID))
		httputil.ServerError(w, "add", "term")
		return
	}
	assigned := false
	for _, b := range bases {
		if b.ID == req.TermBaseID {
			assigned = true
			break
		}
	}
	if !assigned {
		httputil.NotFound(w, "term base")
		return
	}
	base, err := h.queries.GetTermBase(ctx, db.GetTermBaseParams{ID: req.TermBaseID, TenantID: auth.TenantIDFromContext(ctx)})
	if httputil.DBError(w, err, "term base") {
		return
	}
	status := req.Status
	if !auth.IsManager(r) {
		status = linguistStatus(jc)
	}
	domain, err := h.projectDomain(ctx, jc.ProjectID)
	if err != nil {
		logging.Error(ctx, err, "read project domain", "job-term-domain-failed", logging.JobID(jc.JobID))
		httputil.ServerError(w, "add", "term")
		return
	}
	res, err := h.QuickAddTerm(ctx, base, QuickAddInput{
		SourceTerm:     req.SourceTerm,
		TargetTerm:     req.TargetTerm,
		TargetLanguage: jc.TargetLanguage,
		Status:         status,
		Definition:     req.Definition,
		Notes:          req.Notes,
		Domain:         domain,
	}, auth.UserIDFromContext(ctx))
	if httputil.ServiceError(w, err, "add", "term") {
		return
	}
	httputil.Created(w, res)
}

// projectDomain is the project's Domain label value, or "" when it has none.
func (h *Handler) projectDomain(ctx context.Context, projectID int64) (string, error) {
	v, err := h.queries.GetProjectSystemLabelValue(ctx, db.GetProjectSystemLabelValueParams{
		ProjectID: projectID, TenantID: auth.TenantIDFromContext(ctx), SystemKey: &domainSystemKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v.Value, err
}
