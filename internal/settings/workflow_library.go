package settings

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/stepdef"
)

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
// definitions (created here when missing). Nothing in the product applies it
// by default: seeds, demo data and test fixtures call it.
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
