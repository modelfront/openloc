// Package autostep does the job-side bookkeeping after automated work —
// pre-translation and AI steps — for the upload worker and the synchronous
// Apply TM / Apply MT path alike, so both leave a job in the same state.
package autostep

import (
	"context"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/project"
	"openloc/internal/projectevents"
	"openloc/internal/workflow"
)

// RecordMTMissing stores why a run that asked for MT found no engine for the
// job's language (see TranslateOutcome.MTMissing), or clears it when MT had
// one. Best-effort: the run's output is already written.
func RecordMTMissing(ctx context.Context, q db.Querier, jobID int64, reason pipeline.PhaseSkipReason) {
	if q == nil {
		return
	}
	var r *string
	if reason != "" {
		s := string(reason)
		r = &s
	}
	if err := q.SetJobMTSkipReason(ctx, db.SetJobMTSkipReasonParams{ID: jobID, Reason: r}); err != nil {
		logging.Warn(ctx, err, "job MT skip reason not recorded", "job-mt-skip-reason-failed", logging.JobID(jobID))
	}
}

// Complete advances the job when its active step is an automated step of
// stepType, recording details on the workflow.step_auto_completed event. When
// that was the workflow's last step there is no person left to complete the
// job, so it completes here too. Best-effort: the step's output is already
// written, so a failure leaves the step in_progress for a manager to complete
// by hand.
func Complete(ctx context.Context, q db.Querier, wf *workflow.Service, tenantID, jobID int64, stepType string, actor *int64, details map[string]any) {
	if wf == nil || q == nil {
		return
	}
	active, err := q.GetActiveStepForJob(ctx, jobID)
	if err != nil {
		return // no active step — nothing to complete
	}
	if active.StepType != stepType {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	details["step_type"] = stepType
	adv, err := wf.AdvanceJob(ctx, tenantID, jobID, active, actor, nil, true, details)
	if err != nil {
		logging.Error(ctx, err, "automated step auto-complete failed; step left in_progress", stepType+"-step-auto-complete-failed",
			logging.JobID(jobID), logging.WorkflowStep(active.StepName))
		return
	}
	logging.Info(ctx, "automated step auto-completed", stepType+"-step-auto-completed",
		logging.JobID(jobID), logging.WorkflowStep(active.StepName))
	if adv.WorkflowCompleted {
		completeJob(ctx, q, tenantID, jobID, stepType)
	}
}

// completeJob marks the job completed and re-evaluates the project's
// auto-complete rule. No TM write-back: an automated step's output is
// auto-approved and must not be sent to TM — AdvanceJob skips it on the auto
// path.
func completeJob(ctx context.Context, q db.Querier, tenantID, jobID int64, stepType string) {
	if err := q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{
		ID: jobID, Status: "completed", CurrentStep: "workflow complete",
	}); err != nil {
		logging.Error(ctx, err, "automated final-step job completion failed", stepType+"-step-job-complete-failed", logging.JobID(jobID))
		return
	}
	job, err := q.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return
	}
	file, err := q.GetFileTenant(ctx, db.GetFileTenantParams{ID: job.FileID, TenantID: tenantID})
	if err != nil {
		return
	}
	fileID, jID := job.FileID, job.ID
	projectevents.Emit(ctx, q, projectevents.Event{
		ProjectID: file.ProjectID,
		TenantID:  tenantID,
		Type:      projectevents.TypeJobCompleted,
		FileID:    &fileID,
		JobID:     &jID,
		Details:   map[string]any{"file_name": file.Name, "target_language": job.TargetLanguage},
	})
	project.Reevaluate(ctx, q, tenantID, file.ProjectID)
}
