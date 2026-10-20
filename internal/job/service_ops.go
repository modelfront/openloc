package job

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/ptr"
	"openloc/internal/stepdef"
	"openloc/internal/stepround"
	"openloc/internal/taskedit"
	"openloc/internal/workflow"
)

// ClearResult is what a job-wide clear did.
type ClearResult struct {
	Cleared int64 `json:"cleared"`
	// Held: rows the clear would have cleared that another user has open.
	// Counted for a whole-job clear only.
	Held             int64 `json:"held"`
	WorkflowReopened bool  `json:"workflow_reopened"`
}

// ClearTranslations reverts the job's translations — every unlocked one with
// scope "all", or only the unconfirmed ones with "non_confirmed". First
// workflow step only, and never while a pipeline run holds the job.
func (s *Service) ClearTranslations(ctx context.Context, job db.Job, scope string) (ClearResult, error) {
	if err := s.PreflightClearTranslations(ctx, job, scope); err != nil {
		return ClearResult{}, err
	}
	scopeAll, _ := parseClearScope(scope)
	res, _, err := s.clearRows(ctx, job, clearRowsOpts{scopeAll: scopeAll})
	if err != nil {
		return ClearResult{}, err
	}
	s.emitJobEvent(ctx, projectevents.TypeJobCleared, job, map[string]any{
		"scope": scope, "cleared": res.Cleared, "workflow_reopened": res.WorkflowReopened,
	})
	return res, nil
}

// clearRowsOpts narrows clearRows. ids nil clears the whole job; historyBy set
// writes a `cleared` history row per cleared id, attributed to that user.
type clearRowsOpts struct {
	scopeAll  bool
	ids       []int64
	historyBy *int64
}

// clearRows blanks the job's clearable rows and drops their QA findings and AI
// remarks in one transaction, reopening the first step of a finished
// workflow. Callers run the preflight first. Returns the cleared ids.
func (s *Service) clearRows(ctx context.Context, job db.Job, opts clearRowsOpts) (ClearResult, []int64, error) {
	// Clear + workflow-revert run in one tx. Clearing a job whose workflow ran
	// to completion (current_step_id nil) reopens the first step; otherwise the
	// editor would compute read-only and the user would be stuck. Assignees are
	// preserved by the reset query.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClearResult{}, nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	// Serialises with a reopen-by-edit on the same job (taskedit).
	if _, err := q.LockJobForTaskEdit(ctx, db.LockJobForTaskEditParams{JobID: job.ID, TenantID: auth.TenantIDFromContext(ctx)}); err != nil {
		return ClearResult{}, nil, fmt.Errorf("lock job: %w", err)
	}

	actor := auth.UserIDPtr(auth.UserIDFromContext(ctx))
	clearedIDs, err := q.ClearTranslationsByJob(ctx, db.ClearTranslationsByJobParams{
		FileID:         job.FileID,
		TargetLanguage: job.TargetLanguage,
		ScopeAll:       opts.scopeAll,
		Ids:            opts.ids,
		ActorID:        actor,
	})
	if err != nil {
		return ClearResult{}, nil, fmt.Errorf("clear translations: %w", err)
	}
	res := ClearResult{Cleared: int64(len(clearedIDs))}
	if opts.ids == nil {
		if res.Held, err = q.CountHeldClearableByJob(ctx, db.CountHeldClearableByJobParams{
			JobID: job.ID, TenantID: auth.TenantIDFromContext(ctx), ActorID: actor, ScopeAll: opts.scopeAll,
		}); err != nil {
			return ClearResult{}, nil, fmt.Errorf("count held rows: %w", err)
		}
	}
	// Only the rows the clear touched lose their QA findings and AI remarks;
	// a human comment is about the segment and stays.
	if err := q.DeleteQAResultsForTranslations(ctx, clearedIDs); err != nil {
		return ClearResult{}, nil, fmt.Errorf("clear QA findings: %w", err)
	}
	if err := q.DeleteAICommentsForTranslations(ctx, clearedIDs); err != nil {
		return ClearResult{}, nil, fmt.Errorf("clear AI comments: %w", err)
	}
	if opts.historyBy != nil && len(clearedIDs) > 0 {
		if err := q.InsertClearedHistoryBatch(ctx, db.InsertClearedHistoryBatchParams{
			TranslationIds: clearedIDs, CreatedBy: *opts.historyBy,
		}); err != nil {
			return ClearResult{}, nil, fmt.Errorf("record cleared history: %w", err)
		}
	}

	// A job-wide clear reverts a finished workflow even when nothing was left
	// to clear; a selection that cleared nothing reverts nothing.
	if job.CurrentStepID == nil && (opts.ids == nil || len(clearedIDs) > 0) {
		steps, err := q.ListJobStepAssignments(ctx, job.ID)
		if err != nil {
			return ClearResult{}, nil, fmt.Errorf("list workflow steps: %w", err)
		}
		if len(steps) > 0 {
			first := steps[0] // ordered by step_order
			if err := q.ResetJobStepAssignments(ctx, job.ID); err != nil {
				return ClearResult{}, nil, fmt.Errorf("reset workflow steps: %w", err)
			}
			if err := q.ReopenStepAssignment(ctx, first.ID); err != nil {
				return ClearResult{}, nil, fmt.Errorf("reopen first step: %w", err)
			}
			if err := stepround.Open(ctx, q, job.ID, first.ID, stepround.Reset); err != nil {
				return ClearResult{}, nil, err
			}
			if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &first.ID}); err != nil {
				return ClearResult{}, nil, fmt.Errorf("update job step: %w", err)
			}
			res.WorkflowReopened = true
		}
	}
	// A completed job is open again once its workflow is.
	jobReopened := false
	if res.WorkflowReopened {
		n, err := q.MarkJobReopened(ctx, db.MarkJobReopenedParams{ID: job.ID, CurrentStep: pipeline.ReopenedStepLabel})
		if err != nil {
			return ClearResult{}, nil, fmt.Errorf("reopen job: %w", err)
		}
		jobReopened = n > 0
	}
	if err := tx.Commit(ctx); err != nil {
		return ClearResult{}, nil, fmt.Errorf("commit clear: %w", err)
	}
	if jobReopened {
		s.reevaluateAutoComplete(ctx, job.FileID)
	}
	return res, clearedIDs, nil
}

// PreflightClearTranslations runs every check ClearTranslations runs without
// writing: a valid scope plus preflightClearJob.
func (s *Service) PreflightClearTranslations(ctx context.Context, job db.Job, scope string) error {
	if _, ok := parseClearScope(scope); !ok {
		return httputil.NewClientError(`scope must be "non_confirmed" or "all"`)
	}
	return s.preflightClearJob(ctx, job)
}

// preflightClearJob is the job-state half of every clear: no pipeline run
// holding the job, the job's current task taking edits, first step only.
func (s *Service) preflightClearJob(ctx context.Context, job db.Job) error {
	if pipeline.IsPipelineRunning(job.Status, job.CurrentStep) {
		return httputil.NewConflictError("pipeline is running — wait for it to finish before clearing")
	}
	// The guard refuses an errored task, and a finished workflow for anyone
	// but a manager. Its reopen is not applied: on a finished workflow this
	// clear reverts to the first step instead.
	if _, err := s.guard.Check(ctx, auth.TenantIDFromContext(ctx), taskedit.Ref{JobID: job.ID}, taskedit.Opts{}); err != nil {
		return err
	}
	if !workflow.JobInFirstStep(ctx, s.queries, job) {
		return httputil.NewForbiddenError("clear is only available in the first workflow step")
	}
	return nil
}

// MTPreviewResult is one MT suggestion for a source segment.
type MTPreviewResult struct {
	Text     string `json:"text"`
	RichText string `json:"rich_text"`
	Engine   string `json:"engine"`
	// Stored: the pipeline's own MT output on record, not a live call.
	Stored bool `json:"stored,omitempty"`
	// The engine behind the text, as translation_history attributes it.
	MTEngineID *int64 `json:"-"`
	AIEngineID *int64 `json:"-"`
}

// MTPreview returns an MT translation of one source segment. When the
// pipeline already translated that segment (translationID > 0 with MT on
// record), the stored output is returned — a live call re-derives text
// already on record, and with an AI engine costs an LLM round trip.
func (s *Service) MTPreview(ctx context.Context, tenantID int64, job db.Job, sourceLang, sourceText string, translationID int64) (MTPreviewResult, error) {
	// The segment's own tagged source, resolved from translation_id so a
	// caller never chooses which markup the engine is asked to preserve.
	var sourceRichText string
	if translationID > 0 {
		stored, err := s.queries.GetStoredMTForTranslation(ctx, db.GetStoredMTForTranslationParams{
			TranslationID:  translationID,
			FileID:         job.FileID,
			TargetLanguage: job.TargetLanguage,
		})
		if err == nil && stored.Text != nil {
			return MTPreviewResult{
				Text:       *stored.Text,
				RichText:   ptr.Deref(stored.RichText),
				Engine:     engineNameFromDetails(stored.Details),
				Stored:     true,
				MTEngineID: stored.MtEngineID,
				AIEngineID: stored.AiEngineID,
			}, nil
		}
		// No MT on record (or a read error) falls through to the live fetch.
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			logging.Warn(ctx, err, "MT preview: stored result lookup failed", "mt-preview-stored-lookup-failed", logging.TranslationID(translationID))
		}
		rich, err := s.queries.GetSegmentRichTextForTranslation(ctx, db.GetSegmentRichTextForTranslationParams{
			TranslationID:  translationID,
			FileID:         job.FileID,
			TargetLanguage: job.TargetLanguage,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			logging.Warn(ctx, err, "MT preview: source rich text lookup failed", "mt-preview-rich-text-failed", logging.TranslationID(translationID))
		}
		sourceRichText = ptr.Deref(rich)
	}
	out, err := s.pipeline.MTPreview(ctx, tenantID, job.FileID, sourceLang, job.TargetLanguage, sourceText, sourceRichText, translationID)
	if err != nil {
		return MTPreviewResult{}, fmt.Errorf("mt preview: %w", err)
	}
	return MTPreviewResult{
		Text: out.Text, RichText: out.RichText, Engine: out.EngineName,
		MTEngineID: out.MTEngineID, AIEngineID: out.AIEngineID,
	}, nil
}

// Analysis returns the job's word-count analysis by match band, weighted by
// the tenant's analysis weights.
func (s *Service) Analysis(ctx context.Context, tenantID int64, job db.Job) (AnalysisBreakdown, error) {
	rows, err := s.queries.GetJobAnalysis(ctx, db.GetJobAnalysisParams{
		FileID:         job.FileID,
		TargetLanguage: job.TargetLanguage,
	})
	if err != nil {
		return AnalysisBreakdown{}, fmt.Errorf("job analysis: %w", err)
	}
	resp := aggregateJobAnalysis(rows)
	applyWeights(&resp, fetchWeightsMap(ctx, s.queries, tenantID))
	return resp, nil
}

// AssignJob assigns a member to the job's current workflow step, so the job
// shows on that member's Tasks page.
func (s *Service) AssignJob(ctx context.Context, tenantID, jobID, userID int64) error {
	job, err := s.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return err
	}
	if job.CurrentStepID == nil {
		return httputil.NewClientError("job has no active workflow step to assign")
	}
	current, err := s.queries.GetCurrentStepForJob(ctx, *job.CurrentStepID)
	if err != nil {
		return fmt.Errorf("load current step: %w", err)
	}
	if stepdef.IsAutomated(current.StepType) {
		return httputil.NewClientError("the job's current step is automated: it runs by itself and cannot be assigned")
	}
	assignee, err := workflow.CheckAssignee(ctx, s.queries, tenantID, userID)
	if err != nil {
		return err
	}
	if err := s.queries.AssignStepToUser(ctx, db.AssignStepToUserParams{
		ID:         *job.CurrentStepID,
		AssignedTo: &userID,
	}); err != nil {
		return fmt.Errorf("assign job: %w", err)
	}
	s.emitJobEvent(ctx, projectevents.TypeJobAssigned, job, map[string]any{
		"assignee_id": userID, "assignee_email": assignee.Email, "assignee_name": projectevents.PersonName(assignee),
	})
	return nil
}
