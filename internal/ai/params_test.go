package ai

import (
	"context"
	"testing"
)

// Model ids used across the parameter tests. Named so a catalog rename breaks
// compilation here rather than silently reclassifying a case.
const (
	testOpenAIModel = "gpt-4o"
	testOpus5       = "claude-opus-5"
	testSonnet5     = "claude-sonnet-5"
	testFable5      = "claude-fable-5"
	testHaiku45     = "claude-haiku-4-5-20251001"
)

func TestValidateModelConfig(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		model    string
		cfg      map[string]any
		wantErr  bool
	}{
		{"empty is valid", ProviderOpenAI, testOpenAIModel, nil, false},
		{"valid openai temperature", ProviderOpenAI, testOpenAIModel, map[string]any{"temperature": 0.7}, false},
		{"valid openai integer", ProviderOpenAI, testOpenAIModel, map[string]any{"max_completion_tokens": float64(500)}, false},
		{"unknown key rejected", ProviderOpenAI, testOpenAIModel, map[string]any{"top_k": 5.0}, true},
		{"temperature over max rejected", ProviderOpenAI, testOpenAIModel, map[string]any{"temperature": 3.0}, true},
		{"temperature below min rejected", ProviderOpenAI, testOpenAIModel, map[string]any{"temperature": -1.0}, true},
		{"wrong type rejected", ProviderOpenAI, testOpenAIModel, map[string]any{"temperature": "hot"}, true},
		{"non-integer for integer rejected", ProviderOpenAI, testOpenAIModel, map[string]any{"seed": 1.5}, true},
		{"extension has no params", testExtProvider, "mockqe", map[string]any{"temperature": 0.5}, true},
		// An id no vendor claims has no parameters, so any setting is a mistake.
		{"unknown model offers no params", ProviderClaude, "mistral-large", map[string]any{"effort": "high"}, true},
		// A model the vendor ships after this build still classifies: an
		// unrecognised claude-* id takes the current generation's parameters
		// rather than none, which is what lets a new model work without a deploy.
		{"future claude model takes effort", ProviderClaude, "claude-opus-6", map[string]any{"effort": "high"}, false},
		{"future claude model still rejects temperature", ProviderClaude, "claude-opus-6", map[string]any{"temperature": 0.5}, true},

		// The whole point of making this model-aware: the same key is valid on
		// one model of a vendor and a hard 400 on another.
		{"haiku accepts temperature", ProviderClaude, testHaiku45, map[string]any{"temperature": 0.5}, false},
		{"haiku accepts top_k", ProviderClaude, testHaiku45, map[string]any{"top_k": float64(40)}, false},
		{"haiku temperature max is 1", ProviderClaude, testHaiku45, map[string]any{"temperature": 2.0}, true},
		{"opus 5 rejects temperature", ProviderClaude, testOpus5, map[string]any{"temperature": 0.5}, true},
		{"opus 5 rejects top_p", ProviderClaude, testOpus5, map[string]any{"top_p": 0.9}, true},
		{"opus 5 rejects top_k", ProviderClaude, testOpus5, map[string]any{"top_k": float64(40)}, true},
		{"opus 5 rejects a thinking budget", ProviderClaude, testOpus5, map[string]any{"thinking_budget_tokens": float64(4096)}, true},
		{"haiku rejects effort", ProviderClaude, testHaiku45, map[string]any{"effort": "high"}, true},

		{"opus 5 accepts effort", ProviderClaude, testOpus5, map[string]any{"effort": "xhigh"}, false},
		{"unknown effort level rejected", ProviderClaude, testOpus5, map[string]any{"effort": "turbo"}, true},
		{"opus 5 accepts thinking off", ProviderClaude, testOpus5, map[string]any{"thinking": ThinkingDisabled}, false},
		{"fable 5 can't turn thinking off", ProviderClaude, testFable5, map[string]any{"thinking": ThinkingDisabled}, true},
		{"fable 5 accepts effort", ProviderClaude, testFable5, map[string]any{"effort": "max"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := ValidateModelConfig(c.provider, c.model, c.cfg)
			if c.wantErr && msg == "" {
				t.Fatalf("expected an error, got none")
			}
			if !c.wantErr && msg != "" {
				t.Fatalf("expected valid, got %q", msg)
			}
		})
	}
}

// Combinations each provider rejects at request time. Catching them on save is
// the difference between an inline form error and every job failing later.
func TestValidateModelConfig_CrossFieldRules(t *testing.T) {
	t.Run("thinking budget must leave room for the answer", func(t *testing.T) {
		cfg := map[string]any{"thinking_budget_tokens": float64(8192), "max_tokens": float64(4096)}
		if msg := ValidateModelConfig(ProviderClaude, testHaiku45, cfg); msg == "" {
			t.Fatal("a budget ≥ max_tokens must be rejected")
		}
		cfg["max_tokens"] = float64(16000)
		if msg := ValidateModelConfig(ProviderClaude, testHaiku45, cfg); msg != "" {
			t.Fatalf("a budget below max_tokens is valid, got %q", msg)
		}
	})

	t.Run("opus 5 can't disable thinking above high effort", func(t *testing.T) {
		for _, effort := range []string{"xhigh", "max"} {
			cfg := map[string]any{"thinking": ThinkingDisabled, "effort": effort}
			if msg := ValidateModelConfig(ProviderClaude, testOpus5, cfg); msg == "" {
				t.Errorf("thinking off + effort %s must be rejected on Opus 5", effort)
			}
		}
		for _, effort := range []string{"low", "medium", "high"} {
			cfg := map[string]any{"thinking": ThinkingDisabled, "effort": effort}
			if msg := ValidateModelConfig(ProviderClaude, testOpus5, cfg); msg != "" {
				t.Errorf("thinking off + effort %s is valid on Opus 5, got %q", effort, msg)
			}
		}
		// Sonnet 5 has no such restriction.
		cfg := map[string]any{"thinking": ThinkingDisabled, "effort": "max"}
		if msg := ValidateModelConfig(ProviderClaude, testSonnet5, cfg); msg != "" {
			t.Errorf("Sonnet 5 allows thinking off at any effort, got %q", msg)
		}
	})
}

// The GPT-5 series takes the opposite parameter set to the GPT-4 chat models.
// Offering a knob the model rejects turns every translation into a 400, so the
// catalog must not carry one across.
func TestParamsFor_GPT5OffersReasoningNotSampling(t *testing.T) {
	keys := map[string]bool{}
	for _, p := range ParamsFor(ProviderOpenAI, "gpt-5") {
		keys[p.Key] = true
	}
	for _, want := range []string{ParamKeyReasoningEffort, ParamKeyVerbosity, ParamKeyMaxCompletionToks} {
		if !keys[want] {
			t.Errorf("gpt-5 should offer %q", want)
		}
	}
	// Verbatim from Microsoft's "Not Supported" list for reasoning models.
	for _, banned := range []string{
		ParamKeyTemperature, ParamKeyTopP, ParamKeyPresencePenalty,
		ParamKeyFrequencyPenalty, ParamKeyMaxTokens, ParamKeySeed,
	} {
		if keys[banned] {
			t.Errorf("gpt-5 must not offer %q — the provider rejects it", banned)
		}
	}

	// And the GPT-4 chat models keep their sampling knobs.
	chatKeys := map[string]bool{}
	for _, p := range ParamsFor(ProviderOpenAI, "gpt-4o") {
		chatKeys[p.Key] = true
	}
	if !chatKeys[ParamKeyTemperature] || chatKeys[ParamKeyReasoningEffort] {
		t.Error("gpt-4o should keep temperature and not gain reasoning_effort")
	}
}

// Save-time validation is the line of defence that turns a runtime 400 into an
// inline form error.
func TestValidateModelConfig_GPT5RejectsSamplingKnobs(t *testing.T) {
	if msg := ValidateModelConfig(ProviderOpenAI, "gpt-5", map[string]any{"temperature": 0.0}); msg == "" {
		t.Error("temperature on gpt-5 should be rejected on save")
	}
	if msg := ValidateModelConfig(ProviderOpenAI, "gpt-5", map[string]any{"reasoning_effort": "low"}); msg != "" {
		t.Errorf("reasoning_effort=low should be valid on gpt-5, got %q", msg)
	}
	// `none`/`xhigh`/`max` land with gpt-5.1+/5.6 and error on plain gpt-5.
	if msg := ValidateModelConfig(ProviderOpenAI, "gpt-5", map[string]any{"reasoning_effort": "xhigh"}); msg == "" {
		t.Error("reasoning_effort=xhigh is not supported on gpt-5 and should be rejected")
	}
}

func TestParamsFor_NeverOffersRemovedAnthropicParams(t *testing.T) {
	removed := map[string]bool{
		ParamKeyTemperature: true, ParamKeyTopP: true, ParamKeyTopK: true,
		ParamKeyThinkingBudgetToks: true,
	}
	for _, model := range []string{testOpus5, "claude-opus-4-8", testSonnet5, testFable5} {
		for _, p := range ParamsFor(ProviderClaude, model) {
			if removed[p.Key] {
				t.Errorf("%s: catalog offers %q, which the model rejects with a 400", model, p.Key)
			}
		}
	}
	// ...and the older model must keep them, or its knobs vanish for no reason.
	var haveTemp bool
	for _, p := range ParamsFor(ProviderClaude, testHaiku45) {
		if p.Key == ParamKeyTemperature {
			haveTemp = true
		}
		if p.Key == ParamKeyEffort {
			t.Error("Haiku 4.5 errors on effort — it must not be offered")
		}
	}
	if !haveTemp {
		t.Error("Haiku 4.5 accepts temperature; it should still be offered")
	}
}

func TestCatalogModels_CarryPerModelParams(t *testing.T) {
	c := NewCatalog(nil, nil, DefaultModelLimit)
	c.Set(1, ProviderClaude, classifyAll(ProviderClaude, []RemoteModel{
		{ID: testOpus5}, {ID: testHaiku45},
	}))
	models := c.Models(context.Background(), 1, ProviderClaude)
	if len(models) == 0 {
		t.Fatal("claude should expose models")
	}
	byID := map[string][]ParamDef{}
	for _, m := range models {
		byID[m.ID] = m.Params
	}
	if len(byID[testOpus5]) == 0 || len(byID[testHaiku45]) == 0 {
		t.Fatal("every model should carry its own params")
	}
	keys := func(defs []ParamDef) map[string]bool {
		out := map[string]bool{}
		for _, d := range defs {
			out[d.Key] = true
		}
		return out
	}
	if keys(byID[testOpus5])[ParamKeyTemperature] {
		t.Error("Opus 5 must not advertise temperature")
	}
	if !keys(byID[testOpus5])[ParamKeyEffort] {
		t.Error("Opus 5 should advertise effort")
	}
	// An extension exposes no models at all, so there's nothing to carry params.
	if len(c.Models(context.Background(), 1, testExtProvider)) != 0 {
		t.Error("an extension exposes no chat models")
	}
}
