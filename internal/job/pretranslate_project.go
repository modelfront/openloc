package job

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// PretranslateRequest narrows a project pre-translate: JobIDs nil means every
// job, Phase "" means TM and MT.
type PretranslateRequest struct {
	JobIDs []int64 `json:"job_ids"`
	Phase  string  `json:"phase"`
}

// maxPretranslateJobIDs caps job_ids on one pre-translate request.
const maxPretranslateJobIDs = 5000

// phases checks the request and returns the phases it asks for. The error is
// a ClientError.
func (req PretranslateRequest) phases() (pipeline.TranslatePhases, error) {
	if req.JobIDs != nil {
		if len(req.JobIDs) == 0 || len(req.JobIDs) > maxPretranslateJobIDs {
			return pipeline.TranslatePhases{}, httputil.NewClientError(fmt.Sprintf("job_ids must hold 1 to %d ids", maxPretranslateJobIDs))
		}
		for _, id := range req.JobIDs {
			if id <= 0 {
				return pipeline.TranslatePhases{}, httputil.NewClientError("job_ids must be positive")
			}
		}
	}
	switch req.Phase {
	case "":
		return pipeline.FullPipeline, nil
	case "tm":
		return pipeline.TranslatePhases{UseTM: true, Manual: req.JobIDs != nil}, nil
	case "mt":
		return pipeline.TranslatePhases{UseMT: true, Manual: req.JobIDs != nil}, nil
	}
	return pipeline.TranslatePhases{}, httputil.NewClientError(`phase must be "tm" or "mt"`)
}

// PretranslateProject queues pre-translation, one worker task per job: the
// Analysis tab's one click (every job not running or completed, TM and MT),
// the MCP pretranslate_project tool, and the bulk bar's Apply TM / Apply MT
// (req.JobIDs with one phase). Translate skips segments that already have a
// translation, so re-running a failed job is safe. Without an enqueuer
// (tests, dev without Redis) each job runs inline. An unknown or foreign
// project is pgx.ErrNoRows; a job id of another project is skipped.
func (h *Handler) PretranslateProject(ctx context.Context, tenantID, projectID int64, req PretranslateRequest) (PretranslateResult, error) {
	phases, err := req.phases()
	if err != nil {
		return PretranslateResult{}, err
	}
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		return PretranslateResult{}, err
	}
	var jobs []db.ListJobsByProjectRow
	if req.JobIDs == nil {
		if jobs, err = h.queries.ListJobsByProject(ctx, projectID); err != nil {
			return PretranslateResult{}, err
		}
	} else {
		rows, err := h.queries.ListJobsByProjectIDs(ctx, db.ListJobsByProjectIDsParams{ProjectID: projectID, JobIds: req.JobIDs})
		if err != nil {
			return PretranslateResult{}, err
		}
		for _, r := range rows {
			jobs = append(jobs, db.ListJobsByProjectRow(r))
		}
	}
	// Attributed to the caller so each job's translation_history rows carry
	// who started it.
	clicker := auth.UserIDFromContext(ctx)

	res := PretranslateResult{Total: len(jobs), Async: h.enqueuer != nil}
	if req.JobIDs != nil {
		res.Total = len(uniqueIDs(req.JobIDs))
	}
	for _, job := range jobs {
		var ok bool
		if req.JobIDs == nil {
			ok = h.queueProjectJob(ctx, tenantID, project, job, phases, clicker)
		} else {
			ok = h.queueSelectedJob(ctx, tenantID, project, job, phases, clicker)
		}
		if ok {
			res.Queued++
		}
	}
	res.Skipped = res.Total - res.Queued

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
			Details:   map[string]any{"queued": res.Queued, "skipped": res.Skipped, "total": res.Total, "phase": pipeline.PhaseNoun(phases)},
		})
	}
	return res, nil
}

// queueProjectJob queues one job of a whole-project pre-translate; a running
// or completed job is skipped.
func (h *Handler) queueProjectJob(ctx context.Context, tenantID int64, project db.Project, job db.ListJobsByProjectRow, phases pipeline.TranslatePhases, clicker int64) bool {
	// IsPipelineRunning, not a raw status compare: no writer ever sets
	// status "running", so only the step tells a job mid-flight.
	if pipeline.IsPipelineRunning(job.Status, job.CurrentStep) || job.Status == "completed" {
		return false
	}
	if h.enqueuer != nil {
		return h.enqueue(ctx, tenantID, project, job, phases, clicker)
	}
	// One failed job must not block the rest. QP+APE is not part of
	// pre-translate: it runs when a QE+APE workflow step starts.
	if _, err := h.pipeline.TranslateFile(ctx, tenantID, job.FileID, project.SourceLanguage, job.TargetLanguage, nil, phases); err != nil {
		logging.Warn(ctx, err, "project pre-translate failed for a job", "project-pretranslate-sync-failed",
			logging.JobID(job.ID), logging.ProjectID(project.ID))
		return false
	}
	return true
}

// queueSelectedJob queues one job of the bulk bar's Apply TM / Apply MT,
// under the same checks as the job's own button: not running, first workflow
// step, and a completed task is reopened. A job that fails a check is skipped.
func (h *Handler) queueSelectedJob(ctx context.Context, tenantID int64, project db.Project, job db.ListJobsByProjectRow, phases pipeline.TranslatePhases, clicker int64) bool {
	if pipeline.IsPipelineRunning(job.Status, job.CurrentStep) {
		return false
	}
	if h.enqueuer == nil {
		dbJob, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: job.ID, TenantID: tenantID})
		if err != nil {
			return skipped(ctx, err, project.ID, job.ID)
		}
		label := "mt-apply" // the label of the job's own button
		if phases.UseTM {
			label = "tm-apply"
		}
		if _, err = h.ApplyPhases(ctx, tenantID, dbJob, project.SourceLanguage, phases, label); err != nil {
			return skipped(ctx, err, project.ID, job.ID)
		}
		return true
	}
	edit, err := h.checkPretranslation(ctx, tenantID, job.ID)
	if err != nil {
		return skipped(ctx, err, project.ID, job.ID)
	}
	task, err := h.guard.Apply(ctx, tenantID, edit)
	if err != nil {
		logging.Warn(ctx, err, "bulk pre-translate could not reopen a task", "project-pretranslate-reopen-failed",
			logging.JobID(job.ID), logging.ProjectID(project.ID))
		return false
	}
	if task.Reopened {
		// The reopen moved the job out of 'completed': a refused task puts
		// it back to this state, not the one listed before the reopen.
		reopened, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: job.ID, TenantID: tenantID})
		if err != nil {
			return skipped(ctx, err, project.ID, job.ID)
		}
		job.Status, job.CurrentStep, job.ErrorMessage = reopened.Status, reopened.CurrentStep, reopened.ErrorMessage
	}
	return h.enqueue(ctx, tenantID, project, job, phases, clicker)
}

// enqueue queues one job. The enqueuer marks the job queued before it hands
// the task over, so a refused task puts the job back as it was; otherwise it
// would read as queued until the claim's staleness escape frees it.
func (h *Handler) enqueue(ctx context.Context, tenantID int64, project db.Project, job db.ListJobsByProjectRow, phases pipeline.TranslatePhases, clicker int64) bool {
	err := h.enqueuer.EnqueuePipeline(job.FileID, project.SourceLanguage, job.TargetLanguage, job.ID, tenantID, phases, &clicker)
	if err == nil {
		return true
	}
	logging.Warn(ctx, err, "project pre-translate could not queue a job", "project-pretranslate-enqueue-failed",
		logging.JobID(job.ID), logging.ProjectID(project.ID))
	// UpdateJobStatus clears the error message, and queueing cleared the
	// progress counts; the job gets both back.
	err = h.queries.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: job.ID, Status: job.Status, CurrentStep: job.CurrentStep})
	if err == nil && job.Status == "failed" && job.ErrorMessage != nil {
		err = h.queries.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: job.ID, ErrorMessage: job.ErrorMessage})
	}
	if err == nil {
		err = h.queries.RestoreJobProgress(ctx, db.RestoreJobProgressParams{ID: job.ID, TranslatedCount: job.TranslatedCount, TotalCount: job.TotalCount})
	}
	if err != nil {
		logging.Error(ctx, err, "job not put back after a refused pre-translate", "project-pretranslate-restore-failed",
			logging.JobID(job.ID), logging.ProjectID(project.ID))
	}
	return false
}

// skipped reports a job that failed a check. The task's own state (a
// ClientError, ForbiddenError or ConflictError) is an expected skip; anything
// else is logged.
func skipped(ctx context.Context, err error, projectID, jobID int64) bool {
	_, client := httputil.AsClientError(err)
	_, forbidden := httputil.AsForbiddenError(err)
	_, conflict := httputil.AsConflictError(err)
	if !client && !forbidden && !conflict {
		logging.Warn(ctx, err, "bulk pre-translate could not check a job", "project-pretranslate-check-failed",
			logging.JobID(jobID), logging.ProjectID(projectID))
	}
	return false
}

func uniqueIDs(ids []int64) map[int64]bool {
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
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
	// No body: every job, TM and MT. The bulk bar sends job_ids and a phase.
	var req PretranslateRequest
	if err := httputil.Decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	res, err := h.PretranslateProject(r.Context(), auth.TenantIDFromContext(r.Context()), projectID, req)
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
