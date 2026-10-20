package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	"openloc/internal/comments"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/job"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/ptr"
	"openloc/internal/translation"
	"openloc/internal/translationmemory"
	"openloc/internal/workflow"
)

// JobOpsServices are the domain services behind the job_ops tools — the same
// instances the REST handlers use, so a tool cannot drift from its route.
type JobOpsServices struct {
	Jobs         *job.Service
	Workflow     *workflow.Service
	Translations *translation.Service
	Comments     *comments.Service
	TMMatches    tmMatcher
}

type tmMatcher interface {
	JobTMMatches(ctx context.Context, tenantID, jobID int64, sourceText string, translationID int64) ([]translationmemory.JobTMMatch, error)
}

// WireJobOps gives the direct backend its job_ops services. api.go calls it
// once the REST handlers exist; until then the tools answer "not available".
func WireJobOps(b Backend, s JobOpsServices) {
	if d, ok := b.(*directBackend); ok {
		d.jobOps = &s
	}
}

func (b *directBackend) jobServices() (*JobOpsServices, error) {
	if b.jobOps == nil {
		return nil, invalidf("this action is not available on this server")
	}
	return b.jobOps, nil
}

// resolvedJob is a job the caller may act on: tenant-scoped, and assigned
// when the caller is a linguist (the job helper's gate).
func (b *directBackend) resolvedJob(ctx context.Context, jobID int64) (*JobOpsServices, db.Job, string, error) {
	s, err := b.jobServices()
	if err != nil {
		return nil, db.Job{}, "", err
	}
	if _, err := b.job(ctx, jobID); err != nil {
		return nil, db.Job{}, "", err
	}
	tenantID, _ := scope(ctx)
	j, srcLang, err := s.Jobs.ResolveJob(ctx, tenantID, jobID)
	if err != nil {
		return nil, db.Job{}, "", serviceErr(err, "resolve job")
	}
	return s, j, srcLang, nil
}

func actor(ctx context.Context) workflow.Actor {
	_, userID := scope(ctx)
	return workflow.Actor{UserID: userID, Role: auth.RoleFromContext(ctx)}
}

// --- Jobs and workflow steps -------------------------------------------------

func (b *directBackend) ListJobSteps(ctx context.Context, in GetJobIn) (JobStepList, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return JobStepList{}, err
	}
	tenantID, _ := scope(ctx)
	rows, err := s.Workflow.ListJobSteps(ctx, tenantID, j.ID)
	if err != nil {
		return JobStepList{}, serviceErr(err, "list job steps")
	}
	out := JobStepList{JobID: j.ID, Steps: make([]JobStep, 0, len(rows))}
	for _, r := range rows {
		out.Steps = append(out.Steps, JobStep{
			StepAssignmentID: r.ID,
			StepName:         r.StepName,
			StepOrder:        r.StepOrder,
			StepType:         r.StepType,
			Status:           r.Status,
			AssignedTo:       r.AssignedTo,
			AssigneeName:     ptr.Deref(r.AssigneeName),
			Deadline:         tsString(r.Deadline),
			RejectionReason:  r.RejectionReason,
			HandoffNote:      ptr.Deref(r.HandoffNote),
		})
	}
	return out, nil
}

func (b *directBackend) GetJobAnalysis(ctx context.Context, in GetJobIn) (JobAnalysis, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return JobAnalysis{}, err
	}
	tenantID, _ := scope(ctx)
	a, err := s.Jobs.Analysis(ctx, tenantID, j)
	if err != nil {
		return JobAnalysis{}, serviceErr(err, "job analysis")
	}
	return JobAnalysis{JobID: j.ID, Analysis: a}, nil
}

// segmentSource resolves the source text a lookup tool works on: a segment
// of the job (checked to belong to the job's file) or the given free text.
func (b *directBackend) segmentSource(ctx context.Context, j db.Job, in SegmentSourceIn) (string, int64, error) {
	if in.TranslationID == 0 {
		text := strings.TrimSpace(in.SourceText)
		if text == "" {
			return "", 0, invalidf("pass translation_id (a segment of this job) or source_text")
		}
		return text, 0, nil
	}
	tenantID, _ := scope(ctx)
	t, err := b.q.GetTranslationInFileTenant(ctx, db.GetTranslationInFileTenantParams{
		ID: in.TranslationID, FileID: j.FileID, TenantID: tenantID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, errNotFound
	}
	if err != nil {
		return "", 0, fmt.Errorf("get translation: %w", err)
	}
	// Unscoped by signature; the segment id comes from the tenant-scoped row.
	seg, err := b.q.GetSegment(ctx, t.SegmentID)
	if err != nil {
		return "", 0, fmt.Errorf("get segment: %w", err)
	}
	return seg.SourceText, t.ID, nil
}

func (b *directBackend) PreviewMT(ctx context.Context, in SegmentSourceIn) (MTPreview, error) {
	s, j, srcLang, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return MTPreview{}, err
	}
	text, translationID, err := b.segmentSource(ctx, j, in)
	if err != nil {
		return MTPreview{}, err
	}
	tenantID, _ := scope(ctx)
	res, err := s.Jobs.MTPreview(ctx, tenantID, j, srcLang, text, translationID)
	if err != nil {
		return MTPreview{}, serviceErr(err, "mt preview")
	}
	return MTPreview{SourceText: text, Text: res.Text, Engine: res.Engine, Stored: res.Stored}, nil
}

func (b *directBackend) GetTMMatches(ctx context.Context, in SegmentSourceIn) (TMMatchList, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return TMMatchList{}, err
	}
	text, translationID, err := b.segmentSource(ctx, j, in)
	if err != nil {
		return TMMatchList{}, err
	}
	tenantID, _ := scope(ctx)
	hits, err := s.TMMatches.JobTMMatches(ctx, tenantID, j.ID, text, translationID)
	if err != nil {
		return TMMatchList{}, serviceErr(err, "tm matches")
	}
	out := TMMatchList{SourceText: text, Matches: make([]TMMatch, 0, len(hits)), Count: len(hits)}
	for _, h := range hits {
		out.Matches = append(out.Matches, TMMatch{
			Score: h.Score, Percent: h.Percent, SourceText: h.SourceText, TargetText: h.TargetText, TMName: h.TMName, TMID: h.TMID,
		})
	}
	return out, nil
}

func (b *directBackend) applyPhases(ctx context.Context, jobID int64, phases pipeline.TranslatePhases, label string) (PhaseRunResult, error) {
	s, j, srcLang, err := b.resolvedJob(ctx, jobID)
	if err != nil {
		return PhaseRunResult{}, err
	}
	tenantID, _ := scope(ctx)
	res, err := s.Jobs.ApplyPhases(ctx, tenantID, j, srcLang, phases, label)
	if err != nil {
		return PhaseRunResult{}, serviceErr(err, label)
	}
	return PhaseRunResult{JobID: j.ID, Stopped: res.Stopped, SkipReason: res.SkipReason, Held: res.Held}, nil
}

func (b *directBackend) ApplyTM(ctx context.Context, in GetJobIn) (PhaseRunResult, error) {
	return b.applyPhases(ctx, in.JobID, pipeline.TranslatePhases{UseTM: true}, "tm-apply")
}

func (b *directBackend) ApplyMT(ctx context.Context, in GetJobIn) (PhaseRunResult, error) {
	return b.applyPhases(ctx, in.JobID, pipeline.TranslatePhases{UseMT: true}, "mt-apply")
}

func (b *directBackend) AssignJob(ctx context.Context, in AssignJobIn) (AssignResult, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return AssignResult{}, err
	}
	tenantID, _ := scope(ctx)
	if err := s.Jobs.AssignJob(ctx, tenantID, j.ID, in.UserID); err != nil {
		return AssignResult{}, serviceErr(err, "assign job")
	}
	return AssignResult{JobID: j.ID, UserID: in.UserID}, nil
}

func (b *directBackend) AssignStep(ctx context.Context, in AssignStepIn) (AssignResult, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return AssignResult{}, err
	}
	tenantID, _ := scope(ctx)
	if err := s.Workflow.AssignStep(ctx, tenantID, j.ID, in.StepAssignmentID, in.UserID); err != nil {
		return AssignResult{}, serviceErr(err, "assign step")
	}
	return AssignResult{JobID: j.ID, UserID: in.UserID}, nil
}

func (b *directBackend) SetStepDeadline(ctx context.Context, in SetStepDeadlineIn) (StepDeadlineResult, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return StepDeadlineResult{}, err
	}
	var deadline pgtype.Timestamptz
	if raw := strings.TrimSpace(in.Deadline); raw != "" {
		ts, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return StepDeadlineResult{}, invalidf("deadline must be an RFC3339 timestamp like 2026-10-01T17:00:00Z, got %q", in.Deadline)
		}
		deadline = pgtype.Timestamptz{Time: ts, Valid: true}
	}
	tenantID, _ := scope(ctx)
	if err := s.Workflow.SetStepDeadline(ctx, tenantID, j.ID, in.StepAssignmentID, deadline); err != nil {
		return StepDeadlineResult{}, serviceErr(err, "set step deadline")
	}
	return StepDeadlineResult{StepAssignmentID: in.StepAssignmentID, Deadline: tsString(deadline)}, nil
}

// AdvanceStep completes the current step like the editor's Complete button:
// it warns about unconfirmed segments (refusing until allow_unconfirmed),
// hands the comment to the next step, and on the last step goes on to
// complete the job, as the editor chains it.
func (b *directBackend) AdvanceStep(ctx context.Context, in AdvanceStepIn) (AdvanceStepResult, error) {
	s, j, srcLang, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return AdvanceStepResult{}, err
	}
	counts, err := b.q.GetJobTranslationCounts(ctx, db.GetJobTranslationCountsParams{
		FileID: j.FileID, TargetLanguage: j.TargetLanguage,
	})
	if err != nil {
		return AdvanceStepResult{}, fmt.Errorf("count segments: %w", err)
	}
	unconfirmed := int(counts.Total - counts.Approved - counts.AutoApproved)
	if unconfirmed > 0 && !in.AllowUnconfirmed {
		return AdvanceStepResult{}, invalidf(
			"%d segment(s) are not yet confirmed; they would stay in their current state. "+
				"Ask the user whether to complete the step anyway, then call again with allow_unconfirmed=true.", unconfirmed)
	}
	tenantID, userID := scope(ctx)
	res, err := s.Workflow.Advance(ctx, tenantID, j.ID, actor(ctx), in.Comment)
	if err != nil {
		return AdvanceStepResult{}, serviceErr(err, "advance step")
	}
	out := AdvanceStepResult{JobID: j.ID, Status: "advanced", UnconfirmedSegments: unconfirmed}
	if !res.WorkflowCompleted {
		out.NextStepAssignmentID = res.NextAssignmentID
		if res.NextStep != nil {
			out.NextStepName = res.NextStep.Name
		}
		return out, nil
	}
	out.Status = "workflow_completed"
	// Re-read: the advance cleared current_step_id, which CompleteJob keys on.
	fresh, _, err := s.Jobs.ResolveJob(ctx, tenantID, j.ID)
	if err != nil {
		return AdvanceStepResult{}, fmt.Errorf("re-read job: %w", err)
	}
	if _, err := s.Jobs.CompleteJob(ctx, tenantID, userID, fresh, srcLang); err != nil {
		var inv errInvalid
		if mapped := serviceErr(err, "complete job"); errors.As(mapped, &inv) {
			out.Warning = "the last step is complete, but the job was not marked completed: " + inv.Error()
		} else {
			logging.Error(ctx, err, "completing the job after the last step failed", "mcp-advance-complete-job-failed", logging.JobID(j.ID))
			out.Warning = "the last step is complete, but marking the job completed failed; the error has been logged — the user can complete it from the editor"
		}
		return out, nil
	}
	out.JobCompleted = true
	return out, nil
}

func (b *directBackend) RejectStep(ctx context.Context, in RejectStepIn) (RejectStepResult, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return RejectStepResult{}, err
	}
	tenantID, _ := scope(ctx)
	res, err := s.Workflow.Reject(ctx, tenantID, j.ID, actor(ctx), workflow.RejectOpts{
		Reason: strings.TrimSpace(in.Reason), ToStepID: in.ToStepID, RerunAI: in.RerunAI,
	})
	if err != nil {
		return RejectStepResult{}, serviceErr(err, "reject step")
	}
	return RejectStepResult{JobID: j.ID, ReopenedStepAssignmentID: res.ReopenedStepID, RerunAI: res.RerunAI}, nil
}

func (b *directBackend) RerunAIStep(ctx context.Context, in GetJobIn) (AIStepRunResult, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return AIStepRunResult{}, err
	}
	tenantID, _ := scope(ctx)
	res, err := s.Jobs.RunAIStep(ctx, tenantID, j)
	if err != nil {
		return AIStepRunResult{}, serviceErr(err, "ai step")
	}
	out := AIStepRunResult{JobID: j.ID, Stopped: res.Stopped, Total: res.Total, AutoApproved: res.AutoApproved, NeedsReview: res.NeedsReview}
	if res.SkipReason == pipeline.PhaseSkipNoSegments {
		out.SkipReason = string(res.SkipReason)
	}
	return out, nil
}

func (b *directBackend) ExportJob(ctx context.Context, in ExportJobIn) (ExportLink, error) {
	j, err := b.job(ctx, in.JobID)
	if err != nil {
		return ExportLink{}, err
	}
	switch in.Format {
	case "", "source":
		return ExportLink{
			DownloadPath: fmt.Sprintf("/api/jobs/%d/export", j.JobID),
			Filename:     pipeline.SourceExportFilename(j.FileName, j.TargetLanguage),
		}, nil
	case "xliff":
		return ExportLink{
			DownloadPath: fmt.Sprintf("/api/jobs/%d/export?format=xliff", j.JobID),
			Filename:     pipeline.BilingualXLIFFFilename(j.FileName, j.TargetLanguage),
		}, nil
	}
	return ExportLink{}, invalidf("format must be source or xliff, got %q", in.Format)
}
