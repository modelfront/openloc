package job

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
)

// HandleStopJob requests a cooperative stop of the job's running pipeline run
// (pre-translation or QE+APE). The run ends at its next chunk boundary and the
// job lands in status 'stopped' with its saved work kept; Apply TM/MT resumes
// from there.
//
// Access: same gate as Apply TM/MT — managers and the job's assigned linguist
// (the /api/jobs/{jobID} group; no RequireManager).
func (h *Handler) HandleStopJob(w http.ResponseWriter, r *http.Request) {
	job, _, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}

	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stale, err := h.queries.RequestJobStop(dbCtx, job.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		httputil.Error(w, http.StatusConflict, "no pipeline run is in progress for this job (or it is already stopping)")
		return
	}
	if err != nil {
		logging.Error(r.Context(), err, "failed to request job stop", "job-stop-request-failed", logging.JobID(job.ID))
		httputil.ServerError(w, "request", "stop")
		return
	}

	// The flagged run was already dead (hard-killed pod): nothing will consume
	// the flag, so close the job out here. RequestJobStop decided this against
	// the row it wrote, so a run that is genuinely translating can never land
	// in this branch.
	if stale {
		h.markStopped(r.Context(), job.ID, "Stopped — the previous run was no longer active; run Pre-translate again to resume")
	}

	h.emitJobEvent(r.Context(), projectevents.TypeJobStopRequested, job, nil)
	httputil.JSON(w, http.StatusAccepted, map[string]any{"status": "stopping", "job_id": job.ID})
}

// finishStopped closes a synchronous run the user stopped: terminal 'stopped'
// status with a resumable label plus the pipeline_stopped history event.
func (s *Service) finishStopped(ctx context.Context, job db.Job, phase string, st pipeline.RunState) {
	s.markStopped(ctx, job.ID, pipeline.StoppedStepLabel(phase, st))
	details := map[string]any{"phase": phase}
	if st.Translated != nil && st.Total != nil {
		details["translated_count"] = *st.Translated
		details["total_count"] = *st.Total
	}
	s.emitJobEvent(ctx, projectevents.TypeJobPipelineStopped, job, details)
}

// markStopped mirrors setJobStatus: fresh short-lived context, logged failure.
func (s *Service) markStopped(ctx context.Context, jobID int64, label string) {
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.queries.MarkJobStopped(dbCtx, db.MarkJobStoppedParams{ID: jobID, CurrentStep: label}); err != nil {
		logging.Error(ctx, err, "failed to mark job as stopped", "job-stop-write-failed", logging.JobID(jobID))
	}
}
