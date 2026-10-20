package workflow

import (
	"context"
	"errors"
	"io"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/notification"
	"openloc/internal/pipeline"
	"openloc/internal/stepdef"
)

// MaxWorkflowSteps is stepdef.MaxWorkflowSteps; every step writer enforces it
// server-side (the FE cap alone is not trusted).
const MaxWorkflowSteps = stepdef.MaxWorkflowSteps

// OnEventFunc is called when a workflow event occurs (step assigned, rejected, completed).
// The handler fires this asynchronously — errors are logged, not returned.
type OnEventFunc func(ctx context.Context, event notification.Event)

// AIStepEnqueuer enqueues a background QE+APE run for a job whose
// newly-active workflow step carries a QE+APE AI engine. Satisfied by
// *worker.Enqueuer; nil in tests / when async isn't wired (predict is skipped).
type AIStepEnqueuer interface {
	EnqueueAIStep(jobID, tenantID int64, triggeredBy *int64) error
}

type Handler struct {
	queries db.Querier
	svc     *Service
}

// NewHandler builds the HTTP handler over a fresh Service. onEvent may be nil.
func NewHandler(q db.Querier, tx pipeline.TxRunner, onEvent OnEventFunc) *Handler {
	return &Handler{queries: q, svc: NewService(q, tx, onEvent)}
}

// WithAIStep forwards the predict-on-start enqueuer to the Service. Returns h
// for chaining.
func (h *Handler) WithAIStep(enq AIStepEnqueuer) *Handler {
	h.svc.WithAIStep(enq)
	return h
}

// Service exposes the transition logic for callers that share this handler's
// wiring (tests).
func (h *Handler) Service() *Service { return h.svc }

// --- Job Step Assignments ---

// actorFrom is the caller of a step action.
func actorFrom(r *http.Request) Actor {
	return Actor{UserID: auth.UserIDFromContext(r.Context()), Role: auth.RoleFromContext(r.Context())}
}

// HandleListJobSteps returns all step assignments for a job.
func (h *Handler) HandleListJobSteps(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	steps, err := h.svc.ListJobSteps(r.Context(), auth.TenantIDFromContext(r.Context()), jobID)
	if httputil.ServiceError(w, err, "list", "job") {
		return
	}
	httputil.Data(w, steps)
}

// HandleAdvanceStep completes the current step and starts the next one. The
// optional body {"comment": "…"} is the hand-off note for the next step.
// The job is resolved tenant-scoped before any check that depends on it, so
// another tenant cannot tell 403 from 400 from 404 about this job.
func (h *Handler) HandleAdvanceStep(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	var req struct {
		Comment string `json:"comment"`
	}
	if err := httputil.Decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	res, err := h.svc.Advance(ctx, auth.TenantIDFromContext(ctx), jobID, actorFrom(r), req.Comment)
	if httputil.ServiceError(w, err, "advance", "workflow step") {
		return
	}
	if res.WorkflowCompleted {
		httputil.Data(w, map[string]any{"status": "workflow_completed", "message": "All workflow steps completed"})
		return
	}
	httputil.Data(w, map[string]any{"status": "advanced", "next_step_id": res.NextAssignmentID})
}

// HandleRejectStep rejects the current step and reopens an earlier one:
// to_step_id (a step assignment of the job), else the nearest earlier human
// or hybrid step. rerun_ai runs a reopened hybrid step's AI again; reopening
// an AI step always does. The assignee or a manager may reject; running the
// AI again needs a manager (the service checks both).
func (h *Handler) HandleRejectStep(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	var req struct {
		Reason   string `json:"reason"`
		ToStepID *int64 `json:"to_step_id"`
		RerunAI  bool   `json:"rerun_ai"`
	}
	if err := httputil.Decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	res, err := h.svc.Reject(r.Context(), auth.TenantIDFromContext(r.Context()), jobID, actorFrom(r), RejectOpts{
		Reason: req.Reason, ToStepID: req.ToStepID, RerunAI: req.RerunAI,
	})
	if httputil.ServiceError(w, err, "reject", "step") {
		return
	}
	httputil.Data(w, map[string]any{"status": "rejected", "reopened_step_id": res.ReopenedStepID, "rerun_ai": res.RerunAI})
}

// HandleAssignStep assigns a user to a step. Two-layer guard in the service:
// the job belongs to the tenant, and the step assignment belongs to the job.
func (h *Handler) HandleAssignStep(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	stepAssignmentID, err := httputil.ParseID(r, "stepID")
	if err != nil {
		httputil.BadRequest(w, "invalid step ID")
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := httputil.Decode(r, &req); err != nil || req.UserID == 0 {
		httputil.BadRequest(w, "user_id is required")
		return
	}
	err = h.svc.AssignStep(r.Context(), auth.TenantIDFromContext(r.Context()), jobID, stepAssignmentID, req.UserID)
	if httputil.ServiceError(w, err, "assign", "step") {
		return
	}
	httputil.Data(w, map[string]any{"assigned": true})
}

// HandleSetStepDeadline sets or clears the deadline on a job's step assignment
// (the deadline lives on the assignment, not the job). Same two-layer
// tenant guard as HandleAssignStep. Manager-gated at the route.
func (h *Handler) HandleSetStepDeadline(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	stepAssignmentID, err := httputil.ParseID(r, "stepID")
	if err != nil {
		httputil.BadRequest(w, "invalid step ID")
		return
	}
	var req struct {
		Deadline *string `json:"deadline"` // ISO 8601 string, or null/empty to clear
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	deadline, ok := httputil.ParseDeadline(req.Deadline)
	if !ok {
		httputil.BadRequest(w, "invalid deadline format (use ISO 8601)")
		return
	}
	err = h.svc.SetStepDeadline(r.Context(), auth.TenantIDFromContext(r.Context()), jobID, stepAssignmentID, deadline)
	if httputil.ServiceError(w, err, "update", "step") {
		return
	}
	httputil.Data(w, map[string]any{"updated": true})
}

// --- Helpers ---

// JobInFirstStep is true when the job has no workflow OR is on
// step_order=0 (settings.ReplaceWorkflowSteps assigns step_order positionally starting
// at 0; matches the FE's `step_order === 0` gating). Lookup failure
// degrades to true so an orphaned step row doesn't lock out basic ops.
func JobInFirstStep(ctx context.Context, q db.Querier, job db.Job) bool {
	if job.CurrentStepID == nil {
		return true
	}
	step, err := q.GetCurrentStepForJob(ctx, *job.CurrentStepID)
	if err != nil {
		return true
	}
	return step.StepOrder == 0
}
