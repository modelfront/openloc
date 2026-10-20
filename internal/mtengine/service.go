package mtengine

import (
	"context"
	"fmt"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Transport-free create, update and delete, shared by the REST handler and the
// MCP tools. Caller mistakes come back as httputil.ClientError, an in-use
// refusal as httputil.ConflictError, and an id outside the tenant as
// pgx.ErrNoRows.

// CreateInput is a new engine. Enabled nil means true: creating an engine
// implies you want to use it.
type CreateInput struct {
	Name       string
	EngineType string
	Enabled    *bool
	// TokenID is the AI settings key the engine spends. Required for a provider
	// type, refused for a deployment-supplied one.
	TokenID  *int64
	Settings map[string]any
}

// Create validates and inserts an engine.
//
// The endpoint is never an input. A key-backed type reaches its provider
// through the key's connection, so its own column stays nil. A
// deployment-supplied type takes the endpoint this deployment supplies, and a
// type the deployment does not supply cannot be built at all.
func Create(ctx context.Context, q db.Querier, tenantID int64, in CreateInput) (db.MtEngine, error) {
	name := strings.TrimSpace(in.Name)
	engineType := strings.TrimSpace(in.EngineType)
	if name == "" || engineType == "" {
		return db.MtEngine{}, httputil.NewClientError("name and engine_type are required")
	}
	if !ValidType(engineType) {
		return db.MtEngine{}, httputil.NewClientError("engine_type must be one of: mock, libretranslate, deepl, google")
	}
	var apiURL *string
	if !NeedsToken(engineType) {
		supplied, isSupplied := suppliedURL(engineType)
		if !isSupplied {
			return db.MtEngine{}, httputil.NewClientError(DisplayType(engineType) + " is not available on this deployment")
		}
		apiURL = supplied
	}
	if msg := checkToken(ctx, q, tenantID, engineType, in.TokenID); msg != "" {
		return db.MtEngine{}, httputil.NewClientError(msg)
	}
	if msg := ValidateSettings(engineType, in.Settings); msg != "" {
		return db.MtEngine{}, httputil.NewClientError(msg)
	}
	settings, err := encodeSettings(in.Settings)
	if err != nil {
		return db.MtEngine{}, httputil.NewClientError("settings must be a JSON object")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	// No api_key: a provider engine authenticates with the key it names. The
	// column is only set on deployment rows.
	return q.CreateMTEngine(ctx, db.CreateMTEngineParams{
		Name:       name,
		EngineType: engineType,
		ApiKey:     nil,
		ApiUrl:     apiURL,
		Enabled:    enabled,
		TenantID:   tenantID,
		TokenID:    in.TokenID,
		Settings:   settings,
	})
}

// UpdateInput is a partial edit: a nil field keeps the stored value. Settings,
// when non-nil, replaces the stored options wholesale.
type UpdateInput struct {
	Name       *string
	EngineType *string
	Enabled    *bool
	TokenID    *int64
	Settings   map[string]any
}

// Update applies a partial edit.
//
// The endpoint follows the TYPE and changes only on a type change, to the one
// this deployment supplies for the new type: a token-less
// engine is where the pipeline posts the tenant's segments, so it can never
// come from the caller. Key and options are checked against the type the row
// will have after the edit.
func Update(ctx context.Context, q db.Querier, tenantID, id int64, in UpdateInput) (db.MtEngine, error) {
	params, err := PrepareUpdate(ctx, q, tenantID, id, in)
	if err != nil {
		return db.MtEngine{}, err
	}
	return q.UpdateMTEngine(ctx, params)
}

// PrepareUpdate validates an edit and returns the row it would write, without
// writing it.
func PrepareUpdate(ctx context.Context, q db.Querier, tenantID, id int64, in UpdateInput) (db.UpdateMTEngineParams, error) {
	fail := func(msg string) (db.UpdateMTEngineParams, error) {
		return db.UpdateMTEngineParams{}, httputil.NewClientError(msg)
	}
	current, err := q.GetMTEngine(ctx, db.GetMTEngineParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.UpdateMTEngineParams{}, err
	}

	name := current.Name
	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		if trimmed == "" {
			return fail("name must not be empty")
		}
		name = trimmed
	}
	engineType := current.EngineType
	if in.EngineType != nil {
		if !ValidType(*in.EngineType) {
			return fail("engine_type must be one of: mock, libretranslate, deepl, google")
		}
		engineType = *in.EngineType
	}

	// A row keeps the endpoint it was created with while its type stands, so a
	// later LIBRETRANSLATE_URL neither heals nor moves it.
	apiURL := current.ApiUrl
	if engineType != current.EngineType {
		apiURL = nil
		if !NeedsToken(engineType) {
			supplied, isSupplied := suppliedURL(engineType)
			if !isSupplied {
				return fail(DisplayType(engineType) + " is not available on this deployment")
			}
			apiURL = supplied
		}
	}

	tokenID := current.TokenID
	if in.TokenID != nil {
		tokenID = in.TokenID
	}
	if msg := checkToken(ctx, q, tenantID, engineType, tokenID); msg != "" {
		return fail(msg)
	}
	settingsMap := DecodeSettings(current.Settings)
	if in.Settings != nil {
		settingsMap = in.Settings
	}
	if msg := ValidateSettings(engineType, settingsMap); msg != "" {
		return fail(msg)
	}
	settings, err := encodeSettings(settingsMap)
	if err != nil {
		return fail("settings must be a JSON object")
	}
	enabled := current.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	return db.UpdateMTEngineParams{
		ID:         id,
		Name:       name,
		EngineType: engineType,
		ApiKey:     current.ApiKey,
		ApiUrl:     apiURL,
		Enabled:    enabled,
		TenantID:   tenantID,
		TokenID:    tokenID,
		Settings:   settings,
	}, nil
}

// CheckDelete returns the engine when it may be deleted: refused with
// httputil.ConflictError while any live MT router still routes to it, since
// the router FKs cascade and deleting anyway would silently drop routes.
func CheckDelete(ctx context.Context, q db.Querier, tenantID, id int64) (db.MtEngine, error) {
	engine, err := q.GetMTEngine(ctx, db.GetMTEngineParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.MtEngine{}, err
	}
	refs, err := q.CountMTEngineReferences(ctx, id)
	if err != nil {
		return db.MtEngine{}, fmt.Errorf("count MT engine references: %w", err)
	}
	if refs > 0 {
		return db.MtEngine{}, httputil.NewConflictError(fmt.Sprintf("cannot delete: %d MT router(s) still use this MT engine", refs))
	}
	return engine, nil
}

// Delete soft-deletes an engine after CheckDelete.
func Delete(ctx context.Context, q db.Querier, tenantID, id int64) error {
	if _, err := CheckDelete(ctx, q, tenantID, id); err != nil {
		return err
	}
	return q.SoftDeleteMTEngine(ctx, db.SoftDeleteMTEngineParams{ID: id, TenantID: tenantID})
}

// checkToken validates the key an engine wants to spend: present when the type
// needs one, absent when it does not, owned by this tenant, and issued by the
// provider the engine calls. A key from the wrong provider would pass every
// other check and then fail every translation. Returns a client-safe message,
// "" when fine.
func checkToken(ctx context.Context, q db.Querier, tenantID int64, engineType string, tokenID *int64) string {
	if !NeedsToken(engineType) {
		if tokenID != nil {
			return DisplayType(engineType) + " is provided by this deployment and takes no key"
		}
		return ""
	}
	if tokenID == nil {
		return "choose the " + DisplayType(engineType) + " key this engine should use"
	}
	tok, err := q.GetVendorToken(ctx, db.GetVendorTokenParams{ID: *tokenID, TenantID: tenantID})
	if err != nil {
		return "that key does not exist"
	}
	if tok.Provider != engineType {
		return "that key is for " + DisplayType(tok.Provider) + ", not " + DisplayType(engineType)
	}
	return ""
}
