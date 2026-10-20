package aiengine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Transport-free creation, shared by the REST handler and the MCP tool. An
// engine plus its single provider row is two inserts, so it needs the pool for
// a transaction — a system without its row is not a usable engine.

// Input is an AI system's writable shape: a name, a purpose, and the one
// provider row that runs it.
type Input struct {
	Name    string
	Purpose string
	Row     RowInput
}

// RowInput is the provider row. An extension token needs nothing else; an LLM
// token additionally requires a system prompt and a bad-output action.
type RowInput struct {
	TokenID         int64
	Model           string
	SystemPromptID  *int64
	BadOutputAction *string
	Settings        map[string]any
}

// ValidationError carries a message that is safe to show the caller.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// AsValidation reports whether err is a ValidationError and returns its message.
func AsValidation(err error) (string, bool) {
	var v *ValidationError
	if errors.As(err, &v) {
		return v.Msg, true
	}
	return "", false
}

// Create validates and inserts an AI system with its provider row.
//
// The two writes share a transaction on purpose: an engine row with no provider
// row passes every read path as "configured" and then fails at translate time
// with no row for the language pair.
func Create(ctx context.Context, q db.Querier, pool *pgxpool.Pool, catalog *ai.Catalog, tenantID int64, in Input) (db.AiEngine, []db.AiEngineRow, error) {
	purpose := in.Purpose
	if purpose == "" {
		purpose = PurposeQEAPE
	}
	if purpose != PurposeMT && purpose != PurposeQEAPE {
		return db.AiEngine{}, nil, &ValidationError{Msg: "purpose must be one of: mt, qe_ape"}
	}

	var tokenID *int64
	if in.Row.TokenID != 0 {
		id := in.Row.TokenID
		tokenID = &id
	}
	shaped := engineInput{
		Name:    in.Name,
		Purpose: purpose,
		Rows: []rowInput{{
			SystemPromptID:  in.Row.SystemPromptID,
			BadOutputAction: in.Row.BadOutputAction,
			TokenID:         tokenID,
			Model:           in.Row.Model,
			Settings:        in.Row.Settings,
		}},
	}
	// The same validator the REST handler runs — it also normalizes the row
	// (wildcard languages, trimmed model, cleared prompt fields for extensions).
	//
	// A nil catalog would panic inside model validation, so fall back to one
	// that never fetches — empty model lists, and validation that cannot reject
	// what it cannot see. Same posture as the router's own fallback.
	if catalog == nil {
		catalog = ai.NewCatalog(nil, nil, ai.DefaultModelLimit)
	}
	h := &Handler{queries: q, catalog: catalog}
	if msg := h.validate(ctx, tenantID, &shaped, purpose); msg != "" {
		return db.AiEngine{}, nil, &ValidationError{Msg: msg}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return db.AiEngine{}, nil, err
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)

	engine, err := qtx.CreateAIEngine(ctx, db.CreateAIEngineParams{
		TenantID: tenantID, Name: shaped.Name, Purpose: purpose,
	})
	if err != nil {
		return db.AiEngine{}, nil, err
	}
	rows, err := insertRows(ctx, qtx, engine.ID, shaped.Rows)
	if err != nil {
		return db.AiEngine{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.AiEngine{}, nil, err
	}
	return engine, rows, nil
}

// UpdateInput replaces an engine's name and provider row. Enabled nil keeps
// the current state, so an edit to the row cannot silently switch an engine
// back on. The purpose is not editable.
type UpdateInput struct {
	Name    string
	Enabled *bool
	Row     RowInput
}

// Update validates and replaces an engine's name, state and provider row.
func Update(ctx context.Context, q db.Querier, pool *pgxpool.Pool, catalog *ai.Catalog, tenantID, id int64, in UpdateInput) (db.AiEngine, []db.AiEngineRow, error) {
	return update(ctx, q, pool, catalog, tenantID, id, shapeUpdate(in))
}

func shapeUpdate(in UpdateInput) engineInput {
	var tokenID *int64
	if in.Row.TokenID != 0 {
		t := in.Row.TokenID
		tokenID = &t
	}
	return engineInput{
		Name:    in.Name,
		Enabled: in.Enabled,
		Rows: []rowInput{{
			SystemPromptID:  in.Row.SystemPromptID,
			BadOutputAction: in.Row.BadOutputAction,
			TokenID:         tokenID,
			Model:           in.Row.Model,
			Settings:        in.Row.Settings,
		}},
	}
}

// update is the shared body of the REST PATCH and Update. The engine row and
// its provider rows are replaced in one transaction.
func update(ctx context.Context, q db.Querier, pool *pgxpool.Pool, catalog *ai.Catalog, tenantID, id int64, in engineInput) (db.AiEngine, []db.AiEngineRow, error) {
	existing, err := validateUpdate(ctx, q, catalog, tenantID, id, &in)
	if err != nil {
		return db.AiEngine{}, nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return db.AiEngine{}, nil, err
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)

	enabled := existing.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	engine, err := qtx.UpdateAIEngine(ctx, db.UpdateAIEngineParams{
		ID: id, TenantID: tenantID, Name: in.Name, Enabled: enabled,
	})
	if err != nil {
		return db.AiEngine{}, nil, err
	}
	if err := qtx.DeleteAIEngineRows(ctx, id); err != nil {
		return db.AiEngine{}, nil, err
	}
	rows, err := insertRows(ctx, qtx, id, in.Rows)
	if err != nil {
		return db.AiEngine{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.AiEngine{}, nil, err
	}
	return engine, rows, nil
}

// ValidateUpdate checks an edit exactly as Update does, without writing it,
// and returns the engine as it is now.
func ValidateUpdate(ctx context.Context, q db.Querier, catalog *ai.Catalog, tenantID, id int64, in UpdateInput) (db.AiEngine, error) {
	shaped := shapeUpdate(in)
	return validateUpdate(ctx, q, catalog, tenantID, id, &shaped)
}

func validateUpdate(ctx context.Context, q db.Querier, catalog *ai.Catalog, tenantID, id int64, in *engineInput) (db.AiEngine, error) {
	existing, err := q.GetAIEngine(ctx, db.GetAIEngineParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.AiEngine{}, err
	}
	// Flipping the role of an engine a router already routes to would silently
	// break that router.
	if p := strings.TrimSpace(in.Purpose); p != "" && p != existing.Purpose {
		return db.AiEngine{}, &ValidationError{Msg: "an engine's purpose cannot be changed — create a new engine instead"}
	}
	if catalog == nil {
		catalog = ai.NewCatalog(nil, nil, ai.DefaultModelLimit)
	}
	h := &Handler{queries: q, catalog: catalog}
	if msg := h.validate(ctx, tenantID, in, existing.Purpose); msg != "" {
		return db.AiEngine{}, &ValidationError{Msg: msg}
	}
	return existing, nil
}

// CheckDelete returns the engine when it may be deleted: refused with
// httputil.ConflictError while any live router reaches it, as default or in a
// rule — AI routers for a QE+APE engine, MT routers for a translation engine.
// Steps and projects reach engines only through routers.
func CheckDelete(ctx context.Context, q db.Querier, tenantID, id int64) (db.AiEngine, error) {
	engine, err := q.GetAIEngine(ctx, db.GetAIEngineParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.AiEngine{}, err
	}
	refs, err := q.CountAIRouterRefsForAIEngine(ctx, db.CountAIRouterRefsForAIEngineParams{EngineID: &id, TenantID: tenantID})
	if err != nil {
		return db.AiEngine{}, fmt.Errorf("count router references: %w", err)
	}
	if refs > 0 {
		return db.AiEngine{}, httputil.NewConflictError(fmt.Sprintf("cannot delete: %d router(s) still use this engine", refs))
	}
	return engine, nil
}

// Delete soft-deletes an engine after CheckDelete, in one transaction with
// removing its provider rows: a deleted engine keeps no rows, so it holds no
// reference to a system prompt or a key.
func Delete(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, id int64) error {
	if _, err := CheckDelete(ctx, q, tenantID, id); err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	txq := db.New(tx)
	if err := txq.SoftDeleteAIEngine(ctx, db.SoftDeleteAIEngineParams{ID: id, TenantID: tenantID}); err != nil {
		return err
	}
	if err := txq.DeleteAIEngineRows(ctx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
