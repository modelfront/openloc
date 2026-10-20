package project

// Library workflow ↔ project: copy a library workflow
// onto the project, or promote the project's steps into the library. Both are
// manager surface (the /api/projects group is RequireManager).

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/projectevents"
	"openloc/internal/settings"
	"openloc/internal/workflowlib"
)

// HandleApplyWorkflow replaces the project's steps with a snapshot of a
// library workflow. POST /api/projects/{projectID}/workflow/apply {workflow_id}.
// Jobs already running constrain it like any step edit: a position some job
// has reached must keep its identity, else 409 (settings.ReplaceWorkflowSteps).
func (h *Handler) HandleApplyWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	var req struct {
		WorkflowID int64 `json:"workflow_id"`
	}
	if err := httputil.Decode(r, &req); err != nil || req.WorkflowID == 0 {
		httputil.BadRequest(w, "workflow_id is required")
		return
	}
	out, err := h.ApplyWorkflow(r.Context(), auth.TenantIDFromContext(r.Context()), id, req.WorkflowID)
	if err != nil {
		WriteServiceError(w, err, "apply", "workflow")
		return
	}
	httputil.Data(w, out)
}

// HandleSaveWorkflowAs promotes the project's current steps into a new library
// workflow and points the project at it. POST /api/projects/{projectID}/workflow/save-as {name}.
func (h *Handler) HandleSaveWorkflowAs(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{ID: id, TenantID: tenantID})
	if httputil.DBError(w, err, "project") {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "name is required")
		return
	}
	steps, err := h.queries.ListSettingsWorkflowSteps(r.Context(), project.SettingsProfileID)
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
	if err := txq.SetSettingsProfileWorkflow(r.Context(), db.SetSettingsProfileWorkflowParams{ID: project.SettingsProfileID, WorkflowID: &wf.ID}); err != nil {
		httputil.ServerError(w, "stamp", "workflow")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "commit", "transaction")
		return
	}

	actorID, actorName := projectevents.ActorFromContext(r.Context())
	projectevents.Emit(r.Context(), h.queries, projectevents.Event{
		ProjectID: project.ID, TenantID: tenantID, Type: projectevents.TypeWorkflowSavedAs,
		ActorID: actorID, ActorName: actorName,
		Details: map[string]any{"workflow_id": wf.ID, "workflow_name": wf.Name},
	})
	httputil.Created(w, wf)
}

// HandleDetachWorkflow clears the project's "Based on …" pointer so its steps
// read as project-specific again. POST /api/projects/{projectID}/workflow/detach.
// The steps were already a copy, so nothing else changes;
// this is what unlocks editing in the UI.
func (h *Handler) HandleDetachWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{ID: id, TenantID: tenantID})
	if httputil.DBError(w, err, "project") {
		return
	}
	profile, err := h.queries.GetSettingsProfile(r.Context(), project.SettingsProfileID)
	if err != nil {
		httputil.ServerError(w, "get", "settings profile")
		return
	}
	if profile.WorkflowID == nil {
		httputil.NoContent(w)
		return
	}
	details := map[string]any{"workflow_id": *profile.WorkflowID}
	if name := settings.WorkflowName(r.Context(), h.queries, tenantID, profile.WorkflowID); name != nil {
		details["workflow_name"] = *name
	}
	if err := h.queries.SetSettingsProfileWorkflow(r.Context(), db.SetSettingsProfileWorkflowParams{ID: project.SettingsProfileID, WorkflowID: nil}); err != nil {
		httputil.ServerError(w, "detach", "workflow")
		return
	}
	actorID, actorName := projectevents.ActorFromContext(r.Context())
	projectevents.Emit(r.Context(), h.queries, projectevents.Event{
		ProjectID: project.ID, TenantID: tenantID, Type: projectevents.TypeWorkflowDetached,
		ActorID: actorID, ActorName: actorName, Details: details,
	})
	httputil.NoContent(w)
}
