package mtengine

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/enginehealth"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/mt"
)

type Handler struct {
	queries db.Querier
	// dec resolves a vendor token's key. Needed because a provider engine's
	// catalog cannot be read without it — see Credential.
	dec Decryptor
}

func NewHandler(q db.Querier, dec Decryptor) *Handler {
	return &Handler{queries: q, dec: dec}
}

// engineStatus is the wire shape for an MT engine. It deliberately OMITS
// api_key: the browser must never hold MT credentials. `has_api_key` is enough
// to render "set / not set", and the edit form follows the same "blank keeps
// the current key" rule as AI tokens.
type engineStatus struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	EngineType string  `json:"engine_type"`
	ApiURL     *string `json:"api_url"`
	Enabled    bool    `json:"enabled"`
	HasAPIKey  bool    `json:"has_api_key"`
	// The AI settings key this engine spends. Null for a
	// deployment engine, whose credential comes from the environment.
	TokenID *int64 `json:"token_id"`
	// The provider request options this engine translates with, keyed by
	// ParamDef.Key. Always an object, never null — the editor binds to it.
	Settings map[string]any `json:"settings"`
	// Why this engine cannot translate, or "" when it can.
	// Switched off, or the key it spends was deleted — the list, every router
	// that routes to it and the pipeline all read this one field.
	Unusable string `json:"unusable"`
	// Where this engine came from: "" = a user built it here, "deployment" =
	// it was provisioned with the org from what the host supplies
	//
	// PROVENANCE ONLY — it restricts nothing. A deployment engine is the org's
	// to rename, switch off and delete like any other; this says where it came
	// from, which is what the AI settings grid needs to know whether a built-in
	// vendor is available at all.
	ProvisionedBy string `json:"provisioned_by"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

const provisionedByDeployment = "deployment"

// ProvisionedBy reports where an engine came from, or "" when a user built it.
//
// Provenance is a property of the ROW, not the engine type: a hand-made mock or
// LibreTranslate engine reads as the user's, because it is. Only the rows
// `mtdefaults` created with the org carry the flag.
func ProvisionedBy(e db.MtEngine) string {
	if e.DeploymentManaged {
		return provisionedByDeployment
	}
	return ""
}

func engineStatusFrom(ctx context.Context, e db.MtEngine) engineStatus {
	return engineStatus{
		ID: e.ID, Name: e.Name, EngineType: e.EngineType, ApiURL: e.ApiUrl,
		Enabled:       e.Enabled,
		HasAPIKey:     e.ApiKey != nil && *e.ApiKey != "",
		TokenID:       e.TokenID,
		Settings:      DecodeSettings(e.Settings),
		Unusable:      string(enginehealth.Shown(Health(e), auth.IsDemo(ctx))),
		ProvisionedBy: ProvisionedBy(e),
		CreatedAt:     e.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:     e.UpdatedAt.Time.Format(time.RFC3339),
	}
}

// Health reports whether an engine can translate. A deployment-supplied type
// needs no key, so a null token is correct there rather than broken.
func Health(e db.MtEngine) enginehealth.Reason {
	return enginehealth.Of(e.Enabled, NeedsToken(e.EngineType), e.TokenID != nil)
}

// DecodeSettings turns the stored options into the wire object, defaulting to
// empty. A row whose JSON this build cannot read still lists — the engine is
// more useful than its options are, and the editor will simply show defaults.
func DecodeSettings(raw []byte) map[string]any {
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	engines, err := h.queries.ListMTEngines(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "MT engines")
		return
	}
	out := make([]engineStatus, 0, len(engines))
	for _, e := range engines {
		out = append(out, engineStatusFrom(r.Context(), e))
	}
	httputil.Data(w, out)
}

func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	// `enabled` is *bool so an absent field (→ true) is told apart from false:
	// a false default silently excludes the engine from routing.
	var req struct {
		Name       string `json:"name"`
		EngineType string `json:"engine_type"`
		Enabled    *bool  `json:"enabled"`
		// The AI settings key this engine spends. Required for a provider type,
		// meaningless for a built-in.
		TokenID *int64 `json:"token_id"`
		// Provider request options, validated against the type's catalog.
		Settings map[string]any `json:"settings"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	engine, err := Create(r.Context(), h.queries, tenantID, CreateInput{
		Name: req.Name, EngineType: req.EngineType, Enabled: req.Enabled,
		TokenID: req.TokenID, Settings: req.Settings,
	})
	if err != nil {
		httputil.ServiceError(w, err, "create", "MT engine")
		return
	}
	httputil.Created(w, engineStatusFrom(r.Context(), engine))
}

// encodeSettings marshals the options for storage. An empty map stores as `{}`
// rather than NULL, so every row reads back the same shape.
func encodeSettings(cfg map[string]any) ([]byte, error) {
	if cfg == nil {
		cfg = map[string]any{}
	}
	return json.Marshal(cfg)
}

// HandleUpdate applies a partial edit (see Update). Every field is a pointer so
// an absent one keeps the stored value — a partial PATCH that omits `enabled`
// must not disable the engine. There is no api_url field.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "engineID")
	if err != nil {
		httputil.BadRequest(w, "invalid engine ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())

	var req struct {
		Name       *string        `json:"name"`
		EngineType *string        `json:"engine_type"`
		Enabled    *bool          `json:"enabled"`
		TokenID    *int64         `json:"token_id"`
		Settings   map[string]any `json:"settings"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	engine, err := Update(r.Context(), h.queries, tenantID, id, UpdateInput{
		Name: req.Name, EngineType: req.EngineType, Enabled: req.Enabled,
		TokenID: req.TokenID, Settings: req.Settings,
	})
	if err != nil {
		httputil.ServiceError(w, err, "update", "MT engine")
		return
	}
	httputil.Data(w, engineStatusFrom(r.Context(), engine))
}

// HandleDelete deletes an MT engine; 409 while any live MT router still
// references it (see Delete).
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "engineID")
	if err != nil {
		httputil.BadRequest(w, "invalid engine ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := Delete(r.Context(), h.queries, tenantID, id); err != nil {
		httputil.ServiceError(w, err, "delete", "MT engine")
		return
	}
	httputil.NoContent(w)
}

func (h *Handler) HandleLanguages(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "engineID")
	if err != nil {
		httputil.BadRequest(w, "invalid engine ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	engine, err := h.queries.GetMTEngineCredential(r.Context(), db.GetMTEngineCredentialParams{
		ID:       id,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "MT engine") {
		return
	}

	// An engine with no key yet, or a key that cannot be decrypted, still has a
	// static catalog worth showing — better than a 500 on a settings page.
	apiKey, apiURL, credErr := Credential(engine, h.dec)
	if credErr != nil {
		logging.Warn(r.Context(), credErr, "MT engine credential unavailable for a catalog read",
			"mt-languages-credential-failed", logging.Engine(engine.EngineType))
	}
	// CatalogEngine, not NewFromDB: a row saved before its key still has a
	// static catalog to show, rather than a 500. nil settings because the
	// providers' language endpoints take none of the request options.
	catalog := mt.CatalogEngine(engine.EngineType, apiKey, apiURL, nil)
	httputil.Data(w, mt.SupportedLanguages(r.Context(), catalog))
}

// HandleCatalog returns the engine types a user may build, with the options
// each one takes. The create form renders its fields from this rather than from
// a hardcoded list, so a provider gaining an option needs no frontend change.
func (h *Handler) HandleCatalog(w http.ResponseWriter, _ *http.Request) {
	httputil.Data(w, Catalog())
}
