package aiengine

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/extension"
	"openloc/pkg/extsdk"
)

// ValidateModelSelection checks a {token, model, settings} triple — everything
// needed to reach a vendor with a configured model. It resolves the token to
// its vendor (tenant-scoped, so a foreign token id reads as "not found" rather
// than leaking), then applies the vendor's model catalog and that model's
// parameter whitelist (the same setting can be valid on one
// model of a vendor and a hard 400 on another).
//
// Returns the resolved vendor slug and a client-safe message ("" = valid). The
// vendor is what tells extension rows apart from LLM ones, so callers get it
// back instead of re-reading the token.
func ValidateModelSelection(
	ctx context.Context, q db.Querier, catalog *ai.Catalog,
	tenantID, tokenID int64, model string, settings map[string]any,
) (vendor string, msg string) {
	if tokenID <= 0 {
		return "", "a token is required"
	}
	tok, err := q.GetVendorToken(ctx, db.GetVendorTokenParams{ID: tokenID, TenantID: tenantID})
	if err != nil {
		return "", "token not found"
	}
	model = strings.TrimSpace(model)
	if extID, isExt := extension.IDFromProvider(tok.Provider); isExt {
		// An extension has no models and takes no LLM parameters; the model
		// names the extension.
		if model != extID {
			return tok.Provider, "a custom provider's model must be its id " + extID
		}
		if len(settings) > 0 {
			return tok.Provider, "a custom provider takes no model settings"
		}
		ext, err := q.GetExtension(ctx, db.GetExtensionParams{TenantID: tenantID, ExtID: extID})
		if err != nil {
			return tok.Provider, "custom provider " + extID + " is not added"
		}
		if !slices.Contains(ext.Capabilities, extsdk.CapabilityQE) && !slices.Contains(ext.Capabilities, extsdk.CapabilityAPE) {
			return tok.Provider, "custom provider " + extID + " cannot review translations"
		}
		return tok.Provider, ""
	}
	if !catalog.Valid(ctx, tenantID, tok.Provider, model) {
		return tok.Provider, "invalid model for provider " + tok.Provider
	}
	return tok.Provider, ai.ValidateModelConfig(tok.Provider, model, settings)
}

// SettingsBytes marshals a settings map for the jsonb column, normalizing nil
// and empty to "{}" so the column is never NULL or an empty byte slice.
func SettingsBytes(settings map[string]any) ([]byte, error) {
	if len(settings) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(settings)
}
