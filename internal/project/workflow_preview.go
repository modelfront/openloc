package project

import (
	"context"
	"fmt"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/settings"
)

// WorkflowPreview is what a workflow change would do, found by making it in
// a transaction that is rolled back: the same checks and the same effects as
// the real write, so a preview that passes is a save that passes (unless a
// job reaches another step in between).
type WorkflowPreview struct {
	Added    int `json:"added"`
	Removed  int `json:"removed"`
	Changed  int `json:"changed"`
	OpenJobs int `json:"open_jobs"`
	// StartNow lists, in step order, the steps that start at once on jobs
	// that had already finished every step. Never nil.
	StartNow []StepStart `json:"start_now"`
	// Effects says the above in sentences, for the confirm dialog and MCP.
	Effects []string `json:"effects"`
}

// StepStart is a step a workflow change starts on jobs right away.
type StepStart struct {
	StepName string `json:"step_name"`
	RunsAI   bool   `json:"runs_ai"`
	Jobs     int    `json:"jobs"`
}

// PreviewWorkflowSteps is SetWorkflowSteps without the write.
func (h *Handler) PreviewWorkflowSteps(ctx context.Context, tenantID, id int64, steps []settings.StepInput) (WorkflowPreview, error) {
	return h.previewWorkflow(ctx, tenantID, id, func(q db.Querier, profileID int64) (settings.ReplaceResult, error) {
		return settings.ReplaceWorkflowSteps(ctx, q, tenantID, profileID, steps)
	})
}

// PreviewApplyWorkflow is ApplyWorkflow without the write.
func (h *Handler) PreviewApplyWorkflow(ctx context.Context, tenantID, id, workflowID int64) (WorkflowPreview, error) {
	return h.previewWorkflow(ctx, tenantID, id, func(q db.Querier, profileID int64) (settings.ReplaceResult, error) {
		return settings.ApplyWorkflow(ctx, q, tenantID, workflowID, profileID)
	})
}

func (h *Handler) previewWorkflow(ctx context.Context, tenantID, id int64, write func(db.Querier, int64) (settings.ReplaceResult, error)) (WorkflowPreview, error) {
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID})
	if err != nil {
		return WorkflowPreview{}, err
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return WorkflowPreview{}, fmt.Errorf("begin: %w", err)
	}
	// Never committed: the preview only reads what the write would do.
	defer tx.Rollback(ctx) //nolint:errcheck // the rollback is the point
	txq := db.New(tx)
	res, err := write(txq, project.SettingsProfileID)
	if err != nil {
		return WorkflowPreview{}, err
	}
	if err := checkLiveWorkflow(project, res); err != nil {
		return WorkflowPreview{}, err
	}
	// ListJobsByProject is project-scoped; project came from the tenant-scoped lookup.
	jobs, err := txq.ListJobsByProject(ctx, project.ID)
	if err != nil {
		return WorkflowPreview{}, fmt.Errorf("list jobs: %w", err)
	}
	p := WorkflowPreview{Changed: res.Changed, StartNow: []StepStart{}}
	if d := len(res.After) - len(res.Before); d > 0 {
		p.Added = d
	} else {
		p.Removed = -d
	}
	for _, j := range jobs {
		if j.Status != "completed" {
			p.OpenJobs++
		}
	}
	for _, st := range res.StartedSteps {
		if n := len(p.StartNow); n > 0 && p.StartNow[n-1].StepName == st.Step.Name {
			p.StartNow[n-1].Jobs++
			continue
		}
		p.StartNow = append(p.StartNow, StepStart{StepName: st.Step.Name, RunsAI: st.Step.AiRouterID != nil, Jobs: 1})
	}
	p.Effects = p.effects()
	return p, nil
}

func (p WorkflowPreview) effects() []string {
	if p.OpenJobs == 0 {
		return []string{"No job is in progress: only files added from now on use the new steps."}
	}
	out := []string{fmt.Sprintf("%s in progress. Each one follows the new steps when it gets to them.", plural(p.OpenJobs, "job is", "jobs are"))}
	for _, s := range p.StartNow {
		line := fmt.Sprintf("%s finished every step and %s “%s” now.", plural(s.Jobs, "job has", "jobs have"), pick(s.Jobs, "starts", "start"), s.StepName)
		if s.RunsAI {
			line += " Its AI runs at once, which may cost money."
		}
		out = append(out, line)
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// HandlePreviewWorkflowSteps answers what PUT /workflow-steps would do with
// the same body, without doing it: 200 with a WorkflowPreview, or the 400/409
// the PUT would return. POST /api/projects/{projectID}/workflow-steps/preview
// — manager surface.
func (h *Handler) HandlePreviewWorkflowSteps(w http.ResponseWriter, r *http.Request) {
	id, steps, ok := decodeWorkflowSteps(w, r)
	if !ok {
		return
	}
	p, err := h.PreviewWorkflowSteps(r.Context(), auth.TenantIDFromContext(r.Context()), id, steps)
	if err != nil {
		WriteServiceError(w, err, "preview", "workflow steps")
		return
	}
	httputil.Data(w, p)
}
