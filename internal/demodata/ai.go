package demodata

import (
	"fmt"

	"openloc/internal/ai"
	"openloc/internal/aiengine"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
	"openloc/internal/segmentprompt"
)

// aiEngine is one engine the dataset may build, with the prompt it runs.
type aiEngine struct {
	provider, label, model, name, purpose, promptName, promptText string
	// review prompts judge and post-edit; the translation prompt only writes text.
	review bool
}

var reviewEngines = []aiEngine{
	{ai.ProviderOpenAI, "OpenAI", aiEngineModel, aiEngineName, aiengine.PurposeQEAPE, aiPromptName, aiPromptText, true},
	{ai.ProviderClaude, "Claude", aiClaudeModel, aiClaudeSystem, aiengine.PurposeQEAPE, aiClaudePrompt, aiClaudePromptText, true},
}

// translationEngine is the organization-only AI-as-MT engine.
var translationEngine = aiEngine{ai.ProviderOpenAI, "OpenAI", aiEngineModel, aiMTEngineName, aiengine.PurposeMT, aiMTPromptName, aiMTPromptText, false}

// aiEngines adds one review engine per LLM provider, each with the prompt it
// runs. Without Options.Organization a provider the tenant has no connection
// for is skipped and named in Result.Skipped. With it the engines are always
// built, keyless: the vendor and a connection exist, no token does, so the
// engine is complete on screen and can never call a provider.
func (b *builder) aiEngines() error {
	engines := reviewEngines
	if b.org != nil {
		engines = append(append([]aiEngine{}, reviewEngines...), translationEngine)
	}
	for _, e := range engines {
		tokenID, ok, err := b.aiToken(e.provider)
		if err != nil {
			return err
		}
		if !ok {
			b.res.Skipped = append(b.res.Skipped, fmt.Sprintf(
				"AI review engine %q: the organization has no %s connection.", e.name, e.label))
			continue
		}
		id, err := b.aiEngine(e, tokenID)
		if err != nil {
			return err
		}
		switch e.name {
		case aiEngineName:
			b.ai.nordwind = &id
		case aiClaudeSystem:
			b.ai.helix = &id
		case aiMTEngineName:
			b.ai.translation = &id
		}
	}
	return nil
}

// aiToken returns the token an engine row uses. ok is false when the tenant
// has no connection for the provider (never in organization mode, where the
// row stays keyless and tokenID is nil).
func (b *builder) aiToken(provider string) (tokenID *int64, ok bool, err error) {
	if b.org != nil {
		return nil, true, b.keylessProvider(provider)
	}
	tok, err := b.q.GetFirstVendorToken(b.ctx, db.GetFirstVendorTokenParams{TenantID: b.tenantID, Provider: provider})
	if isNoRows(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s token: %w", provider, err)
	}
	return &tok.ID, true, nil
}

// keylessProvider makes sure the provider's vendor row and one connection
// exist, without a token.
func (b *builder) keylessProvider(provider string) error {
	vendor, err := b.q.UpsertAIVendor(b.ctx, db.UpsertAIVendorParams{TenantID: b.tenantID, Provider: provider})
	if err != nil {
		return fmt.Errorf("ai vendor %s: %w", provider, err)
	}
	conns, err := b.q.ListAIConnections(b.ctx, b.tenantID)
	if err != nil {
		return fmt.Errorf("list ai connections: %w", err)
	}
	for _, c := range conns {
		if c.Provider == provider {
			return nil
		}
	}
	if _, err := b.q.CreateAIConnection(b.ctx, db.CreateAIConnectionParams{VendorID: vendor.ID, Name: ai.DisplayName(provider)}); err != nil {
		return fmt.Errorf("ai connection %s: %w", provider, err)
	}
	return nil
}

func (b *builder) aiEngine(e aiEngine, tokenID *int64) (int64, error) {
	params := db.CreateAIPromptParams{TenantID: b.tenantID, Name: e.promptName, PromptText: e.promptText, OutText: true}
	if e.review {
		// Judge and post-edit: the QE+APE output every review step expects. The
		// TM match is the context a reviewer most often needs.
		params.OutStatus, params.OutNote, params.OutComments = true, true, true
		params.SegmentContext = []string{segmentprompt.KeyTMMatch}
	}
	// The marketing reviewer also gets the glossary, and house rules per
	// language: what the demo's prompt tour shows.
	var rules []promptLanguageRule
	if e.promptName == aiPromptName {
		params.SegmentContext = []string{segmentprompt.KeyTMMatch, segmentprompt.KeyTerms}
		rules = nordwindLanguageRules
	}
	p, err := b.q.CreateAIPrompt(b.ctx, params)
	if err != nil {
		return 0, fmt.Errorf("ai prompt %s: %w", e.promptName, err)
	}
	for i, r := range rules {
		if err := b.q.CreateAIPromptLanguageAddition(b.ctx, db.CreateAIPromptLanguageAdditionParams{
			PromptID: p.ID, TargetLanguages: r.targets, AdditionText: r.text, Position: int32(i),
		}); err != nil {
			return 0, fmt.Errorf("ai prompt %s language rule: %w", e.promptName, err)
		}
	}
	if err := b.record(ItemAIPrompt, p.ID); err != nil {
		return 0, err
	}
	engine, err := b.q.CreateAIEngine(b.ctx, db.CreateAIEngineParams{TenantID: b.tenantID, Name: e.name, Purpose: e.purpose})
	if err != nil {
		return 0, fmt.Errorf("ai engine %s: %w", e.name, err)
	}
	if err := b.record(ItemAIEngine, engine.ID); err != nil {
		return 0, err
	}
	badOutput := "comment"
	if _, err := b.q.CreateAIEngineRow(b.ctx, db.CreateAIEngineRowParams{
		EngineID: engine.ID, SourceLang: langcode.Wildcard, TargetLangs: []string{langcode.Wildcard},
		SystemPromptID: &p.ID, BadOutputAction: &badOutput,
		TokenID: tokenID, Model: e.model, Settings: []byte("{}"), SortOrder: 0,
	}); err != nil {
		return 0, fmt.Errorf("ai engine row %s: %w", e.name, err)
	}
	b.res.AIEngines = append(b.res.AIEngines, Entity{ID: engine.ID, Name: engine.Name})
	return engine.ID, nil
}

// keylessMTEngine adds a DeepL engine with no token, so the MT router has a
// vendor engine to route to. Organization mode only.
func (b *builder) keylessMTEngine() (db.MtEngine, error) {
	if err := b.keylessProvider(ai.ProviderDeepL); err != nil {
		return db.MtEngine{}, err
	}
	e, err := b.q.CreateMTEngine(b.ctx, db.CreateMTEngineParams{
		Name: ai.DisplayName(ai.ProviderDeepL), EngineType: ai.ProviderDeepL, Enabled: true,
		TenantID: b.tenantID, Settings: []byte("{}"),
	})
	if err != nil {
		return db.MtEngine{}, fmt.Errorf("mt engine deepl: %w", err)
	}
	return e, nil
}

// routers gives the organization's MT router a per-language rule (French goes
// to the AI translation engine) and its AI router a rule sending German to the
// regulated-content reviewer. Organization mode only.
func (b *builder) routers(mtRouterID int64) error {
	if b.ai.translation != nil {
		if _, err := b.q.CreateMTRouterRule(b.ctx, db.CreateMTRouterRuleParams{RouterID: mtRouterID, TargetLang: "fr", AiEngineID: b.ai.translation}); err != nil {
			return fmt.Errorf("mt router rule: %w", err)
		}
	}
	if b.lib.HasAI && b.ai.helix != nil {
		if _, err := b.q.CreateAIRouterRule(b.ctx, db.CreateAIRouterRuleParams{RouterID: b.lib.AIRouter.ID, TargetLang: "de", EngineID: *b.ai.helix}); err != nil {
			return fmt.Errorf("ai router rule: %w", err)
		}
	}
	return nil
}
