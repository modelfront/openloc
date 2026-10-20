package taskedit

import (
	"context"
	"net/http"

	"openloc/internal/auth"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// WriteError writes a guard error — a refusal as its status and code, anything
// else as a logged 500 — and reports whether err was non-nil.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if httputil.WriteCodedError(w, err) {
		return true
	}
	logging.Error(ctx, err, "task edit guard failed", "task-edit-guard-failed")
	httputil.ServerError(w, "check", "task")
	return true
}

// CheckHTTP is Check for the request's tenant and role. ok=false means a
// response was written.
func (g *Guard) CheckHTTP(w http.ResponseWriter, r *http.Request, ref Ref, opts Opts) (Decision, bool) {
	ctx := r.Context()
	d, err := g.Check(ctx, auth.TenantIDFromContext(ctx), ref, opts)
	if WriteError(ctx, w, err) {
		return Decision{}, false
	}
	return d, true
}

// ApplyHTTP is Apply for the request's tenant. ok=false means a response was
// written.
func (g *Guard) ApplyHTTP(w http.ResponseWriter, r *http.Request, d Decision) (Result, bool) {
	ctx := r.Context()
	res, err := g.Apply(ctx, auth.TenantIDFromContext(ctx), d)
	if WriteError(ctx, w, err) {
		return Result{}, false
	}
	return res, true
}

// EnsureHTTP is CheckHTTP then ApplyHTTP, for writes with nothing in between.
func (g *Guard) EnsureHTTP(w http.ResponseWriter, r *http.Request, ref Ref, opts Opts) (Result, bool) {
	d, ok := g.CheckHTTP(w, r, ref, opts)
	if !ok {
		return Result{}, false
	}
	return g.ApplyHTTP(w, r, d)
}

// WithReopened adds "task_reopened": true to a response body when the write
// put the job's completed task back in progress, so the editor reloads the
// task it is working on.
func WithReopened(body map[string]any, reopened bool) map[string]any {
	if reopened {
		body["task_reopened"] = true
	}
	return body
}
