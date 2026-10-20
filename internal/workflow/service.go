package workflow

// service.go — step-transition logic shared by the HTTP handler
// (HandleAdvanceStep) and the worker, which completes `ai` steps on its own
// once their QE+APE run has finished.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack"
	"openloc/internal/logging"
	"openloc/internal/notification"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/stepdef"
	"openloc/internal/stepround"
	"openloc/internal/strdist"
)

// Service owns the workflow transitions. The HTTP handler adds authz and the
// request/response shape around it; the worker calls it directly.
type Service struct {
	queries    db.Querier
	tx         pipeline.TxRunner
	onEvent    OnEventFunc
	aiEnqueuer AIStepEnqueuer
}

// NewService builds a Service. tx runs each step transition in one
// transaction; onEvent may be nil (notifications dropped).
func NewService(q db.Querier, tx pipeline.TxRunner, onEvent OnEventFunc) *Service {
	if onEvent == nil {
		onEvent = func(context.Context, notification.Event) {}
	}
	return &Service{queries: q, tx: tx, onEvent: onEvent}
}

// WithAIStep wires the predict-on-start enqueuer. enq may be nil.
func (s *Service) WithAIStep(enq AIStepEnqueuer) *Service {
	s.aiEnqueuer = enq
	return s
}

// AsyncNotifier adapts a notification sender into an OnEventFunc that runs
// the send on a detached goroutine, so a slow or panicking sender can never
// stall a request or a worker task.
func AsyncNotifier(send func(context.Context, notification.Event) error) OnEventFunc {
	return func(ctx context.Context, event notification.Event) {
		bgCtx := context.WithoutCancel(ctx)
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					logging.Error(bgCtx, fmt.Errorf("%v", rec), "notification send panicked", "workflow-notify-panic")
					errtrack.CapturePanic(bgCtx, rec, "notification", "workflow-event-send")
				}
			}()
			if err := send(bgCtx, event); err != nil {
				logging.Warn(bgCtx, err, "notification send failed", "workflow-notify-failed")
			}
		}()
	}
}

// AdvanceResult describes what AdvanceJob did.
type AdvanceResult struct {
	// WorkflowCompleted is true when the completed step was the last one.
	WorkflowCompleted bool
	// NextAssignmentID / NextStep / NextAssignee describe the step that is
	// now active. Zero / nil when WorkflowCompleted.
	NextAssignmentID int64
	NextStep         *db.WorkflowStep
	NextAssignee     *int64
}

// flushUnrecordedText records the text a step hands over that history never
// captured: segments a person edited and never confirmed. Drafts write no
// history by design, so that text otherwise lives only on the segment — the
// panel keeps showing the older confirmed version as the latest one, and the
// next person to confirm gets an edit distance measured from before the
// previous step, taking credit for work they did not do.
//
// The distance has to be computed here rather than in SQL: strdist.Levenshtein
// is the only place that number is produced, and Postgres has none without the
// fuzzystrmatch extension. The candidate set is bounded by "edited but never
// confirmed", so the loop is short — and empty on a job where everyone
// confirmed, since the query's own filters match nothing.
//
// Best-effort, like every other emission: a job still advances if the audit
// write fails. Attribution is the row's edited_by, the person who typed, not
// whoever clicked Complete.
func (s *Service) flushUnrecordedText(ctx context.Context, jobID, stepAssignmentID int64) {
	rows, err := s.queries.ListUnrecordedTextForJob(ctx, jobID)
	if err != nil {
		logging.Error(ctx, err, "step handoff: unrecorded-text lookup failed",
			"handoff-flush-lookup-failed", logging.JobID(jobID))
		return
	}
	for _, row := range rows {
		var baseline string
		if row.Baseline != nil {
			baseline = *row.Baseline
		}
		var text string
		if row.ActiveText != nil {
			text = *row.ActiveText
		}
		dist := int32(strdist.Levenshtein(baseline, text))
		pipeline.EmitHumanHistory(ctx, s.queries, pipeline.HumanEmitOpts{
			TranslationID:       row.ID,
			Kind:                db.TranslationHistoryKindHuman,
			Text:                row.ActiveText,
			RichText:            row.ActiveRichText,
			CreatedBy:           row.EditedBy,
			EditDistance:        &dist,
			JobStepAssignmentID: &stepAssignmentID,
			Details:             map[string]any{"source": "step_handoff"},
		})
	}
}

// AdvanceJob completes the job's active step and starts the next one, or
// closes the workflow when there is none. The completed step's round is
// recorded per segment and the next step starts without the previous step's
// approvals and drafts, all in one transaction. It also emits the audit row, fires the
// assignment / completion notifications, and enqueues the QE+APE run when the
// newly-active step carries an engine.
//
// actor is the user driving the advance (nil for a system action). note is
// the hand-off note for whoever works the next step (nil for none); it is
// stored on that step, replacing any earlier one, and sent with that step's
// assignment notification. An automated step completed without a note of its
// own passes on the one it was handed. auto marks an `ai` step completing
// itself: the audit row is workflow.step_auto_completed carrying autoDetails
// (outcome counts). The caller has already verified the
// job's tenant and, for a human advance, the caller's right to complete it.
func (s *Service) AdvanceJob(ctx context.Context, tenantID, jobID int64, active db.GetActiveStepForJobRow, actor *int64, note *string, auto bool, autoDetails map[string]any) (AdvanceResult, error) {
	if !auto {
		s.flushUnrecordedText(ctx, jobID, active.ID)
	}

	// The note the next step receives: an automated step completed without a
	// note of its own passes on the one it was handed.
	handoff := note
	if handoff == nil && stepdef.IsAutomated(active.StepType) {
		handoff = active.HandoffNote
	}

	var (
		dismissed *string
		next      db.GetNextStepAssignmentRow
		nextStep  db.WorkflowStep
		assignee  *int64
		completed bool
	)
	// A deadlock victim runs again: the body resets what it sets.
	err := database.RetryConflicts(ctx, "step-advance-conflict-retried", func() error {
		dismissed, next, nextStep, assignee, completed = nil, db.GetNextStepAssignmentRow{}, db.WorkflowStep{}, nil, false
		return s.tx.InTx(ctx, func(q db.Querier) error {
			if _, err := q.LockJobForTaskEdit(ctx, db.LockJobForTaskEditParams{JobID: jobID, TenantID: tenantID}); err != nil {
				return fmt.Errorf("lock job: %w", err)
			}
			if err := stepround.Close(ctx, q, active.ID, stepround.Completed); err != nil {
				return err
			}
			if err := q.CompleteStepAssignment(ctx, active.ID); err != nil {
				return fmt.Errorf("complete step: %w", err)
			}
			var err error
			if dismissed, err = DismissFailure(ctx, q, jobID); err != nil {
				return err
			}
			next, err = q.GetNextStepAssignment(ctx, active.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				completed = true
				if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: nil}); err != nil {
					return fmt.Errorf("clear job step: %w", err)
				}
				return nil
			}
			if err != nil {
				return fmt.Errorf("get next step: %w", err)
			}
			if nextStep, err = q.GetWorkflowStep(ctx, next.WorkflowStepID); err != nil {
				return fmt.Errorf("get next workflow step: %w", err)
			}
			// An automated step has no person on it. Anything else keeps the
			// person a manager put on it, and takes the blueprint's default
			// assignee only when it has none.
			if !stepdef.IsAutomated(nextStep.StepType) {
				assignee = next.AssignedTo
				if assignee == nil {
					def, err := q.GetDefaultAssigneeForStep(ctx, next.ID)
					if err != nil && !errors.Is(err, pgx.ErrNoRows) {
						return fmt.Errorf("get default assignee: %w", err)
					}
					assignee = def
				}
			}
			if err := q.StartNextStep(ctx, db.StartNextStepParams{
				ID: next.ID, AssignedTo: assignee, HandoffNote: handoff,
			}); err != nil {
				return fmt.Errorf("start next step: %w", err)
			}
			if err := stepround.Open(ctx, q, jobID, next.ID, stepround.Resume); err != nil {
				return err
			}
			if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &next.ID}); err != nil {
				return fmt.Errorf("update job step: %w", err)
			}
			return nil
		})
	})
	if err != nil {
		return AdvanceResult{}, err
	}

	eventType := projectevents.TypeStepAdvanced
	details := map[string]any{"from_step": active.StepName}
	if dismissed != nil {
		details["error_dismissed"] = projectevents.ShortMessage(*dismissed)
	}
	if auto {
		eventType = projectevents.TypeStepAutoCompleted
		for k, v := range autoDetails {
			details[k] = v
		}
	}
	if note != nil {
		details["handoff_note"] = *note
	}

	if completed {
		if actor != nil {
			pID, pName, fName, tLang := s.resolveJobContext(ctx, jobID)
			s.onEvent(ctx, notification.Event{
				Type: notification.EventCompletion, TenantID: tenantID, RecipientID: *actor,
				JobID: jobID, ProjectID: pID, ProjectName: pName, FileName: fName, TargetLang: tLang,
			})
		}
		details["workflow_completed"] = true
		s.emitStepEvent(ctx, tenantID, eventType, jobID, details)
		return AdvanceResult{WorkflowCompleted: true}, nil
	}

	details["to_step"] = nextStep.Name
	details["to_step_type"] = nextStep.StepType
	s.emitStepEvent(ctx, tenantID, eventType, jobID, details)

	// Predict-on-start: a hybrid or ai step runs its AI router's engine
	// as soon as it becomes active. The worker completes an ai step itself
	// afterwards.
	if s.aiEnqueuer != nil && nextStep.AiRouterID != nil {
		if err := s.aiEnqueuer.EnqueueAIStep(jobID, tenantID, actor); err != nil {
			logging.Warn(ctx, err, "enqueue AI step on advance failed", "workflow-advance-ai-enqueue-failed", logging.JobID(jobID))
		}
	}

	if assignee != nil {
		pID, pName, fName, tLang := s.resolveJobContext(ctx, jobID)
		ev := notification.Event{
			Type: notification.EventAssignment, TenantID: tenantID, RecipientID: *assignee,
			JobID: jobID, ProjectID: pID, ProjectName: pName, FileName: fName, TargetLang: tLang,
			StepName: nextStep.Name,
		}
		if handoff != nil {
			ev.Comment = *handoff
		}
		if actor != nil {
			ev.ActorID = *actor
			if u, err := s.queries.GetUser(ctx, *actor); err == nil {
				ev.ActorName = u.Name
			}
		} else {
			ev.ActorName = "Automated step"
		}
		s.onEvent(ctx, ev)
	}

	return AdvanceResult{NextAssignmentID: next.ID, NextStep: &nextStep, NextAssignee: assignee}, nil
}

// DismissFailure accepts a failed job's error when a person completes or sends
// back its task, so the error does not move onto the task that becomes current
// next. Returns the dismissed message, or nil when the job was not failed.
func (s *Service) DismissFailure(ctx context.Context, jobID int64) (*string, error) {
	return DismissFailure(ctx, s.queries, jobID)
}

// DismissFailure clears a failed job's error and leaves it idle. Returns the
// dismissed message, or nil when the job was not failed. Tenant scoping is the
// caller's.
func DismissFailure(ctx context.Context, q db.Querier, jobID int64) (*string, error) {
	msg, err := q.DismissJobFailure(ctx, db.DismissJobFailureParams{
		ID: jobID, CurrentStep: pipeline.ErrorDismissedStepLabel,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dismiss job failure: %w", err)
	}
	if msg == nil {
		empty := ""
		return &empty, nil
	}
	return msg, nil
}

// resolveJobContext fetches project/file info for a job to populate
// notification events. Best-effort: lookups that fail leave zero values.
func (s *Service) resolveJobContext(ctx context.Context, jobID int64) (projectID int64, projectName, fileName, targetLang string) {
	job, err := s.queries.GetJob(ctx, jobID)
	if err != nil {
		logging.Warn(ctx, err, "notification: resolve job failed", "workflow-notify-resolve-job-failed", logging.JobID(jobID))
		return
	}
	targetLang = job.TargetLanguage
	file, err := s.queries.GetFile(ctx, job.FileID)
	if err != nil {
		logging.Warn(ctx, err, "notification: resolve file failed", "workflow-notify-resolve-file-failed", logging.JobID(jobID))
		return
	}
	fileName = file.Name
	project, err := s.queries.GetProjectByID(ctx, file.ProjectID)
	if err != nil {
		logging.Warn(ctx, err, "notification: resolve project failed", "workflow-notify-resolve-project-failed", logging.JobID(jobID))
		return
	}
	return project.ID, project.Name, fileName, targetLang
}

// emitStepEvent records a project_events row for a workflow-step action.
// Best-effort: the job→file→project resolution only serves audit correlation
// (callers have already tenant-verified jobID), so a failed lookup skips the
// event and never blocks the canonical write. tenantID is explicit because
// the worker's context carries no auth claims.
func (s *Service) emitStepEvent(ctx context.Context, tenantID int64, eventType string, jobID int64, details map[string]any) {
	job, err := s.queries.GetJob(ctx, jobID)
	if err != nil {
		return
	}
	file, err := s.queries.GetFile(ctx, job.FileID)
	if err != nil {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	details["file_name"] = file.Name
	details["target_language"] = job.TargetLanguage
	actorID, actorName := projectevents.ActorFromContext(ctx)
	fileID := job.FileID
	jID := jobID
	projectevents.Emit(ctx, s.queries, projectevents.Event{
		ProjectID: file.ProjectID,
		TenantID:  tenantID,
		Type:      eventType,
		ActorID:   actorID,
		ActorName: actorName,
		FileID:    &fileID,
		JobID:     &jID,
		Details:   details,
	})
}
