// Package systemworkflow owns the built-in "Pre-translation" workflow
// every tenant has: one automated Pre-translation step that runs the
// pre-translation pipeline and completes itself. The rows carry a system_key,
// which keeps them out of the step and workflow libraries — nobody can list,
// edit, compose or delete them there. Projects and templates pick the workflow
// with the Workflow card's Raw MT mode.
//
// It is a leaf package (database only) so tenant provisioning in auth, the
// seed and the tests can all ensure the rows.
package systemworkflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
)

const (
	// StepType is the step type only the built-in Pre-translation step has.
	StepType = "pretranslate"

	StepKey      = "pretranslate"
	StepName     = "Pre-translation"
	WorkflowKey  = "pretranslate_only"
	WorkflowName = "Pre-translation"
)

// EnsurePretranslate gives a tenant the built-in step definition and the
// single-step workflow composed of it. Idempotent.
func EnsurePretranslate(ctx context.Context, q db.Querier, tenantID int64) error {
	if err := q.EnsureSystemStepDefinition(ctx, db.EnsureSystemStepDefinitionParams{
		TenantID: tenantID, Name: StepName, StepType: StepType, SystemKey: ptr.To(StepKey),
	}); err != nil {
		return fmt.Errorf("ensure pre-translation step: %w", err)
	}
	if err := q.EnsureSystemWorkflow(ctx, db.EnsureSystemWorkflowParams{
		TenantID: tenantID, Name: WorkflowName, SystemKey: ptr.To(WorkflowKey),
	}); err != nil {
		return fmt.Errorf("ensure pre-translation workflow: %w", err)
	}
	wf, err := Pretranslate(ctx, q, tenantID)
	if err != nil {
		return err
	}
	steps, err := q.ListWorkflowDefinitionSteps(ctx, db.ListWorkflowDefinitionStepsParams{WorkflowID: wf.ID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("list pre-translation workflow steps: %w", err)
	}
	if len(steps) > 0 {
		return nil
	}
	def, err := q.GetSystemStepDefinition(ctx, db.GetSystemStepDefinitionParams{TenantID: tenantID, SystemKey: ptr.To(StepKey)})
	if err != nil {
		return fmt.Errorf("resolve pre-translation step: %w", err)
	}
	if _, err := q.CreateWorkflowDefinitionStep(ctx, db.CreateWorkflowDefinitionStepParams{
		WorkflowID: wf.ID, StepDefinitionID: def.ID, StepOrder: 0,
	}); err != nil {
		return fmt.Errorf("compose pre-translation workflow: %w", err)
	}
	return nil
}

// Pretranslate returns the tenant's built-in Pre-translation workflow.
// pgx.ErrNoRows when the tenant has none.
func Pretranslate(ctx context.Context, q db.Querier, tenantID int64) (db.Workflow, error) {
	wf, err := q.GetSystemWorkflow(ctx, db.GetSystemWorkflowParams{TenantID: tenantID, SystemKey: ptr.To(WorkflowKey)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.Workflow{}, fmt.Errorf("get pre-translation workflow: %w", err)
	}
	return wf, err
}
