package stepdef

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Transport-free create, update and delete, shared by the REST handler, the
// customer API and the MCP tools. Caller mistakes come back as httputil.ClientError, a refusal the
// current state causes as httputil.ConflictError, and an id outside the tenant
// as pgx.ErrNoRows.

// Create adds a step definition to the tenant's library.
func Create(ctx context.Context, q db.Querier, tenantID int64, in Input) (db.StepDefinition, error) {
	if msg := Validate(ctx, q, tenantID, &in); msg != "" {
		return db.StepDefinition{}, httputil.NewClientError(msg)
	}
	sd, err := q.CreateStepDefinition(ctx, db.CreateStepDefinitionParams{
		TenantID: tenantID, Name: in.Name, StepType: in.StepType,
		RoleRequired: DefaultRole, AiRouterID: in.AIRouterID,
	})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			return db.StepDefinition{}, httputil.NewConflictError("a step with this name already exists")
		}
		return db.StepDefinition{}, fmt.Errorf("create step definition: %w", err)
	}
	return sd, nil
}

// Update replaces a definition's identity fields. Profile step rows that
// snapshotted the old values are untouched by design — a library edit never
// rewrites a running project.
func Update(ctx context.Context, q db.Querier, tenantID, id int64, in Input) (db.StepDefinition, error) {
	if _, err := ValidateUpdate(ctx, q, tenantID, id, &in); err != nil {
		return db.StepDefinition{}, err
	}
	sd, err := q.UpdateStepDefinition(ctx, db.UpdateStepDefinitionParams{
		ID: id, TenantID: tenantID, Name: in.Name, StepType: in.StepType,
		RoleRequired: DefaultRole, AiRouterID: in.AIRouterID,
	})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			return db.StepDefinition{}, httputil.NewConflictError("a step with this name already exists")
		}
		return db.StepDefinition{}, err
	}
	return sd, nil
}

// ValidateUpdate normalizes and checks an edit exactly as Update does, without
// writing it, and returns the definition as it is now.
func ValidateUpdate(ctx context.Context, q db.Querier, tenantID, id int64, in *Input) (db.StepDefinition, error) {
	sd, err := q.GetStepDefinition(ctx, db.GetStepDefinitionParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.StepDefinition{}, err
	}
	if msg := Validate(ctx, q, tenantID, in); msg != "" {
		return db.StepDefinition{}, httputil.NewClientError(msg)
	}
	return sd, nil
}

// CheckDelete returns the definition when it may be deleted: refused with
// httputil.ConflictError while a live library workflow still composes it.
// Delete runs the same check under a row lock.
func CheckDelete(ctx context.Context, q db.Querier, tenantID, id int64) (db.StepDefinition, error) {
	sd, err := q.GetStepDefinition(ctx, db.GetStepDefinitionParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.StepDefinition{}, err
	}
	return sd, checkUnused(ctx, q, tenantID, id)
}

func checkUnused(ctx context.Context, q db.Querier, tenantID, id int64) error {
	using, err := q.ListWorkflowsUsingStepDefinition(ctx, db.ListWorkflowsUsingStepDefinitionParams{
		StepDefinitionID: id, TenantID: tenantID,
	})
	if err != nil {
		return fmt.Errorf("list workflows using step definition: %w", err)
	}
	if len(using) > 0 {
		return httputil.NewConflictError(fmt.Sprintf(
			"this step is used by %d workflow(s): %s. Remove it from them first.",
			len(using), WorkflowNames(using)))
	}
	return nil
}

// Delete soft-deletes a definition, refused while a live library workflow
// still composes it.
//
// There is no FK backstop: the delete only sets deleted_at, and the compose
// FK is CASCADE for tenant teardown. So the usage check and the delete run in
// one transaction that row-locks the definition (LockStepDefinition); the
// compose path locks the same row, so a workflow cannot start using the
// definition between the check and the delete. Profile step rows keep their
// snapshot and provenance pointer, so projects are unaffected.
func Delete(ctx context.Context, pool *pgxpool.Pool, tenantID, id int64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	txq := db.New(tx)

	if _, err := txq.LockStepDefinition(ctx, db.LockStepDefinitionParams{ID: id, TenantID: tenantID}); err != nil {
		return err
	}
	if err := checkUnused(ctx, txq, tenantID, id); err != nil {
		return err
	}
	if err := txq.SoftDeleteStepDefinition(ctx, db.SoftDeleteStepDefinitionParams{ID: id, TenantID: tenantID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WorkflowNames renders the workflows that compose a definition as a quoted,
// comma-separated list.
func WorkflowNames(using []db.ListWorkflowsUsingStepDefinitionRow) string {
	names := make([]string, 0, len(using))
	for _, u := range using {
		names = append(names, fmt.Sprintf("%q", u.Name))
	}
	return strings.Join(names, ", ")
}
