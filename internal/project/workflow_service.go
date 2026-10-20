package project

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/projectevents"
	"openloc/internal/settings"
)

// Workflow services behind PUT /workflow-steps and POST /workflow/apply,
// shared with the MCP project tools. A bad input comes back as
// httputil.ClientError; an edit that would move a step some job has already
// reached as httputil.ConflictError (settings.ReplaceWorkflowSteps).

// AppliedWorkflow is the project's workflow after ApplyWorkflow.
type AppliedWorkflow struct {
	WorkflowID   int64             `json:"workflow_id"`
	WorkflowName *string           `json:"workflow_name"`
	Steps        []db.WorkflowStep `json:"steps"`
}

// SetWorkflowSteps replaces the project's steps in place by position, so step
// ids and their job assignments survive; a changed assignee moves onto jobs
// in flight and an appended AI step starts on jobs that already passed the end.
func (h *Handler) SetWorkflowSteps(ctx context.Context, tenantID, id int64, steps []settings.StepInput) ([]db.WorkflowStep, error) {
	_, res, err := h.replaceWorkflowSteps(ctx, tenantID, id, steps)
	if err != nil {
		return nil, err
	}
	return res.After, nil
}

// replaceWorkflowSteps is SetWorkflowSteps returning the project too, so the
// REST handler can re-read the steps with their language deadlines.
func (h *Handler) replaceWorkflowSteps(ctx context.Context, tenantID, id int64, steps []settings.StepInput) (db.Project, settings.ReplaceResult, error) {
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.Project{}, settings.ReplaceResult{}, err
	}
	res, err := ReplaceStepsWithAudit(ctx, h.pool, h.queries, tenantID, project, steps)
	if err != nil {
		return db.Project{}, settings.ReplaceResult{}, err
	}
	h.enqueueStartedAISteps(ctx, tenantID, res.StartedSteps)
	return project, res, nil
}

// ApplyWorkflow replaces the project's steps with a snapshot of a library
// workflow, under the same in-flight constraints as SetWorkflowSteps.
func (h *Handler) ApplyWorkflow(ctx context.Context, tenantID, id, workflowID int64) (AppliedWorkflow, error) {
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID})
	if err != nil {
		return AppliedWorkflow{}, err
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return AppliedWorkflow{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	txq := db.New(tx)
	res, err := settings.ApplyWorkflow(ctx, txq, tenantID, workflowID, project.SettingsProfileID)
	if err != nil {
		return AppliedWorkflow{}, err
	}
	if err := checkLiveWorkflow(project, res); err != nil {
		return AppliedWorkflow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AppliedWorkflow{}, fmt.Errorf("commit: %w", err)
	}
	h.enqueueStartedAISteps(ctx, tenantID, res.StartedSteps)

	out := AppliedWorkflow{
		WorkflowID:   workflowID,
		WorkflowName: settings.WorkflowName(ctx, h.queries, tenantID, &workflowID),
		Steps:        res.After,
	}
	details := map[string]any{
		"workflow_id": workflowID,
		"before":      workflowStepsPayload(ctx, h.queries, tenantID, res.Before),
		"after":       workflowStepsPayload(ctx, h.queries, tenantID, res.After),
	}
	if out.WorkflowName != nil {
		details["workflow_name"] = *out.WorkflowName
	}
	actorID, actorName := projectevents.ActorFromContext(ctx)
	projectevents.Emit(ctx, h.queries, projectevents.Event{
		ProjectID: project.ID, TenantID: tenantID, Type: projectevents.TypeWorkflowApplied,
		ActorID: actorID, ActorName: actorName, Details: details,
	})
	return out, nil
}
