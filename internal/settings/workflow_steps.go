package settings

// Profile workflow steps. Every writer of a settings
// profile's workflow_steps rows — project PUT, template PUT, project create,
// library apply — goes through ReplaceWorkflowSteps so the in-place diff that
// keeps job_step_assignments alive exists exactly once.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/airouter"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/ptr"
	"openloc/internal/stepdef"
	"openloc/internal/stepround"
)

// StepInput is one position of a profile's workflow as a client submits it.
// Identity comes from the library (StepDefinitionID); the only way to keep a
// pre-library row whose name is not in the library is ExistingStepID, which
// carries that row's snapshot forward unchanged. Exactly one of the two is set.
type StepInput struct {
	StepDefinitionID *int64  `json:"step_definition_id"`
	ExistingStepID   *int64  `json:"existing_step_id"`
	DefaultAssignee  *int64  `json:"default_assignee"`
	Deadline         *string `json:"deadline"` // ISO 8601, or null/empty
	// LanguageDeadlines override Deadline for named target languages; see
	// step_deadlines.go. Sent as a set — a language left out has no override.
	LanguageDeadlines []LanguageDeadlineInput `json:"language_deadlines"`
	// AIRouterID swaps the AI router a hybrid/ai step runs, for this profile
	// only (the library definition keeps its own). nil keeps the current
	// snapshot; anything on a human step is refused.
	AIRouterID *int64 `json:"ai_router_id"`
}

// resolvedStep is a StepInput with its identity fields filled in.
type resolvedStep struct {
	name          string
	stepType      string
	role          string
	aiRouterID    *int64
	definitionID  *int64
	assignee      *int64
	deadline      pgtype.Timestamptz
	langDeadlines []resolvedLanguageDeadline
}

// ValidateStepInputs resolves each input against the library and the
// profile's current rows (existing may be nil when the profile has none yet).
// Client-fixable problems come back as httputil.ClientError.
// The profile does not exist yet at pre-create validation time, so there is no
// project whose target languages a per-language deadline could be checked
// against; ReplaceWorkflowSteps does that check on the real write.
func ValidateStepInputs(ctx context.Context, q db.Querier, tenantID int64, existing []db.WorkflowStep, steps []StepInput) error {
	_, err := resolveSteps(ctx, q, tenantID, existing, steps, nil)
	return err
}

// allowedLangs bounds which target languages a per-language deadline may name;
// nil skips that check.
func resolveSteps(ctx context.Context, q db.Querier, tenantID int64, existing []db.WorkflowStep, steps []StepInput, allowedLangs map[string]bool) ([]resolvedStep, error) {
	if len(steps) == 0 {
		return nil, httputil.NewClientError("at least one workflow step is required")
	}
	if len(steps) > stepdef.MaxWorkflowSteps {
		return nil, httputil.NewClientError(fmt.Sprintf("a workflow can have at most %d steps", stepdef.MaxWorkflowSteps))
	}
	byID := make(map[int64]db.WorkflowStep, len(existing))
	for _, e := range existing {
		byID[e.ID] = e
	}
	out := make([]resolvedStep, 0, len(steps))
	for i, s := range steps {
		var r resolvedStep
		switch {
		case s.StepDefinitionID != nil && s.ExistingStepID != nil:
			return nil, httputil.NewClientError(fmt.Sprintf("step %d: give either step_definition_id or existing_step_id, not both", i+1))
		case s.StepDefinitionID != nil:
			def, err := q.GetApplicableStepDefinition(ctx, db.GetApplicableStepDefinitionParams{ID: *s.StepDefinitionID, TenantID: tenantID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil, httputil.NewClientError(fmt.Sprintf("step %d: step definition %d not found", i+1, *s.StepDefinitionID))
				}
				return nil, fmt.Errorf("get step definition: %w", err)
			}
			r = resolvedStep{name: def.Name, stepType: def.StepType, role: def.RoleRequired, aiRouterID: def.AiRouterID, definitionID: &def.ID}
		case s.ExistingStepID != nil:
			e, ok := byID[*s.ExistingStepID]
			if !ok {
				return nil, httputil.NewClientError(fmt.Sprintf("step %d: existing step %d is not part of this workflow", i+1, *s.ExistingStepID))
			}
			r = resolvedStep{name: e.Name, stepType: e.StepType, role: e.RoleRequired, aiRouterID: e.AiRouterID, definitionID: e.StepDefinitionID}
		default:
			return nil, httputil.NewClientError(fmt.Sprintf("step %d: step_definition_id is required — steps are created in the step library", i+1))
		}
		// Router: a row that keeps its definition keeps its snapshot (a later
		// router change on the definition does not silently reach the
		// profile); a new or re-pointed row takes the definition's router. An
		// explicit ai_router_id overrides either, and is validated only when it
		// actually changes the router — a resave echoing the current one must
		// not trip a rule the definition itself was never held to.
		if i < len(existing) && s.StepDefinitionID != nil && ptr.Equal(existing[i].StepDefinitionID, s.StepDefinitionID) && stepdef.RunsAI(r.stepType) {
			r.aiRouterID = existing[i].AiRouterID
		}
		if s.AIRouterID != nil {
			if !stepdef.RunsAI(r.stepType) {
				return nil, httputil.NewClientError(fmt.Sprintf("step %d: a human step cannot have an AI router", i+1))
			}
			if !ptr.Equal(r.aiRouterID, s.AIRouterID) {
				if msg := airouter.ValidateStepRouter(ctx, q, tenantID, s.AIRouterID); msg != "" {
					return nil, httputil.NewClientError(fmt.Sprintf("step %d: %s", i+1, msg))
				}
			}
			r.aiRouterID = s.AIRouterID
		}
		if !stepdef.IsAutomated(r.stepType) && s.DefaultAssignee != nil {
			// The assignee picker only offers members of this org; enforce it
			// here too — the assignee is propagated onto real job assignments.
			if _, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: *s.DefaultAssignee, TenantID: tenantID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil, httputil.NewClientError(fmt.Sprintf("step %d: assignee is not a member of this organization", i+1))
				}
				return nil, fmt.Errorf("verify step assignee: %w", err)
			}
			r.assignee = s.DefaultAssignee
		}
		deadline, ok := httputil.ParseDeadline(s.Deadline)
		if !ok {
			return nil, httputil.NewClientError(fmt.Sprintf("step %d: invalid deadline format (use ISO 8601)", i+1))
		}
		if deadline.Valid && stepdef.IsAutomated(r.stepType) {
			return nil, httputil.NewClientError(fmt.Sprintf("step %d: an automated step runs by itself and cannot have a deadline", i+1))
		}
		r.deadline = deadline
		lds, err := resolveLanguageDeadlines(i+1, r.stepType, allowedLangs, s.LanguageDeadlines)
		if err != nil {
			return nil, err
		}
		r.langDeadlines = lds
		out = append(out, r)
	}
	if err := checkPretranslateAlone(out); err != nil {
		return nil, err
	}
	return out, nil
}

// ReplaceResult is what a step replace did: the rows before and after (for
// audit diffs) and the jobs whose freshly appended step was started right
// away because their workflow had already run out of steps — callers that own
// an AI enqueuer kick off the QE+APE run for the ai/hybrid ones.
type ReplaceResult struct {
	Before, After []db.WorkflowStep
	StartedSteps  []StartedStep
}

// StartedStep is a job whose appended step went straight to in_progress.
type StartedStep struct {
	JobID      int64
	Step       db.WorkflowStep
	AssignedTo *int64
}

// sameIdentity reports whether a resolved input keeps an existing row's
// identity — what the step IS and what runs it: the same library definition
// (or the same name / type, which also lets a pre-library row be linked to
// the same-named definition) AND the same AI router. Position fields
// (assignee, deadline) are not identity.
func sameIdentity(e db.WorkflowStep, r resolvedStep) bool {
	if !ptr.Equal(e.AiRouterID, r.aiRouterID) {
		return false
	}
	if e.StepDefinitionID != nil && r.definitionID != nil && *e.StepDefinitionID == *r.definitionID {
		return true
	}
	return e.Name == r.name && e.StepType == r.stepType
}

// ReplaceWorkflowSteps makes profileID's steps equal to steps. Existing rows
// are updated IN PLACE by position so their ids — and the job_step_assignments
// hanging off them — survive an edit that reorders, renames or appends; only
// trailing rows the edit dropped are deleted.
//
// Jobs already running on the profile constrain the edit:
// a position some job has REACHED (its assignment is no longer pending) keeps
// its identity — swapping it for another step or removing it is refused with
// a conflict error. Positions nobody reached may change freely, and appended
// steps are materialised as pending assignments on every non-completed job;
// a job whose workflow had already finished starts the first appended step at
// once so it really does get "another step at the end".
//
// A step whose assignee changed to a real user, or whose deadline inputs
// changed (see deadlineInputsChanged), is pushed onto the jobs already sitting
// on it. Call inside a transaction.
func ReplaceWorkflowSteps(ctx context.Context, q db.Querier, tenantID, profileID int64, steps []StepInput) (ReplaceResult, error) {
	// The edit writes the tasks of the profile's jobs and may start a step on
	// them, so it locks the jobs first, as a step change does, and reads them
	// after.
	if _, err := q.LockProfileJobsForWrite(ctx, db.LockProfileJobsForWriteParams{TenantID: tenantID, SettingsProfileID: profileID}); err != nil {
		return ReplaceResult{}, fmt.Errorf("lock jobs on profile: %w", err)
	}
	existing, err := q.ListSettingsWorkflowSteps(ctx, profileID)
	if err != nil {
		return ReplaceResult{}, fmt.Errorf("list workflow steps: %w", err)
	}
	allowedLangs, isProject, err := profileTargetLanguages(ctx, q, profileID)
	if err != nil {
		return ReplaceResult{}, err
	}
	resolved, err := resolveSteps(ctx, q, tenantID, existing, steps, allowedLangs)
	if err != nil {
		return ReplaceResult{}, err
	}
	if !isProject {
		// A template profile. Scheduling is per project, so a template stores
		// no dates — see profileTargetLanguages.
		for i := range resolved {
			resolved[i].deadline = pgtype.Timestamptz{}
			resolved[i].langDeadlines = nil
		}
	}

	// Safety rule: every existing position a job has reached must keep its
	// identity, and must not be dropped.
	for i, e := range existing {
		changed := i >= len(resolved) || !sameIdentity(e, resolved[i])
		if !changed {
			continue
		}
		n, err := q.CountStartedAssignmentsForStep(ctx, e.ID)
		if err != nil {
			return ReplaceResult{}, fmt.Errorf("count started assignments: %w", err)
		}
		if n > 0 {
			verb := "changed"
			if i >= len(resolved) {
				verb = "removed"
			}
			return ReplaceResult{}, httputil.NewConflictError(fmt.Sprintf(
				"step %d (%q) has already been reached by %d job(s) and cannot be %s — steps may only be added at the end or changed where no job has got to them yet",
				i+1, e.Name, n, verb))
		}
	}

	// Read before the loop rewrites them: the per-language rows each step had.
	stored, err := LoadStepDeadlines(ctx, q, profileID)
	if err != nil {
		return ReplaceResult{}, err
	}

	var appended []db.WorkflowStep
	for i, r := range resolved {
		if i < len(existing) {
			if err := q.UpdateSettingsWorkflowStep(ctx, db.UpdateSettingsWorkflowStepParams{
				ID: existing[i].ID, SettingsProfileID: profileID,
				Name: r.name, StepOrder: int32(i), RoleRequired: r.role,
				DefaultAssignee: r.assignee, Deadline: r.deadline, AiRouterID: r.aiRouterID,
				StepType: r.stepType, StepDefinitionID: r.definitionID,
			}); err != nil {
				return ReplaceResult{}, fmt.Errorf("update workflow step: %w", err)
			}
			if r.assignee != nil && !ptr.Equal(existing[i].DefaultAssignee, r.assignee) {
				if err := q.ReassignInFlightStepAssignments(ctx, db.ReassignInFlightStepAssignmentsParams{
					AssignedTo: r.assignee, WorkflowStepID: existing[i].ID, TenantID: tenantID,
				}); err != nil {
					return ReplaceResult{}, fmt.Errorf("reassign step assignments: %w", err)
				}
			}
			// Only when the date inputs changed: an assignee-only edit, or a
			// patch of another step, leaves a date a PM set on one job alone.
			if deadlineInputsChanged(existing[i], r, stored.Languages(existing[i].ID)) {
				if err := writeLanguageDeadlines(ctx, q, tenantID, existing[i].ID, r.langDeadlines); err != nil {
					return ReplaceResult{}, err
				}
			}
			continue
		}
		row, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: profileID, Name: r.name, StepOrder: int32(i), RoleRequired: r.role,
			DefaultAssignee: r.assignee, Deadline: r.deadline, AiRouterID: r.aiRouterID,
			StepType: r.stepType, StepDefinitionID: r.definitionID,
		})
		if err != nil {
			return ReplaceResult{}, fmt.Errorf("create workflow step: %w", err)
		}
		// A brand-new row has no assignments yet; the refresh inside is a no-op
		// until materializeAppendedSteps creates them just below.
		if err := writeLanguageDeadlines(ctx, q, tenantID, row.ID, r.langDeadlines); err != nil {
			return ReplaceResult{}, err
		}
		appended = append(appended, row)
	}
	for j := len(resolved); j < len(existing); j++ {
		if err := q.DeleteSettingsWorkflowStep(ctx, db.DeleteSettingsWorkflowStepParams{ID: existing[j].ID, SettingsProfileID: profileID}); err != nil {
			return ReplaceResult{}, fmt.Errorf("delete workflow step: %w", err)
		}
	}

	started, err := materializeAppendedSteps(ctx, q, profileID, appended)
	if err != nil {
		return ReplaceResult{}, err
	}
	if err := ensurePretranslateMT(ctx, q, profileID); err != nil {
		return ReplaceResult{}, err
	}

	after, err := q.ListSettingsWorkflowSteps(ctx, profileID)
	if err != nil {
		return ReplaceResult{}, fmt.Errorf("list workflow steps: %w", err)
	}
	return ReplaceResult{Before: existing, After: after, StartedSteps: started}, nil
}

// materializeAppendedSteps gives every non-completed job on the profile a
// pending assignment for each appended step (mirroring what upload does), and
// starts the first appended step on jobs whose workflow had already finished
// (no active step) so the job is not left with a step it can never reach.
func materializeAppendedSteps(ctx context.Context, q db.Querier, profileID int64, appended []db.WorkflowStep) ([]StartedStep, error) {
	if len(appended) == 0 {
		return nil, nil
	}
	jobs, err := q.ListJobsForSettingsProfile(ctx, profileID)
	if err != nil {
		return nil, fmt.Errorf("list jobs on profile: %w", err)
	}
	stepDeadlines, err := LoadStepDeadlines(ctx, q, profileID)
	if err != nil {
		return nil, err
	}
	var started []StartedStep
	for _, job := range jobs {
		if job.Status == "completed" {
			continue
		}
		for k, step := range appended {
			var assignee *int64
			if !stepdef.IsAutomated(step.StepType) {
				assignee = step.DefaultAssignee
			}
			startNow := k == 0 && job.CurrentStepID == nil
			status := "pending"
			if startNow {
				status = "in_progress"
			}
			a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
				JobID: job.ID, WorkflowStepID: step.ID, AssignedTo: assignee, Status: status,
				Deadline: stepDeadlines.For(step, job.TargetLanguage),
			})
			if err != nil {
				return nil, fmt.Errorf("create step assignment for job %d: %w", job.ID, err)
			}
			if startNow {
				if err := q.StartStepAssignment(ctx, a.ID); err != nil {
					return nil, fmt.Errorf("start appended step for job %d: %w", job.ID, err)
				}
				if err := stepround.Open(ctx, q, job.ID, a.ID, stepround.Reset); err != nil {
					return nil, err
				}
				if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &a.ID}); err != nil {
					return nil, fmt.Errorf("set current step for job %d: %w", job.ID, err)
				}
				started = append(started, StartedStep{JobID: job.ID, Step: step, AssignedTo: assignee})
			}
		}
	}
	return started, nil
}

// ReplaceWorkflowStepsTx is ReplaceWorkflowSteps in its own transaction.
func ReplaceWorkflowStepsTx(ctx context.Context, pool database.TxBeginner, tenantID, profileID int64, steps []StepInput) (ReplaceResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return ReplaceResult{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)
	res, err := ReplaceWorkflowSteps(ctx, db.New(tx), tenantID, profileID, steps)
	if err != nil {
		return ReplaceResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReplaceResult{}, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// ApplyWorkflow copies a library workflow's steps onto a profile and stamps
// the provenance pointer. The copy is a snapshot: later edits to the library
// workflow or its definitions do not reach the profile. Jobs already running
// on the profile constrain it exactly like a step edit does (see
// ReplaceWorkflowSteps): reached positions must keep their identity. Call
// inside a transaction.
func ApplyWorkflow(ctx context.Context, q db.Querier, tenantID, workflowID, profileID int64) (ReplaceResult, error) {
	if _, err := q.GetApplicableWorkflow(ctx, db.GetApplicableWorkflowParams{ID: workflowID, TenantID: tenantID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReplaceResult{}, httputil.NewClientError("workflow not found in this tenant")
		}
		return ReplaceResult{}, fmt.Errorf("get workflow: %w", err)
	}
	defSteps, err := q.ListWorkflowDefinitionSteps(ctx, db.ListWorkflowDefinitionStepsParams{WorkflowID: workflowID, TenantID: tenantID})
	if err != nil {
		return ReplaceResult{}, fmt.Errorf("list workflow steps: %w", err)
	}
	inputs := make([]StepInput, 0, len(defSteps))
	for _, s := range defSteps {
		id := s.StepDefinitionID
		inputs = append(inputs, StepInput{StepDefinitionID: &id, DefaultAssignee: s.DefaultAssignee})
	}
	res, err := ReplaceWorkflowSteps(ctx, q, tenantID, profileID, inputs)
	if err != nil {
		return ReplaceResult{}, err
	}
	if err := q.SetSettingsProfileWorkflow(ctx, db.SetSettingsProfileWorkflowParams{ID: profileID, WorkflowID: &workflowID}); err != nil {
		return ReplaceResult{}, fmt.Errorf("stamp profile workflow: %w", err)
	}
	return res, nil
}

// CreateDefaultWorkflowSteps gives a fresh profile the default workflow:
// Translation → Review, both human, linked to the tenant's default step
// definitions (created here when missing). Workflow steps are mandatory, so
// every profile creation path calls this.
func CreateDefaultWorkflowSteps(ctx context.Context, q db.Querier, tenantID, profileID int64) error {
	if err := stepdef.EnsureDefaults(ctx, q, tenantID); err != nil {
		return err
	}
	for i, name := range stepdef.DefaultNames {
		def, err := q.GetStepDefinitionByName(ctx, db.GetStepDefinitionByNameParams{TenantID: tenantID, Name: name})
		if err != nil {
			return fmt.Errorf("get default step definition %q: %w", name, err)
		}
		if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: profileID, Name: def.Name, StepOrder: int32(i), RoleRequired: def.RoleRequired,
			AiRouterID: def.AiRouterID, StepType: def.StepType, StepDefinitionID: &def.ID,
		}); err != nil {
			return fmt.Errorf("create default workflow step %q: %w", name, err)
		}
	}
	return nil
}

// WorkflowName resolves a profile's provenance pointer to a display name,
// including retired library rows. nil when the profile has no pointer or the
// row cannot be read.
func WorkflowName(ctx context.Context, q db.Querier, tenantID int64, workflowID *int64) *string {
	if workflowID == nil {
		return nil
	}
	wf, err := q.GetWorkflowAny(ctx, db.GetWorkflowAnyParams{ID: *workflowID, TenantID: tenantID})
	if err != nil {
		return nil
	}
	return &wf.Name
}
