package project

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/projectevents"
	"openloc/internal/settings"
)

// HandleSetWorkflowSteps replaces a project's workflow steps with library
// steps (settings.ReplaceWorkflowSteps: in-place by position, so ids and their
// job_step_assignments survive; a changed assignee is pushed onto in-flight
// jobs). PUT /api/projects/{projectID}/workflow-steps — manager surface.
func (h *Handler) HandleSetWorkflowSteps(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
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
	project, _, err := h.replaceWorkflowSteps(r.Context(), tenantID, id, req.Steps)
	if err != nil {
		WriteServiceError(w, err, "update", "workflow steps")
		return
	}
	after, err := settings.ListStepsWithDeadlines(r.Context(), h.queries, project.SettingsProfileID)
	if err != nil {
		httputil.ServerError(w, "list", "workflow steps")
		return
	}
	httputil.Data(w, after)
}

// ReplaceStepsWithAudit replaces a project's workflow steps and records the
// project-timeline event. Shared by the REST handler and the MCP deadline
// tool, so a step edit is audited identically whichever door it comes in by.
// The caller owns what follows: the REST handler also re-runs predict for an
// appended AI step, which a deadline-only patch can never produce.
func ReplaceStepsWithAudit(ctx context.Context, pool *pgxpool.Pool, q db.Querier, tenantID int64, project db.Project, steps []settings.StepInput) (settings.ReplaceResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return settings.ReplaceResult{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	txq := db.New(tx)
	res, err := settings.ReplaceWorkflowSteps(ctx, txq, tenantID, project.SettingsProfileID, steps)
	if err != nil {
		return settings.ReplaceResult{}, err
	}
	if err := checkLiveWorkflow(project, res); err != nil {
		return settings.ReplaceResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return settings.ReplaceResult{}, fmt.Errorf("commit: %w", err)
	}
	// Audit: `Before` is read inside the tx ahead of any
	// mutation, so it is the exact "before" of this replace.
	emitAssignmentsSet(ctx, q, projectevents.TypeWorkflowStepsSet, project.ID, tenantID,
		workflowStepsPayload(ctx, q, tenantID, res.Before),
		workflowStepsPayload(ctx, q, tenantID, res.After))
	return res, nil
}

// errPretranslateSwitch refuses a step write that moves a created project
// into or out of Pre-translation.
var errPretranslateSwitch = httputil.NewClientError("A created project cannot switch to or from Pre-translation. Choose it when you set up the project.")

// checkLiveWorkflow refuses a step write that switches a created project into
// or out of Pre-translation. The workflow is chosen at setup, so a draft
// may switch freely until it is published.
func checkLiveWorkflow(project db.Project, res settings.ReplaceResult) error {
	if project.Status == db.ProjectStatusDraft {
		return nil
	}
	if settings.IsPretranslateOnly(res.Before) != settings.IsPretranslateOnly(res.After) {
		return errPretranslateSwitch
	}
	return nil
}

// enqueueStartedAISteps runs predict-on-start for jobs whose newly
// appended hybrid/ai step a workflow edit started right away. Best-effort:
// the edit has committed; a failed enqueue is logged and the manager can
// re-run the AI step by hand.
func (h *Handler) enqueueStartedAISteps(ctx context.Context, tenantID int64, started []settings.StartedStep) {
	if h.aiEnqueuer == nil {
		return
	}
	actor := auth.UserIDFromContext(ctx)
	for _, st := range started {
		if st.Step.AiRouterID == nil {
			continue
		}
		if err := h.aiEnqueuer.EnqueueAIStep(st.JobID, tenantID, &actor); err != nil {
			logging.Warn(ctx, err, "enqueue AI step for appended workflow step failed", "workflow-append-ai-enqueue-failed", logging.JobID(st.JobID))
		}
	}
}
