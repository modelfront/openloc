package job

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
)

// JobView is a single job with derived status and translation counts.
type JobView struct {
	ID             int64  `json:"id"`
	FileID         int64  `json:"file_id"`
	TargetLanguage string `json:"target_language"`
	WordCount      int32  `json:"word_count"`
	// Project context travels with the job because linguists are barred from
	// /api/projects (RequireManager). The CAT editor needs the project id and
	// source language for every role — concordance scopes its TM lookup by
	// them, and SegmentRow needs the source language — so this is the one
	// source both roles read.
	ProjectID      int64  `json:"project_id"`
	ProjectName    string `json:"project_name"`
	SourceLanguage string `json:"source_language"`
	FileName       string `json:"file_name"`
	FileFormat     string `json:"file_format"`
	// FileInlineHTML: the file's strings are HTML (chips); false = plain text.
	FileInlineHTML bool    `json:"file_inline_html"`
	FileCreatedAt  string  `json:"file_created_at"`
	SegmentCount   int32   `json:"segment_count"`
	Status         string  `json:"status"`
	PipelineStatus string  `json:"pipeline_status"`
	CurrentStep    string  `json:"current_step,omitempty"`
	ErrorMessage   *string `json:"error_message,omitempty"`
	// FailedPhase names the automation a failed job stopped in; the editor and
	// the task matrix offer Re-run from it. Empty unless the job failed.
	FailedPhase pipeline.FailedPhase `json:"failed_phase,omitempty"`
	// MTSkipReason says why the last pre-translation asked for MT and got none
	// (no_assignment: no engine for this language; no_provider: an AI engine
	// that can't run it). The job is not done while it is set.
	MTSkipReason *string `json:"mt_skip_reason,omitempty"`
	// Set while a user's stop request is pending (the run ends at its next
	// chunk boundary); cleared when the job reaches its terminal state.
	StopRequestedAt *string           `json:"stop_requested_at,omitempty"`
	TranslatedCount *int32            `json:"translated_count,omitempty"`
	TotalCount      *int32            `json:"total_count,omitempty"`
	Counts          SegmentCounts     `json:"counts"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
	WorkflowStep    *WorkflowStepInfo `json:"workflow_step,omitempty"`
}

type WorkflowStepInfo struct {
	// ID is the active step ASSIGNMENT id — the target for deadline edits
	// (PATCH /api/jobs/{id}/workflow/steps/{ID}/deadline).
	ID       int64  `json:"id"`
	StepName string `json:"step_name"`
	// StepOrder enables first-step-only gating in the project page (Apply
	// TM / MT only show in step 0). The CAT editor doesn't need
	// it (it loads the full steps list), but the row-level view here has
	// only the active step, so we surface the order on the active step.
	StepOrder    int32  `json:"step_order"`
	AssignedTo   *int64 `json:"assigned_to,omitempty"`
	AssigneeName string `json:"assignee_name,omitempty"`
	Status       string `json:"status"`
	IsLastStep   bool   `json:"is_last_step"`
	// IsAiStep is true when the active step carries a QE+APE AI
	// engine — gates the manager "Re-run AI step" button in the UI.
	IsAiStep bool `json:"is_ai_step"`
	// StepType is human / hybrid / ai; an ai step has no
	// assignee and completes itself.
	StepType string `json:"step_type"`
	// Deadline lives on the active assignment. ISO 8601 or null.
	Deadline *string `json:"deadline"`
}

type SegmentCounts struct {
	Total         int32 `json:"total"`
	Untranslated  int32 `json:"untranslated"`
	PreTranslated int32 `json:"pre_translated"`
	// Untranslated/pre_translated are derived buckets computed in
	// GetJobTranslationCounts.
	AutoApproved int32 `json:"auto_approved"`
	NeedsReview  int32 `json:"needs_review"`
	Approved     int32 `json:"approved"`
	// Exact clear counts (locked + empty rows excluded) so the Clear-
	// translations dialog shows the real number, not an "up to N" upper bound.
	ClearableNonConfirmed int32 `json:"clearable_non_confirmed"`
	ClearableAll          int32 `json:"clearable_all"`
}

func deriveStatus(c SegmentCounts) string {
	if c.Total == 0 {
		return "created"
	}
	done := c.Approved + c.AutoApproved
	if done == c.Total {
		return "completed"
	}
	if c.Untranslated == c.Total {
		return "created"
	}
	if c.NeedsReview > 0 {
		return "needs_attention"
	}
	return "in_progress"
}

// countsKey is a lookup key for batch translation counts.
type countsKey struct {
	FileID         int64
	TargetLanguage string
}

// ListByProject returns every job of a project with its counts and active
// workflow step. An unknown or foreign project is pgx.ErrNoRows.
func (s *Service) ListByProject(ctx context.Context, tenantID, projectID int64) ([]JobView, error) {
	// Every job carries its project context (see JobView), and this is the
	// same project for all of them.
	project, err := s.queries.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		return nil, err
	}
	jobs, err := s.queries.ListJobsByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	return s.views(ctx, tenantID, project, jobs, nil)
}

// ListPageByProject returns up to limit jobs of a project with id above
// cursor, oldest first, with counts and steps for those jobs only. An unknown
// or foreign project is pgx.ErrNoRows.
func (s *Service) ListPageByProject(ctx context.Context, tenantID, projectID, cursor int64, limit int32) ([]JobView, error) {
	project, err := s.queries.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListJobsByProjectPage(ctx, db.ListJobsByProjectPageParams{ProjectID: projectID, Cursor: cursor, PageLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	if len(rows) == 0 {
		return []JobView{}, nil
	}
	jobs := make([]db.ListJobsByProjectRow, len(rows))
	ids := make([]int64, len(rows))
	for i, r := range rows {
		jobs[i] = db.ListJobsByProjectRow(r)
		ids[i] = r.ID
	}
	return s.views(ctx, tenantID, project, jobs, ids)
}

// views builds the JobViews of one project's jobs. jobIDs nil loads counts
// and steps for the whole project; otherwise only for those jobs.
func (s *Service) views(ctx context.Context, tenantID int64, project db.Project, jobs []db.ListJobsByProjectRow, jobIDs []int64) ([]JobView, error) {
	projectID := project.ID
	// Batch-fetch translation counts in one query (no N+1).
	tcRows, err := s.queries.GetTranslationCountsByProject(ctx, db.GetTranslationCountsByProjectParams{ProjectID: projectID, JobIds: jobIDs})
	if err != nil {
		return nil, fmt.Errorf("translation counts: %w", err)
	}
	tcMap := make(map[countsKey]SegmentCounts, len(tcRows))
	for _, tc := range tcRows {
		tcMap[countsKey{FileID: tc.FileID, TargetLanguage: tc.TargetLanguage}] = SegmentCounts{
			Total:                 tc.Total,
			Untranslated:          tc.Untranslated,
			PreTranslated:         tc.PreTranslated,
			AutoApproved:          tc.AutoApproved,
			NeedsReview:           tc.NeedsReview,
			Approved:              tc.Approved,
			ClearableNonConfirmed: tc.ClearableNonConfirmed,
			ClearableAll:          tc.ClearableAll,
		}
	}

	// Batch-fetch workflow step info for all jobs (no N+1). A failure leaves
	// the steps off rather than failing the list.
	wfSteps, wfErr := s.queries.ListActiveStepsForProject(ctx, db.ListActiveStepsForProjectParams{ProjectID: projectID, JobIds: jobIDs})
	if wfErr != nil {
		logging.Warn(ctx, wfErr, "active workflow steps lookup failed; jobs listed without them", "job-list-workflow-steps-failed", logging.ProjectID(projectID))
	}
	wfMap := make(map[int64]*WorkflowStepInfo, len(wfSteps))
	for _, ws := range wfSteps {
		info := &WorkflowStepInfo{
			ID:         ws.StepAssignmentID,
			StepName:   ws.StepName,
			StepOrder:  ws.StepOrder,
			Status:     ws.Status,
			AssignedTo: ws.AssignedTo,
			IsLastStep: ws.IsLastStep,
			IsAiStep:   ws.IsAiStep,
			StepType:   ws.StepType,
			Deadline:   formatOptionalTS(ws.Deadline),
		}
		if ws.AssigneeName != nil {
			info.AssigneeName = *ws.AssigneeName
		}
		wfMap[ws.JobID] = info
	}

	result := make([]JobView, len(jobs))
	for i, j := range jobs {
		c := tcMap[countsKey{FileID: j.FileID, TargetLanguage: j.TargetLanguage}]
		result[i] = JobView{
			ID:              j.ID,
			FileID:          j.FileID,
			TargetLanguage:  j.TargetLanguage,
			WordCount:       j.WordCount,
			ProjectID:       project.ID,
			ProjectName:     project.Name,
			SourceLanguage:  project.SourceLanguage,
			FileName:        j.FileName,
			FileFormat:      string(j.FileFormat),
			FileInlineHTML:  j.FileInlineHtml,
			FileCreatedAt:   formatTS(j.FileCreatedAt),
			SegmentCount:    j.SegmentCount,
			Status:          deriveStatus(c),
			PipelineStatus:  j.Status,
			CurrentStep:     j.CurrentStep,
			ErrorMessage:    j.ErrorMessage,
			FailedPhase:     pipeline.FailedPhaseOf(j.Status, j.CurrentStep),
			MTSkipReason:    j.MtSkipReason,
			StopRequestedAt: formatOptionalTS(j.StopRequestedAt),
			TranslatedCount: j.TranslatedCount,
			TotalCount:      j.TotalCount,
			Counts:          c,
			CreatedAt:       formatTS(j.CreatedAt),
			UpdatedAt:       formatTS(j.UpdatedAt),
			WorkflowStep:    wfMap[j.ID],
		}
	}
	return result, nil
}

// Detail returns one job with its counts and active workflow step. An
// unknown or foreign job is pgx.ErrNoRows.
func (s *Service) Detail(ctx context.Context, tenantID, jobID int64) (JobView, error) {
	job, err := s.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return JobView{}, err
	}
	file, err := s.queries.GetFile(ctx, job.FileID)
	if err != nil {
		return JobView{}, fmt.Errorf("get file: %w", err)
	}
	// Tenant-scoped even though GetJobTenant already proved ownership — the
	// project name and source language are the payload here, so the scope check
	// stays on the query that reads them.
	project, err := s.queries.GetProject(ctx, db.GetProjectParams{ID: file.ProjectID, TenantID: tenantID})
	if err != nil {
		return JobView{}, err
	}

	tc, err := s.queries.GetJobTranslationCounts(ctx, db.GetJobTranslationCountsParams{
		FileID:         job.FileID,
		TargetLanguage: job.TargetLanguage,
	})
	if err != nil {
		return JobView{}, fmt.Errorf("translation counts: %w", err)
	}
	c := SegmentCounts{
		Total:                 tc.Total,
		Untranslated:          tc.Untranslated,
		PreTranslated:         tc.PreTranslated,
		AutoApproved:          tc.AutoApproved,
		NeedsReview:           tc.NeedsReview,
		Approved:              tc.Approved,
		ClearableNonConfirmed: tc.ClearableNonConfirmed,
		ClearableAll:          tc.ClearableAll,
	}
	view := JobView{
		ID:              job.ID,
		FileID:          job.FileID,
		TargetLanguage:  job.TargetLanguage,
		WordCount:       job.WordCount,
		ProjectID:       project.ID,
		ProjectName:     project.Name,
		SourceLanguage:  project.SourceLanguage,
		FileName:        file.Name,
		FileFormat:      string(file.Format),
		FileInlineHTML:  file.InlineHtml,
		FileCreatedAt:   formatTS(file.CreatedAt),
		SegmentCount:    file.SegmentCount,
		Status:          deriveStatus(c),
		PipelineStatus:  job.Status,
		CurrentStep:     job.CurrentStep,
		ErrorMessage:    job.ErrorMessage,
		FailedPhase:     pipeline.FailedPhaseOf(job.Status, job.CurrentStep),
		MTSkipReason:    job.MtSkipReason,
		StopRequestedAt: formatOptionalTS(job.StopRequestedAt),
		TranslatedCount: job.TranslatedCount,
		TotalCount:      job.TotalCount,
		Counts:          c,
		CreatedAt:       formatTS(job.CreatedAt),
		UpdatedAt:       formatTS(job.UpdatedAt),
	}

	if job.CurrentStepID != nil {
		step, err := s.queries.GetCurrentStepForJob(ctx, *job.CurrentStepID)
		if err == nil {
			_, nextErr := s.queries.GetNextStepAssignment(ctx, step.ID)
			name := ""
			if step.AssigneeName != nil {
				name = *step.AssigneeName
			}
			view.WorkflowStep = &WorkflowStepInfo{
				ID:           step.ID,
				StepName:     step.StepName,
				StepOrder:    step.StepOrder,
				AssignedTo:   step.AssignedTo,
				AssigneeName: name,
				Status:       step.Status,
				IsLastStep:   errors.Is(nextErr, pgx.ErrNoRows),
				IsAiStep:     step.IsAiStep,
				StepType:     step.StepType,
				Deadline:     formatOptionalTS(step.Deadline),
			}
		}
	}
	return view, nil
}
