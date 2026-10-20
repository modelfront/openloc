// Package workflowlib is the workflow library: named,
// ordered lists of step definitions that projects and templates copy from.
// Editing a library workflow never touches the profiles that copied it.
//
// Access scope: every route is manager-gated (owner/admin/project_manager);
// api.go wraps the group in RequireManager.
package workflowlib

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/stepdef"
	"openloc/internal/workflow"
)

const maxNameLen = 200

// StepInput is one position in a library workflow.
type StepInput struct {
	StepDefinitionID int64  `json:"step_definition_id"`
	DefaultAssignee  *int64 `json:"default_assignee"`
}

// Input is the writable shape of a workflow (create + update).
type Input struct {
	Name  string      `json:"name"`
	Steps []StepInput `json:"steps"`
}

// ValidationError is a client-safe rejection of an Input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Validate normalizes in and rejects: empty/long name, no steps, more than
// workflow.MaxWorkflowSteps, a step definition that is not a live row of this
// tenant, or an assignee who is not a member of this tenant.
func Validate(ctx context.Context, q db.Querier, tenantID int64, in *Input) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return invalid("name is required")
	}
	if len(in.Name) > maxNameLen {
		return invalid("name too long (max %d characters)", maxNameLen)
	}
	if len(in.Steps) == 0 {
		return invalid("a workflow needs at least one step")
	}
	if len(in.Steps) > workflow.MaxWorkflowSteps {
		return invalid("a workflow can have at most %d steps", workflow.MaxWorkflowSteps)
	}
	for i, s := range in.Steps {
		if _, err := q.GetStepDefinition(ctx, db.GetStepDefinitionParams{ID: s.StepDefinitionID, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return invalid("step %d: step definition %d not found", i+1, s.StepDefinitionID)
			}
			return fmt.Errorf("verify step definition: %w", err)
		}
		if s.DefaultAssignee != nil {
			if _, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: *s.DefaultAssignee, TenantID: tenantID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return invalid("step %d: assignee is not a member of this organization", i+1)
				}
				return fmt.Errorf("verify step assignee: %w", err)
			}
		}
	}
	return nil
}

// Create inserts a workflow and its steps. Caller validates first and runs
// this inside a transaction.
func Create(ctx context.Context, q db.Querier, tenantID int64, in Input) (db.Workflow, error) {
	wf, err := q.CreateWorkflow(ctx, db.CreateWorkflowParams{TenantID: tenantID, Name: in.Name})
	if err != nil {
		return db.Workflow{}, err
	}
	if err := writeSteps(ctx, q, tenantID, wf.ID, in.Steps); err != nil {
		return db.Workflow{}, err
	}
	return wf, nil
}

// ReplaceSteps swaps a workflow's step list. Nothing references
// workflow_definition_steps rows (profiles snapshot, they don't link), so a
// wholesale delete + recreate loses nothing. Caller runs it in a transaction.
func ReplaceSteps(ctx context.Context, q db.Querier, tenantID, workflowID int64, steps []StepInput) error {
	if err := q.DeleteWorkflowDefinitionSteps(ctx, workflowID); err != nil {
		return fmt.Errorf("clear workflow steps: %w", err)
	}
	if err := writeSteps(ctx, q, tenantID, workflowID, steps); err != nil {
		return err
	}
	return q.TouchWorkflow(ctx, db.TouchWorkflowParams{ID: workflowID, TenantID: tenantID})
}

func writeSteps(ctx context.Context, q db.Querier, tenantID, workflowID int64, steps []StepInput) error {
	for i, s := range steps {
		// Row-lock the definition (deleted_at IS NULL) so a concurrent
		// stepdef delete serializes with this compose. A soft-deleted row
		// still satisfies the FK, so without this a workflow could pick up a
		// definition that is being deleted; the lock makes one wait for the
		// other's commit and re-check.
		if _, err := q.LockStepDefinition(ctx, db.LockStepDefinitionParams{ID: s.StepDefinitionID, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return invalid("step %d: step definition %d is no longer available", i+1, s.StepDefinitionID)
			}
			return fmt.Errorf("lock step definition: %w", err)
		}
		if _, err := q.CreateWorkflowDefinitionStep(ctx, db.CreateWorkflowDefinitionStepParams{
			WorkflowID: workflowID, StepDefinitionID: s.StepDefinitionID,
			StepOrder: int32(i), DefaultAssignee: s.DefaultAssignee,
		}); err != nil {
			return fmt.Errorf("create workflow step %d: %w", i, err)
		}
	}
	return nil
}

// CreateFromSteps promotes a profile's step rows into a new library workflow
// ("Save as workflow"). Each row must carry library provenance; a pre-library
// row without one is rejected with a client error naming it, because the
// library cannot invent a definition the PM never created. Assignees travel
// with the steps; deadlines do not (per-project scheduling). Caller validates
// nothing else and runs this inside a transaction.
func CreateFromSteps(ctx context.Context, q db.Querier, tenantID int64, name string, steps []db.WorkflowStep) (db.Workflow, error) {
	in := Input{Name: name, Steps: make([]StepInput, 0, len(steps))}
	for _, s := range steps {
		if s.StepType == stepdef.TypePretranslate {
			return db.Workflow{}, httputil.NewClientError("Pre-translation is built in and cannot be saved as a workflow")
		}
		if s.StepDefinitionID == nil {
			return db.Workflow{}, httputil.NewClientError(fmt.Sprintf("step %q is not a library step — replace it with one before saving this workflow", s.Name))
		}
		in.Steps = append(in.Steps, StepInput{StepDefinitionID: *s.StepDefinitionID, DefaultAssignee: s.DefaultAssignee})
	}
	if err := Validate(ctx, q, tenantID, &in); err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return db.Workflow{}, httputil.NewClientError(ve.Msg)
		}
		return db.Workflow{}, err
	}
	wf, err := Create(ctx, q, tenantID, in)
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return db.Workflow{}, httputil.NewClientError(ve.Msg)
		}
		return db.Workflow{}, err
	}
	return wf, nil
}
