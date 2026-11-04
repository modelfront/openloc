// Package scopecomplete completes the caller's step on every job of a CAT
// editor scope at once: the multi-file editor's Complete step dialog. Each
// job goes the way the single-file editor takes it (approve all if asked,
// advance the step, complete the job after its last step), on its own, so
// one job that cannot complete leaves the others done.
package scopecomplete

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/editor"
	"openloc/internal/httputil"
	"openloc/internal/job"
	"openloc/internal/logging"
	"openloc/internal/translation"
	"openloc/internal/workflow"
)

// Handler serves POST /api/projects/{projectID}/editor/complete?jobs=.
type Handler struct {
	queries      db.Querier
	translations *translation.Service
	workflow     *workflow.Service
	jobs         *job.Service
}

func NewHandler(q db.Querier, t *translation.Service, w *workflow.Service, j *job.Service) *Handler {
	return &Handler{queries: q, translations: t, workflow: w, jobs: j}
}

type request struct {
	// JobIDs narrows the scope to the files to complete; empty is all.
	JobIDs     []int64 `json:"job_ids"`
	Comment    string  `json:"comment"`
	ApproveAll bool    `json:"approve_all"`
	// Steps is the step in progress the caller saw per job (job id → step
	// assignment id, the index header's step_id). A job whose step moved on
	// since is left alone, so sending the same request twice never advances
	// a job twice.
	Steps map[int64]int64 `json:"steps"`
}

// HandleComplete completes the step on each requested job of the scope and
// answers 200 with a result per job, in scope order. Who may complete a step
// is each step's own rule (its assignee, or a manager), checked per job.
func (h *Handler) HandleComplete(w http.ResponseWriter, r *http.Request) {
	scope, ok := editor.RequestScope(w, r, h.queries)
	if !ok {
		return
	}
	var req request
	if err := httputil.Decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	scope, err := scope.Narrow(req.JobIDs)
	if httputil.ServiceError(w, err, "narrow", "editor scope") {
		return
	}
	jobIDs := scope.JobIDs()
	ctx := r.Context()
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: scope.ProjectID, TenantID: scope.TenantID})
	if httputil.ServiceError(w, err, "load", "project") {
		return
	}
	actor := workflow.Actor{UserID: auth.UserIDFromContext(ctx), Role: auth.RoleFromContext(ctx)}
	out := make([]JobResult, len(jobIDs))
	for i, jobID := range jobIDs {
		out[i] = h.completeOne(ctx, scope.TenantID, jobID, project.SourceLanguage, actor, req)
	}
	httputil.Data(w, out)
}

func (h *Handler) completeOne(ctx context.Context, tenantID, jobID int64, sourceLang string, actor workflow.Actor, req request) JobResult {
	res := JobResult{JobID: jobID}
	fail := func(err error) JobResult {
		res.Outcome = OutcomeFailed
		res.Error = jobError(ctx, jobID, err)
		return res
	}
	j, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return fail(err)
	}
	switch {
	case j.Status == "completed":
		// Done by an earlier try: report it as such.
		res.Outcome = OutcomeCompleted
		return res
	case j.CurrentStepID == nil:
		// The last step closed but completing the job did not (an earlier
		// try was cut off between the two): finish it.
		return h.finish(ctx, tenantID, j, sourceLang, actor, res, fail)
	}
	// Before approving anything: a file whose step moved on is left alone.
	if want, ok := req.Steps[jobID]; ok && *j.CurrentStepID != want {
		return fail(workflow.ErrStepChanged)
	}
	if req.ApproveAll {
		approved, err := h.translations.ConfirmJob(ctx, j, tenantID, actor.UserID)
		if err != nil {
			return fail(err)
		}
		for _, s := range approved.Skipped {
			if s.Reason.BlocksStepCompletion() {
				res.Blocked = append(res.Blocked, s)
			}
		}
		if len(res.Blocked) > 0 {
			res.Outcome = OutcomeBlocked
			return res
		}
	}
	// The step the caller saw, checked again under the job's lock.
	adv, err := h.workflow.AdvanceExpecting(ctx, tenantID, jobID, req.Steps[jobID], actor, req.Comment)
	if err != nil {
		return fail(err)
	}
	if !adv.WorkflowCompleted {
		res.Outcome = OutcomeAdvanced
		res.NextStepID = adv.NextAssignmentID
		return res
	}
	// The final advance chains into completing the job, as the single-file
	// editor does: that is what closes it and writes its approved rows to TM.
	j, err = h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return fail(err)
	}
	return h.finish(ctx, tenantID, j, sourceLang, actor, res, fail)
}

// finish completes a job whose last step has closed.
func (h *Handler) finish(ctx context.Context, tenantID int64, j db.Job, sourceLang string, actor workflow.Actor,
	res JobResult, fail func(error) JobResult) JobResult {
	if _, err := h.jobs.CompleteJob(ctx, tenantID, actor.UserID, j, sourceLang); err != nil {
		return fail(err)
	}
	res.Outcome = OutcomeCompleted
	return res
}

// jobError says what went wrong without leaking an internal error: a coded
// refusal or a client-facing message as is, anything else as a generic line,
// logged.
func jobError(ctx context.Context, jobID int64, err error) *JobError {
	if errors.Is(err, pgx.ErrNoRows) {
		return &JobError{Code: "not_found", Message: "job not found"}
	}
	var ce httputil.CodedError
	if errors.As(err, &ce) {
		return &JobError{Code: ce.ErrorCode(), Message: ce.Error()}
	}
	for _, as := range []func(error) (string, bool){
		httputil.AsClientError, httputil.AsForbiddenError, httputil.AsConflictError,
	} {
		if msg, ok := as(err); ok {
			return &JobError{Message: msg}
		}
	}
	logging.Error(ctx, err, "completing a step from the multi-file editor failed", "scope-complete-job-failed",
		logging.JobID(jobID))
	return &JobError{Code: "server_error", Message: "Could not complete this file's step"}
}
