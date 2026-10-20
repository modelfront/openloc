package template

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/settings"
	"openloc/internal/workflowlib"
)

// HandleListSteps returns a template's workflow steps.
// GET /api/templates/{templateID}/workflow-steps — manager surface.
func (h *Handler) HandleListSteps(w http.ResponseWriter, r *http.Request) {
	profileID, ok := h.resolveTemplateProfile(w, r)
	if !ok {
		return
	}
	steps, err := settings.ListStepsWithDeadlines(r.Context(), h.queries, profileID)
	if err != nil {
		httputil.ServerError(w, "list", "workflow steps")
		return
	}
	httputil.Data(w, steps)
}

// HandleSetSteps replaces a template's workflow steps with library steps.
// PUT /api/templates/{templateID}/workflow-steps — manager surface. Same
// in-place replace as the project path (settings.ReplaceWorkflowSteps); a
// template profile never carries job assignments, so nothing can be lost.
func (h *Handler) HandleSetSteps(w http.ResponseWriter, r *http.Request) {
	profileID, ok := h.resolveTemplateProfile(w, r)
	if !ok {
		return
	}
	var req struct {
		Steps []settings.StepInput `json:"steps"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	res, err := settings.ReplaceWorkflowStepsTx(r.Context(), h.pool, tenantID, profileID, req.Steps)
	if err != nil {
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		httputil.ServerError(w, "update", "workflow steps")
		return
	}
	httputil.Data(w, res.After)
}

// HandleApplyWorkflow copies a library workflow onto the template.
// POST /api/templates/{templateID}/workflow/apply {workflow_id} — manager surface.
func (h *Handler) HandleApplyWorkflow(w http.ResponseWriter, r *http.Request) {
	profileID, ok := h.resolveTemplateProfile(w, r)
	if !ok {
		return
	}
	var req struct {
		WorkflowID int64 `json:"workflow_id"`
	}
	if err := httputil.Decode(r, &req); err != nil || req.WorkflowID == 0 {
		httputil.BadRequest(w, "workflow_id is required")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "start", "transaction")
		return
	}
	defer tx.Rollback(r.Context())
	res, err := settings.ApplyWorkflow(r.Context(), db.New(tx), tenantID, req.WorkflowID, profileID)
	if err != nil {
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		httputil.ServerError(w, "apply", "workflow")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "commit", "transaction")
		return
	}
	httputil.Data(w, res.After)
}

// HandleSaveWorkflowAs promotes the template's steps into a new library
// workflow and points the template at it.
// POST /api/templates/{templateID}/workflow/save-as {name} — manager surface.
func (h *Handler) HandleSaveWorkflowAs(w http.ResponseWriter, r *http.Request) {
	profileID, ok := h.resolveTemplateProfile(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "name is required")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	steps, err := h.queries.ListSettingsWorkflowSteps(r.Context(), profileID)
	if err != nil {
		httputil.ServerError(w, "list", "workflow steps")
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "start", "transaction")
		return
	}
	defer tx.Rollback(r.Context())
	txq := db.New(tx)
	wf, err := workflowlib.CreateFromSteps(r.Context(), txq, tenantID, req.Name, steps)
	if err != nil {
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		if httputil.IsUniqueViolation(err) {
			httputil.Error(w, http.StatusConflict, "a workflow with this name already exists")
			return
		}
		httputil.ServerError(w, "create", "workflow")
		return
	}
	if err := txq.SetSettingsProfileWorkflow(r.Context(), db.SetSettingsProfileWorkflowParams{ID: profileID, WorkflowID: &wf.ID}); err != nil {
		httputil.ServerError(w, "stamp", "workflow")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "commit", "transaction")
		return
	}
	httputil.Created(w, wf)
}

// HandleDetachWorkflow clears the template's "Based on …" pointer.
// POST /api/templates/{templateID}/workflow/detach — manager surface.
func (h *Handler) HandleDetachWorkflow(w http.ResponseWriter, r *http.Request) {
	profileID, ok := h.resolveTemplateProfile(w, r)
	if !ok {
		return
	}
	if err := h.queries.SetSettingsProfileWorkflow(r.Context(), db.SetSettingsProfileWorkflowParams{ID: profileID, WorkflowID: nil}); err != nil {
		httputil.ServerError(w, "detach", "workflow")
		return
	}
	httputil.NoContent(w)
}
