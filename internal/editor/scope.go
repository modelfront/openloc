package editor

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// ScopeJob is one job of a scope with its file.
type ScopeJob struct {
	JobID        int64
	FileID       int64
	FileName     string
	FileFormat   db.FileFormat
	InlineHTML   bool
	StepName     string
	StepAssignee *int64
	// StepID is the step in progress (a job step assignment), 0 when none.
	StepID int64
}

// Scope is what one CAT editor shows: jobs of one project in one target
// language, in the order the editor lists their files. The single-file editor
// is a scope of one job.
type Scope struct {
	TenantID       int64
	ProjectID      int64
	TargetLanguage string
	Jobs           []ScopeJob
}

// FileIDs lists the scope's files in scope order.
func (s Scope) FileIDs() []int64 {
	out := make([]int64, len(s.Jobs))
	for i, j := range s.Jobs {
		out[i] = j.FileID
	}
	return out
}

// JobIDs lists the scope's jobs in scope order.
func (s Scope) JobIDs() []int64 {
	out := make([]int64, len(s.Jobs))
	for i, j := range s.Jobs {
		out[i] = j.JobID
	}
	return out
}

// ErrNotInScope refuses a job_ids list that names a job outside the scope.
var ErrNotInScope = &scopeError{http.StatusBadRequest, "bad_request", "job_ids must be jobs of the scope"}

// Narrow keeps the scope's jobs among jobIDs, in scope order; an empty list
// keeps every job. A job outside the scope is refused.
func (s Scope) Narrow(jobIDs []int64) (Scope, error) {
	if len(jobIDs) == 0 {
		return s, nil
	}
	want := make(map[int64]bool, len(jobIDs))
	for _, id := range jobIDs {
		want[id] = true
	}
	out := s
	out.Jobs = make([]ScopeJob, 0, len(jobIDs))
	for _, j := range s.Jobs {
		if want[j.JobID] {
			out.Jobs = append(out.Jobs, j)
			delete(want, j.JobID)
		}
	}
	if len(want) > 0 {
		return Scope{}, ErrNotInScope
	}
	return out, nil
}

// JobOfFile returns the scope's job on fileID.
func (s Scope) JobOfFile(fileID int64) (ScopeJob, bool) {
	for _, j := range s.Jobs {
		if j.FileID == fileID {
			return j, true
		}
	}
	return ScopeJob{}, false
}

// Scope refusals. Each carries its HTTP status for httputil.ServiceError.
var (
	ErrScopeNotFound      = &scopeError{http.StatusNotFound, "not_found", "job not found"}
	ErrScopeMixedLanguage = &scopeError{http.StatusUnprocessableEntity, "mixed_languages",
		"the jobs opened together must share one target language"}
	errScopeEmpty    = &scopeError{http.StatusBadRequest, "bad_request", "jobs must list 1 to 500 job ids"}
	errScopeRepeated = &scopeError{http.StatusBadRequest, "bad_request", "jobs lists a job twice"}
	errNotAssigned   = &scopeError{http.StatusForbidden, "forbidden", "job is not assigned to you"}
)

type scopeError struct {
	status  int
	code    string
	message string
}

func (e *scopeError) Error() string     { return e.message }
func (e *scopeError) StatusCode() int   { return e.status }
func (e *scopeError) ErrorCode() string { return e.code }

// ResolveScope loads jobIDs, in that order, for tenantID. projectID 0 accepts
// the project of the first job; otherwise every job must be in projectID. A
// job outside the tenant or the project reads as not found, so an id from
// another organization says nothing about it.
func ResolveScope(ctx context.Context, q db.Querier, tenantID, projectID int64, jobIDs []int64) (Scope, error) {
	if len(jobIDs) == 0 || len(jobIDs) > MaxScopeJobs {
		return Scope{}, errScopeEmpty
	}
	seen := make(map[int64]bool, len(jobIDs))
	for _, id := range jobIDs {
		if seen[id] {
			return Scope{}, errScopeRepeated
		}
		seen[id] = true
	}
	rows, err := q.ListEditorScopeJobs(ctx, db.ListEditorScopeJobsParams{JobIds: jobIDs, TenantID: tenantID})
	if err != nil {
		return Scope{}, fmt.Errorf("list scope jobs: %w", err)
	}
	byID := make(map[int64]db.ListEditorScopeJobsRow, len(rows))
	for _, r := range rows {
		byID[r.JobID] = r
	}
	scope := Scope{TenantID: tenantID, ProjectID: projectID, Jobs: make([]ScopeJob, len(jobIDs))}
	for i, id := range jobIDs {
		r, ok := byID[id]
		if !ok {
			return Scope{}, ErrScopeNotFound
		}
		if scope.ProjectID == 0 {
			scope.ProjectID = r.ProjectID
		}
		if r.ProjectID != scope.ProjectID {
			return Scope{}, ErrScopeNotFound
		}
		if i == 0 {
			scope.TargetLanguage = r.TargetLanguage
		} else if r.TargetLanguage != scope.TargetLanguage {
			return Scope{}, ErrScopeMixedLanguage
		}
		scope.Jobs[i] = ScopeJob{
			JobID: r.JobID, FileID: r.FileID, FileName: r.FileName, FileFormat: r.FileFormat,
			InlineHTML: r.InlineHtml, StepName: r.StepName, StepAssignee: r.StepAssignee, StepID: r.StepID,
		}
	}
	return scope, nil
}

// requireAssigned refuses a linguist any job of the scope not assigned to
// them. Every other role passes.
func requireAssigned(ctx context.Context, q db.Querier, scope Scope) error {
	if auth.RoleFromContext(ctx) != auth.RoleLinguist {
		return nil
	}
	userID := auth.UserIDFromContext(ctx)
	n, err := q.CountJobsAssignedToUser(ctx, db.CountJobsAssignedToUserParams{
		JobIds: scope.JobIDs(), UserID: &userID,
	})
	if err != nil {
		return fmt.Errorf("verify job assignments: %w", err)
	}
	if int(n) != len(scope.Jobs) {
		return errNotAssigned
	}
	return nil
}

// RequestScope resolves the scope a request names. Under /api/jobs/{jobID}
// it is that job, whose route group already checked tenant and assignment.
// Under /api/projects/{projectID} it is the jobs of `?jobs=1,2,3`, checked
// here: in the caller's tenant and project, one target language, and for a
// linguist each assigned to them. On failure it writes the response and
// returns false.
func RequestScope(w http.ResponseWriter, r *http.Request, q db.Querier) (Scope, bool) {
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	if chi.URLParam(r, "jobID") != "" {
		jobID, err := httputil.ParseID(r, "jobID")
		if err != nil {
			httputil.BadRequest(w, "invalid job ID")
			return Scope{}, false
		}
		scope, err := ResolveScope(ctx, q, tenantID, 0, []int64{jobID})
		if httputil.ServiceError(w, err, "resolve", "editor scope") {
			return Scope{}, false
		}
		return scope, true
	}
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return Scope{}, false
	}
	jobIDs, err := parseIDs(r.URL.Query().Get("jobs"))
	if err != nil {
		httputil.BadRequest(w, errScopeEmpty.message)
		return Scope{}, false
	}
	scope, err := ResolveScope(ctx, q, tenantID, projectID, jobIDs)
	if err == nil {
		err = requireAssigned(ctx, q, scope)
	}
	if httputil.ServiceError(w, err, "resolve", "editor scope") {
		return Scope{}, false
	}
	return scope, true
}
