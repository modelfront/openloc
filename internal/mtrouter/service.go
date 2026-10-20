package mtrouter

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/mtengine"
)

// Transport-free update and delete, shared by the REST handler and the MCP
// tools. Caller mistakes come back as httputil.ClientError, a refusal the
// current state causes as httputil.ConflictError, and an id outside the tenant
// as pgx.ErrNoRows.

// Update replaces a router's name, default and rules. Profiles keep pointing
// at the router, so the change reaches every job translated from now on.
func Update(ctx context.Context, q db.Querier, pool *pgxpool.Pool, dec mtengine.Decryptor, tenantID, id int64, in Input) (db.MtRouter, []db.MtRouterRule, error) {
	if _, err := ValidateUpdate(ctx, q, dec, tenantID, id, &in); err != nil {
		return db.MtRouter{}, nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return db.MtRouter{}, nil, err
	}
	defer tx.Rollback(ctx)
	txq := db.New(tx)

	rt, err := txq.UpdateMTRouter(ctx, db.UpdateMTRouterParams{
		ID: id, TenantID: tenantID, Name: in.Name,
		DefaultMtEngineID: in.Default.MTEngineID, DefaultAiEngineID: in.Default.AIEngineID,
	})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			return db.MtRouter{}, nil, httputil.NewConflictError("an MT router with this name already exists")
		}
		return db.MtRouter{}, nil, err
	}
	if err := txq.DeleteMTRouterRules(ctx, rt.ID); err != nil {
		return db.MtRouter{}, nil, err
	}
	rules, err := writeRules(ctx, txq, rt.ID, in.Rules)
	if err != nil {
		return db.MtRouter{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.MtRouter{}, nil, err
	}
	return rt, rules, nil
}

// ValidateUpdate normalizes and checks an edit exactly as Update does, without
// writing it, and returns the router as it is now.
func ValidateUpdate(ctx context.Context, q db.Querier, dec mtengine.Decryptor, tenantID, id int64, in *Input) (db.MtRouter, error) {
	rt, err := q.GetMTRouter(ctx, db.GetMTRouterParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.MtRouter{}, err
	}
	if msg := Validate(ctx, q, dec, tenantID, in); msg != "" {
		return db.MtRouter{}, httputil.NewClientError(msg)
	}
	return rt, nil
}

// CheckDelete returns the router when it may be deleted: refused with
// httputil.ConflictError while any project or template still points at it.
func CheckDelete(ctx context.Context, q db.Querier, tenantID, id int64) (db.MtRouter, error) {
	rt, err := q.GetMTRouter(ctx, db.GetMTRouterParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.MtRouter{}, err
	}
	refs, err := q.CountProfileRefsForMTRouter(ctx, db.CountProfileRefsForMTRouterParams{RouterID: &id, TenantID: tenantID})
	if err != nil {
		return db.MtRouter{}, fmt.Errorf("count profile references: %w", err)
	}
	if refs > 0 {
		return db.MtRouter{}, httputil.NewConflictError(fmt.Sprintf("cannot delete: %d project(s)/template(s) still use this MT router", refs))
	}
	return rt, nil
}

// Delete soft-deletes a router after CheckDelete.
func Delete(ctx context.Context, q db.Querier, tenantID, id int64) error {
	if _, err := CheckDelete(ctx, q, tenantID, id); err != nil {
		return err
	}
	return q.SoftDeleteMTRouter(ctx, db.SoftDeleteMTRouterParams{ID: id, TenantID: tenantID})
}
