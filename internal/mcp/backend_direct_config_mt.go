package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"openloc/internal/aiengine"
	"openloc/internal/aiprompt"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/mt"
	"openloc/internal/mtengine"
	"openloc/internal/settings"
)

// Configuration tools: MT engines, routers, the step and
// workflow libraries, templates and QA profiles. Every write calls the same
// domain function as the REST handler; previews call its validate-only half,
// so a preview that succeeds describes a change that will be accepted.

// configErr maps a domain service error onto the MCP error surface: an id
// outside the tenant is errNotFound, a caller mistake or a refusal the current
// state causes carries its message to the model, anything else stays internal.
func configErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if msg, ok := httputil.AsClientError(err); ok {
		return invalidf("%s", msg)
	}
	if msg, ok := httputil.AsConflictError(err); ok {
		return invalidf("%s", msg)
	}
	if msg, ok := aiprompt.AsValidation(err); ok {
		return invalidf("%s", msg)
	}
	if msg, ok := aiengine.AsValidation(err); ok {
		return invalidf("%s", msg)
	}
	if errors.Is(err, settings.ErrTMProfileLinked) {
		return invalidf("this template follows a TM profile, so its match threshold and penalties come from that profile — change them on the TM profile, or detach the template from it in settings first")
	}
	return err
}

// configPlural renders "1 project" / "3 projects".
func configPlural(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func configMTEngineOut(e db.MtEngine) MTEngine {
	out := MTEngine{
		MTEngineID: e.ID, Name: e.Name, EngineType: e.EngineType,
		TypeName: mtengine.DisplayType(e.EngineType), Enabled: e.Enabled,
		Settings: mtengine.DecodeSettings(e.Settings), Unusable: string(mtengine.Health(e)),
		ProvisionedBy: mtengine.ProvisionedBy(e),
	}
	if e.TokenID != nil {
		out.KeyTokenID = *e.TokenID
	}
	return out
}

func (b *directBackend) ListMTEngines(ctx context.Context, _ ListMTEnginesIn) (MTEngineList, error) {
	tenantID, _ := scope(ctx)
	rows, err := b.q.ListMTEngines(ctx, tenantID)
	if err != nil {
		return MTEngineList{}, fmt.Errorf("list MT engines: %w", err)
	}
	out := MTEngineList{Engines: make([]MTEngine, 0, len(rows))}
	for _, e := range rows {
		out.Engines = append(out.Engines, configMTEngineOut(e))
	}
	return out, nil
}

func (b *directBackend) GetMTEngineCatalog(_ context.Context, _ GetMTEngineCatalogIn) (MTEngineCatalog, error) {
	entries := mtengine.Catalog()
	out := MTEngineCatalog{Types: make([]MTEngineType, 0, len(entries))}
	for _, c := range entries {
		out.Types = append(out.Types, MTEngineType{
			EngineType: c.EngineType, Name: c.Name, NeedsKey: c.NeedsToken, Params: c.Params,
		})
	}
	return out, nil
}

// ListMTLanguages asks the engine's provider, exactly as
// GET /api/mt-engines/{id}/languages does. The endpoint is the stored one,
// never a caller's.
func (b *directBackend) ListMTLanguages(ctx context.Context, in ListMTLanguagesIn) (MTLanguageList, error) {
	tenantID, _ := scope(ctx)
	e, err := b.q.GetMTEngineCredential(ctx, db.GetMTEngineCredentialParams{ID: in.MTEngineID, TenantID: tenantID})
	if err != nil {
		return MTLanguageList{}, configErr(err)
	}
	// Without a usable key the engine still has a static catalog to show.
	apiKey, apiURL, credErr := mtengine.Credential(e, b.dec)
	if credErr != nil {
		logging.Warn(ctx, credErr, "MT engine credential unavailable for a catalog read",
			"mt-languages-credential-failed", logging.Engine(e.EngineType))
	}
	cat := mt.SupportedLanguages(ctx, mt.CatalogEngine(e.EngineType, apiKey, apiURL, nil))
	return MTLanguageList{MTEngineID: e.ID, Catalog: cat}, nil
}

// CreateMTEngine builds only the types this deployment supplies. A DeepL or
// Google engine spends a provider key, and choosing or entering a key stays in
// AI settings.
func (b *directBackend) CreateMTEngine(ctx context.Context, in CreateMTEngineIn) (MTEngine, error) {
	tenantID, _ := scope(ctx)
	if mtengine.NeedsToken(in.EngineType) {
		return MTEngine{}, invalidf("%s needs a provider key, so it is created in AI settings, not here — pick a type whose needs_key is false from openloc_get_mt_engine_catalog",
			mtengine.DisplayType(in.EngineType))
	}
	e, err := mtengine.Create(ctx, b.q, tenantID, mtengine.CreateInput{
		Name: in.Name, EngineType: in.EngineType, Enabled: in.Enabled, Settings: in.Settings,
	})
	if err != nil {
		return MTEngine{}, configErr(err)
	}
	return configMTEngineOut(e), nil
}

func (in UpdateMTEngineIn) service() mtengine.UpdateInput {
	return mtengine.UpdateInput{Name: in.Name, Enabled: in.Enabled, Settings: in.Settings}
}

func (b *directBackend) PreviewUpdateMTEngine(ctx context.Context, in UpdateMTEngineIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	if _, err := mtengine.PrepareUpdate(ctx, b.q, tenantID, in.MTEngineID, in.service()); err != nil {
		return Preview{}, configErr(err)
	}
	e, err := b.q.GetMTEngine(ctx, db.GetMTEngineParams{ID: in.MTEngineID, TenantID: tenantID})
	if err != nil {
		return Preview{}, configErr(err)
	}
	refs, err := b.q.CountMTEngineReferences(ctx, e.ID)
	if err != nil {
		return Preview{}, fmt.Errorf("count MT engine references: %w", err)
	}
	p := Preview{Summary: fmt.Sprintf("Update the MT engine %q.", e.Name)}
	if refs > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf("Used by %s; every project routed to it translates with the change from now on.", configPlural(int64(refs), "MT router")))
	}
	if in.Enabled != nil && !*in.Enabled && e.Enabled {
		p.Effects = append(p.Effects, "Switching it off stops machine translation for every route that uses it.")
	}
	return p, nil
}

func (b *directBackend) UpdateMTEngine(ctx context.Context, in UpdateMTEngineIn) (MTEngine, error) {
	tenantID, _ := scope(ctx)
	e, err := mtengine.Update(ctx, b.q, tenantID, in.MTEngineID, in.service())
	if err != nil {
		return MTEngine{}, configErr(err)
	}
	return configMTEngineOut(e), nil
}

func (b *directBackend) PreviewDeleteMTEngine(ctx context.Context, in DeleteMTEngineIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	e, err := mtengine.CheckDelete(ctx, b.q, tenantID, in.MTEngineID)
	if err != nil {
		return Preview{}, configErr(err)
	}
	return Preview{Summary: fmt.Sprintf("Delete the MT engine %q. No router uses it.", e.Name)}, nil
}

func (b *directBackend) DeleteMTEngine(ctx context.Context, in DeleteMTEngineIn) (DeletedConfig, error) {
	tenantID, _ := scope(ctx)
	e, err := mtengine.CheckDelete(ctx, b.q, tenantID, in.MTEngineID)
	if err != nil {
		return DeletedConfig{}, configErr(err)
	}
	if err := mtengine.Delete(ctx, b.q, tenantID, in.MTEngineID); err != nil {
		return DeletedConfig{}, configErr(err)
	}
	return DeletedConfig{ID: e.ID, Name: e.Name}, nil
}
