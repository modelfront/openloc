package airouter

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Transport-free update and delete, shared by the REST handler and the MCP
// tools. Caller mistakes come back as httputil.ClientError, a refusal the
// current state causes as httputil.ConflictError, and an id outside the tenant
// as pgx.ErrNoRows.

// Update replaces a router's name, default and rules. Steps keep pointing at
// the router, so the change reaches every job that starts a step on it from
// now on.
func Update(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, id int64, in Input) (db.AiRouter, []db.AiRouterRule, error) {
	if _, err := ValidateUpdate(ctx, q, tenantID, id, &in); err != nil {
		return db.AiRouter{}, nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return db.AiRouter{}, nil, err
	}
	defer tx.Rollback(ctx)
	txq := db.New(tx)

	rt, err := txq.UpdateAIRouter(ctx, db.UpdateAIRouterParams{
		ID: id, TenantID: tenantID, Name: in.Name, DefaultEngineID: in.DefaultEngineID,
	})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			return db.AiRouter{}, nil, httputil.NewConflictError("an AI router with this name already exists")
		}
		return db.AiRouter{}, nil, err
	}
	if err := txq.DeleteAIRouterRules(ctx, rt.ID); err != nil {
		return db.AiRouter{}, nil, err
	}
	rules, err := writeRules(ctx, txq, rt.ID, in.Rules)
	if err != nil {
		return db.AiRouter{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.AiRouter{}, nil, err
	}
	return rt, rules, nil
}

// ValidateUpdate normalizes and checks an edit exactly as Update does, without
// writing it, and returns the router as it is now.
func ValidateUpdate(ctx context.Context, q db.Querier, tenantID, id int64, in *Input) (db.AiRouter, error) {
	current, err := q.GetAIRouter(ctx, db.GetAIRouterParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.AiRouter{}, err
	}
	if msg := Validate(ctx, q, tenantID, in); msg != "" {
		return db.AiRouter{}, httputil.NewClientError(msg)
	}
	return current, nil
}

// StepsUsing lists the live library step definitions that run a router.
func StepsUsing(ctx context.Context, q db.Querier, tenantID, id int64) ([]db.ListStepDefinitionsUsingAIRouterRow, error) {
	defs, err := q.ListStepDefinitionsUsingAIRouter(ctx, db.ListStepDefinitionsUsingAIRouterParams{
		AiRouterID: &id, TenantID: tenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("list steps using AI router: %w", err)
	}
	return defs, nil
}

// CheckDelete returns the router when it may be deleted: refused with
// httputil.ConflictError while any workflow step — library definition or
// project / template snapshot — still points at it.
func CheckDelete(ctx context.Context, q db.Querier, tenantID, id int64) (db.AiRouter, error) {
	rt, err := q.GetAIRouter(ctx, db.GetAIRouterParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.AiRouter{}, err
	}
	refs, err := q.CountStepRefsForAIRouter(ctx, db.CountStepRefsForAIRouterParams{RouterID: &id, TenantID: tenantID})
	if err != nil {
		return db.AiRouter{}, fmt.Errorf("count step references: %w", err)
	}
	if refs > 0 {
		return db.AiRouter{}, httputil.NewConflictError(fmt.Sprintf("cannot delete: %d workflow step(s) still use this router", refs))
	}
	return rt, nil
}

// Delete soft-deletes a router after CheckDelete.
func Delete(ctx context.Context, q db.Querier, tenantID, id int64) error {
	if _, err := CheckDelete(ctx, q, tenantID, id); err != nil {
		return err
	}
	return q.SoftDeleteAIRouter(ctx, db.SoftDeleteAIRouterParams{ID: id, TenantID: tenantID})
}
