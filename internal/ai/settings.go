package ai

import (
	"context"
	"encoding/json"

	"openloc/internal/logging"
)

// Applying a provider config's stored settings to the outbound request
//
// Two rules keep this safe:
//
//  1. WHITELIST BY CATALOG. Only keys the chosen model actually accepts are
//     copied into the body. settings is jsonb — it can hold keys written by an
//     older schema, a hand-edited row, or a since-changed catalog, and blindly
//     splatting it into the request body would let those become arbitrary
//     provider parameters (`stream`, `messages`, …).
//  2. MAP, DON'T FORWARD. Several keys aren't top-level provider fields —
//     `effort` nests under output_config, `thinking` is an object, a Haiku
//     budget is thinking.budget_tokens. The stored key is our vocabulary, not
//     the wire format.

// Settings is a provider config's validated tunable parameters
// (ai_engine_rows.settings). Nil is the common case and means "provider defaults".
type Settings map[string]any

// ParseSettings decodes a settings jsonb column. Unparseable JSON yields nil
// rather than an error: a corrupt settings blob must not stop a file from being
// translated, and the request is still valid without it.
func ParseSettings(ctx context.Context, raw []byte) Settings {
	if len(raw) == 0 {
		return nil
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		logging.Warn(ctx, err, "ignoring unparseable provider settings", "ai-settings-unparseable")
		return nil
	}
	return s
}

// allowed returns the settings keys model accepts, dropping anything else.
func (s Settings) allowed(provider, model string) Settings {
	if len(s) == 0 {
		return nil
	}
	out := make(Settings, len(s))
	for _, d := range ParamsFor(provider, model) {
		if v, ok := s[d.Key]; ok {
			out[d.Key] = v
		}
	}
	return out
}

// applyOpenAI maps settings onto an OpenAI chat/completions body. Keys are
// top-level fields there, so this is a filtered copy — except temperature,
// which keeps its long-standing 0 default when unset (deterministic output is
// what translation wants, and changing that silently would shift every
// tenant's results).
//
// Reasoning models (GPT-5 series) are the exception: they reject `temperature`
// outright, so the default must NOT be applied to them — it would 400 every
// request. They're deterministic-ish by construction anyway; `reasoning_effort`
// is the dial that replaces it.
func (s Settings) applyOpenAI(body map[string]any, model string) {
	if familyFromID(ProviderOpenAI, model) != familyOpenAIReasoning {
		body["temperature"] = 0
	}
	for k, v := range s.allowed(ProviderOpenAI, model) {
		body[k] = v
	}
}

// applyClaude maps settings onto an Anthropic messages body. batchTokens is the
// batch-derived max_tokens (see batchMaxTokens) — used unless the config pins
// one explicitly. Returns nothing; mutates body.
func (s Settings) applyClaude(body map[string]any, model string, batchTokens int) {
	allowed := s.allowed(ProviderClaude, model)

	// max_tokens is required by Anthropic and caps thinking + answer together.
	maxTokens := batchTokens
	if v, ok := numSetting(allowed, ParamKeyMaxTokens); ok && v >= 1 {
		maxTokens = int(v)
	}
	body["max_tokens"] = maxTokens

	// Sampling knobs (pre-adaptive models only — the catalog already refuses to
	// offer them elsewhere, so reaching here means the model accepts them).
	for _, key := range []string{ParamKeyTemperature, ParamKeyTopP, ParamKeyTopK} {
		if v, ok := allowed[key]; ok {
			body[key] = v
		}
	}

	// Thinking. Two spellings across generations: `effort` + an on/off switch on
	// the current models, a fixed `budget_tokens` on the older ones.
	thinking := strSetting(allowed, ParamKeyThinking)
	effort := strSetting(allowed, ParamKeyEffort)
	if thinking == ThinkingDisabled && (effort == "xhigh" || effort == "max") {
		// The provider rejects this pair; save-time validation blocks it, so a
		// row reaching here was written before that rule or edited directly.
		// Honour the explicit "off" and drop the effort — turning thinking back
		// on would silently multiply the tenant's token spend.
		effort = ""
	}
	switch {
	case thinking == ThinkingDisabled:
		body["thinking"] = map[string]any{"type": ThinkingDisabled}
	case thinking == ThinkingAdaptive:
		body["thinking"] = map[string]any{"type": ThinkingAdaptive}
	}
	if effort != "" {
		body["output_config"] = map[string]any{"effort": effort}
	}
	if budget, ok := numSetting(allowed, ParamKeyThinkingBudgetToks); ok && budget >= 1024 {
		// Older-generation thinking. The budget must stay under max_tokens or
		// the request 400s — validation enforces it on save, and this guard
		// covers rows that predate it.
		if int(budget) < maxTokens {
			body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": int(budget)}
		}
	}
}

// thinkingEnabled reports whether a request under these settings will spend
// thinking tokens. It drives the max_tokens headroom in batchMaxTokens: on the
// current Anthropic models thinking is ON unless switched off, and it shares
// the max_tokens budget with the answer — so a cap sized for output alone can
// truncate a batched reply mid-array.
func (s Settings) thinkingEnabled(provider, model string) bool {
	if provider != ProviderClaude {
		return false
	}
	allowed := s.allowed(provider, model)
	switch familyFromID(provider, model) {
	case familyClaudeEffort:
		return strSetting(allowed, ParamKeyThinking) != ThinkingDisabled
	case familyClaudeEffortAlwaysThinking:
		return true // Fable 5 can't turn it off
	case familyClaudeBudget:
		_, ok := numSetting(allowed, ParamKeyThinkingBudgetToks)
		return ok
	}
	return false
}
