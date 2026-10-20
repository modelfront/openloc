package workflowlib

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Transport-free create, update and delete, shared by the REST handler, the
// customer API and the MCP tools. Caller mistakes come back as httputil.ClientError, a name clash as
// httputil.ConflictError, and an id outside the tenant as pgx.ErrNoRows.

// Detail is a workflow with its steps in order. Steps is never nil.
type Detail struct {
	db.Workflow
	Steps []db.ListWorkflowDefinitionStepsRow `json:"steps"`
}

// LoadDetail reads the steps of wf, a workflow of the tenant.
func LoadDetail(ctx context.Context, q db.Querier, tenantID int64, wf db.Workflow) (Detail, error) {
	steps, err := q.ListWorkflowDefinitionSteps(ctx, db.ListWorkflowDefinitionStepsParams{WorkflowID: wf.ID, TenantID: tenantID})
	if err != nil {
		return Detail{}, fmt.Errorf("list workflow steps: %w", err)
	}
	if steps == nil {
		steps = []db.ListWorkflowDefinitionStepsRow{}
	}
	return Detail{Workflow: wf, Steps: steps}, nil
}

// CreateValidated validates a new workflow, creates it with its steps and
// reads it back, all in one transaction, so a failed read saves nothing.
func CreateValidated(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID int64, in Input) (Detail, error) {
	if err := Validate(ctx, q, tenantID, &in); err != nil {
		return Detail{}, asClientError(err)
	}
	var d Detail
	err := database.InTx(ctx, pool, func(_ pgx.Tx, txq db.Querier) error {
		wf, err := Create(ctx, txq, tenantID, in)
		if err != nil {
			if httputil.IsUniqueViolation(err) {
				return httputil.NewConflictError("a workflow with this name already exists")
			}
			return asClientError(err)
		}
		d, err = LoadDetail(ctx, txq, tenantID, wf)
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	return d, nil
}

// Update renames a workflow, replaces its step list and reads it back, all in
// one transaction, so a failed read changes nothing. Profiles that copied the
// workflow keep their own steps.
func Update(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, id int64, in Input) (Detail, error) {
	if _, err := ValidateUpdate(ctx, q, tenantID, id, &in); err != nil {
		return Detail{}, err
	}
	var d Detail
	err := database.InTx(ctx, pool, func(_ pgx.Tx, txq db.Querier) error {
		if _, err := txq.UpdateWorkflowName(ctx, db.UpdateWorkflowNameParams{ID: id, TenantID: tenantID, Name: in.Name}); err != nil {
			if httputil.IsUniqueViolation(err) {
				return httputil.NewConflictError("a workflow with this name already exists")
			}
			return err
		}
		if err := ReplaceSteps(ctx, txq, tenantID, id, in.Steps); err != nil {
			return asClientError(err)
		}
		wf, err := txq.GetWorkflow(ctx, db.GetWorkflowParams{ID: id, TenantID: tenantID})
		if err != nil {
			return err
		}
		d, err = LoadDetail(ctx, txq, tenantID, wf)
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	return d, nil
}

// ValidateUpdate normalizes and checks an edit exactly as Update does, without
// writing it, and returns the workflow as it is now.
func ValidateUpdate(ctx context.Context, q db.Querier, tenantID, id int64, in *Input) (db.Workflow, error) {
	wf, err := q.GetWorkflow(ctx, db.GetWorkflowParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.Workflow{}, err
	}
	if err := Validate(ctx, q, tenantID, in); err != nil {
		return db.Workflow{}, asClientError(err)
	}
	return wf, nil
}

// Delete soft-deletes a workflow. Profiles that copied it keep their steps;
// their provenance pointer resolves to the retired row for display.
func Delete(ctx context.Context, q db.Querier, tenantID, id int64) error {
	if _, err := q.GetWorkflow(ctx, db.GetWorkflowParams{ID: id, TenantID: tenantID}); err != nil {
		return err
	}
	return q.SoftDeleteWorkflow(ctx, db.SoftDeleteWorkflowParams{ID: id, TenantID: tenantID})
}

// asClientError maps a ValidationError onto httputil.ClientError and leaves
// every other error alone.
func asClientError(err error) error {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return httputil.NewClientError(ve.Msg)
	}
	return err
}
