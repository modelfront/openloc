package job

import (
	"context"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
)

// PretranslateResult counts a project-level pre-translate: jobs queued, jobs
// skipped (running, completed, or failed to enqueue), and the total.
type PretranslateResult struct {
	Queued  int `json:"queued"`
	Skipped int `json:"skipped"`
	Total   int `json:"total"`
	// Async is true when the work went to the queue rather than running inline.
	Async bool `json:"-"`
}

// PretranslateProject queues TM + MT pre-translation for every job of the
// project that is not running or completed — the Analysis tab's one click,
// and the MCP pretranslate_project tool. Translate skips segments that
// already have a translation, so re-running a failed job is safe. Without an
// enqueuer (tests, dev without Redis) each job runs inline. An unknown or
// foreign project is pgx.ErrNoRows.
func (h *Handler) PretranslateProject(ctx context.Context, tenantID, projectID int64) (PretranslateResult, error) {
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		return PretranslateResult{}, err
	}
	jobs, err := h.queries.ListJobsByProject(ctx, projectID)
	if err != nil {
		return PretranslateResult{}, err
	}
	// Attributed to the caller so each job's translation_history rows carry
	// who started it.
	clicker := auth.UserIDFromContext(ctx)

	res := PretranslateResult{Total: len(jobs), Async: h.enqueuer != nil}
	for _, job := range jobs {
		// IsPipelineRunning, not a raw status compare: no writer ever sets
		// status "running", so only the step tells a job mid-flight.
		if pipeline.IsPipelineRunning(job.Status, job.CurrentStep) || job.Status == "completed" {
			res.Skipped++
			continue
		}
		if h.enqueuer != nil {
			if err := h.enqueuer.EnqueuePipeline(job.FileID, project.SourceLanguage, job.TargetLanguage, job.ID, tenantID, pipeline.FullPipeline, &clicker); err != nil {
				logging.Warn(ctx, err, "project pre-translate could not queue a job", "project-pretranslate-enqueue-failed",
					logging.JobID(job.ID), logging.ProjectID(projectID))
				res.Skipped++
				continue
			}
			res.Queued++
			continue
		}
		// One failed job must not block the rest. QP+APE is not part of
		// pre-translate: it runs when a QE+APE workflow step starts.
		if _, err := h.pipeline.TranslateFile(ctx, tenantID, job.FileID, project.SourceLanguage, job.TargetLanguage, nil, pipeline.FullPipeline); err != nil {
			logging.Warn(ctx, err, "project pre-translate failed for a job", "project-pretranslate-sync-failed",
				logging.JobID(job.ID), logging.ProjectID(projectID))
			res.Skipped++
			continue
		}
		res.Queued++
	}

	// One event per request, not per job — the per-job outcomes stay on
	// jobs.status.
	if res.Queued > 0 {
		actorID, actorName := projectevents.ActorFromContext(ctx)
		projectevents.Emit(ctx, h.queries, projectevents.Event{
			ProjectID: project.ID,
			TenantID:  tenantID,
			Type:      projectevents.TypePretranslateStarted,
			ActorID:   actorID,
			ActorName: actorName,
			Details:   map[string]any{"queued": res.Queued, "skipped": res.Skipped, "total": res.Total},
		})
	}
	return res, nil
}

// HandleProjectPretranslate — POST /api/projects/{projectID}/pretranslate.
// 202 when work went to the queue, 200 when it ran inline or nothing queued;
// the body {queued, skipped, total} feeds the toast.
func (h *Handler) HandleProjectPretranslate(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	res, err := h.PretranslateProject(r.Context(), auth.TenantIDFromContext(r.Context()), projectID)
	if err != nil {
		httputil.ServiceError(w, err, "pretranslate", "project")
		return
	}
	status := http.StatusOK
	if res.Async && res.Queued > 0 {
		status = http.StatusAccepted
	}
	httputil.JSON(w, status, res)
}
