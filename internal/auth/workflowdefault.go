package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/systemworkflow"
)

// defaultWorkflowName + defaultWorkflowStepNames are the "batteries included"
// library a new org starts with, so its Workflows and Steps pages are never
// empty. The step names mirror stepdef.DefaultNames
// ("Translation" / "Review") — kept as a local
// copy because stepdef imports this package, so auth cannot import it back.
const defaultWorkflowName = "Default"

var defaultWorkflowStepNames = []string{"Translation", "Review"}

// ensureDefaultWorkflow gives a freshly-provisioned org its workflows: the
// built-in Pre-translation workflow, and the default human step
// definitions with a "Default" workflow composed of them. The Default steps
// and workflow are ordinary library rows — editable and deletable like any
// other. Runs in the provisioning transaction alongside the other per-org
// defaults (mtdefaults.EnsureForTenant). Idempotent: skips the Default
// workflow when a live library workflow by that name already exists.
func ensureDefaultWorkflow(ctx context.Context, q db.Querier, tenantID int64) error {
	if err := systemworkflow.EnsurePretranslate(ctx, q, tenantID); err != nil {
		return err
	}
	for _, name := range defaultWorkflowStepNames {
		if err := q.EnsureStepDefinition(ctx, db.EnsureStepDefinitionParams{TenantID: tenantID, Name: name}); err != nil {
			return fmt.Errorf("ensure step definition %q: %w", name, err)
		}
	}
	if _, err := q.GetWorkflowByName(ctx, db.GetWorkflowByNameParams{TenantID: tenantID, Name: defaultWorkflowName}); err == nil {
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check default workflow: %w", err)
	}
	wf, err := q.CreateWorkflow(ctx, db.CreateWorkflowParams{TenantID: tenantID, Name: defaultWorkflowName})
	if err != nil {
		return fmt.Errorf("create default workflow: %w", err)
	}
	for i, name := range defaultWorkflowStepNames {
		def, err := q.GetStepDefinitionByName(ctx, db.GetStepDefinitionByNameParams{TenantID: tenantID, Name: name})
		if err != nil {
			return fmt.Errorf("resolve default step %q: %w", name, err)
		}
		if _, err := q.CreateWorkflowDefinitionStep(ctx, db.CreateWorkflowDefinitionStepParams{
			WorkflowID: wf.ID, StepDefinitionID: def.ID, StepOrder: int32(i),
		}); err != nil {
			return fmt.Errorf("compose default workflow step %q: %w", name, err)
		}
	}
	return nil
}
