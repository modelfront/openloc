package ai

import (
	"fmt"

	"openloc/internal/ptr"
)

// Per-MODEL tunable request parameters surfaced in the provider editor.
// A provider config stores chosen values in
// ai_engine_rows.settings (a JSON object keyed by ParamDef.Key); they are
// validated here on save and applied to the outbound request in settings.go.
//
// Why per-model and not per-vendor: Anthropic removed `temperature`, `top_p`,
// `top_k` and the fixed thinking budget on its current generation — sending any
// of them to Opus 5, Opus 4.8, Sonnet 5 or Fable 5 is a hard 400. Those models
// take `effort` (thinking depth) instead. Haiku 4.5 is the opposite: it takes
// the classic sampling knobs and a `budget_tokens` thinking budget, and rejects
// `effort`. One vendor-wide list can't describe both, and guessing wrong
// doesn't degrade — it fails the request.

// ParamType is the input kind a ParamDef expects (drives both the editor control
// and validation).
type ParamType string

const (
	ParamNumber  ParamType = "number"  // float, min ≤ v ≤ max
	ParamInteger ParamType = "integer" // whole number, min ≤ v ≤ max
	ParamBoolean ParamType = "boolean"
	ParamEnum    ParamType = "enum" // one of Options[].Value
	// Free text, for identifiers only a provider can validate — a DeepL glossary
	// id, say. No LLM parameter uses it; MT engine options do (internal/mtengine).
	ParamString ParamType = "string"
)

// ParamOption is a selectable value for an enum parameter.
type ParamOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ParamDef describes one tunable parameter: how to render + validate it.
type ParamDef struct {
	Key         string        `json:"key"`
	Label       string        `json:"label"`
	Type        ParamType     `json:"type"`
	Min         *float64      `json:"min,omitempty"`
	Max         *float64      `json:"max,omitempty"`
	Step        *float64      `json:"step,omitempty"`
	Options     []ParamOption `json:"options,omitempty"`
	Description string        `json:"description"`
}

// f points at a float64: the bounds in the parameter tables below.
var f = ptr.To[float64]

// Parameter keys. Named because settings.go maps them onto request bodies and a
// typo would silently drop a setting rather than fail.
const (
	ParamKeyTemperature        = "temperature"
	ParamKeyTopP               = "top_p"
	ParamKeyTopK               = "top_k"
	ParamKeyMaxTokens          = "max_tokens"
	ParamKeyMaxCompletionToks  = "max_completion_tokens"
	ParamKeyPresencePenalty    = "presence_penalty"
	ParamKeyFrequencyPenalty   = "frequency_penalty"
	ParamKeySeed               = "seed"
	ParamKeyEffort             = "effort"
	ParamKeyThinking           = "thinking"
	ParamKeyThinkingBudgetToks = "thinking_budget_tokens"
	ParamKeyReasoningEffort    = "reasoning_effort"
	ParamKeyVerbosity          = "verbosity"
)

// Thinking values for ParamKeyThinking.
const (
	ThinkingAdaptive = "adaptive"
	ThinkingDisabled = "disabled"
)

// paramFamily is the set of parameters a model accepts. Derived from the model
// id by familyFromID (modelclassify.go), a pure function, so a model the vendor
// ships after this build still resolves to a family — the failure mode for
// getting it wrong is a 400 on every request, not a soft degradation.
type paramFamily string

const (
	// familyNone — no tunable parameters.
	familyNone paramFamily = ""
	// familyOpenAIChat — OpenAI chat/completions sampling parameters.
	familyOpenAIChat paramFamily = "openai_chat"
	// familyOpenAIReasoning — the GPT-5 series. Same wire protocol as
	// familyOpenAIChat, opposite parameter set: Microsoft lists `temperature`,
	// `top_p`, `presence_penalty`, `frequency_penalty`, `logprobs`,
	// `top_logprobs`, `logit_bias` and `max_tokens` as "currently unsupported
	// with reasoning models", and sending any of them is a 400 rather than a
	// soft degradation. They take `reasoning_effort` + `verbosity` instead, and
	// cap output with `max_completion_tokens`.
	familyOpenAIReasoning paramFamily = "openai_reasoning"
	// familyClaudeEffort — Anthropic's current generation: adaptive thinking
	// with an `effort` dial, thinking switchable off. Sampling parameters and
	// `budget_tokens` are removed on these models (400).
	familyClaudeEffort paramFamily = "claude_effort"
	// familyClaudeEffortAlwaysThinking — as above, but thinking can't be turned
	// off (Fable 5 rejects an explicit thinking:disabled).
	familyClaudeEffortAlwaysThinking paramFamily = "claude_effort_always_thinking"
	// familyClaudeBudget — pre-adaptive Anthropic models: classic sampling
	// parameters plus a fixed thinking budget. `effort` errors on these.
	familyClaudeBudget paramFamily = "claude_budget"
)

// effortOptions are the thinking-depth levels, cheapest first.
var effortOptions = []ParamOption{
	{Value: "low", Label: "Low"},
	{Value: "medium", Label: "Medium"},
	{Value: "high", Label: "High (default)"},
	{Value: "xhigh", Label: "Extra high"},
	{Value: "max", Label: "Max"},
}

// claudeEffortParams is the shared body of the two effort families.
var claudeEffortParams = []ParamDef{
	{Key: ParamKeyEffort, Label: "Effort", Type: ParamEnum, Options: effortOptions,
		Description: "How hard the model thinks before answering, and how many tokens it spends overall. Higher is more thorough and more expensive; the provider default is High."},
	{Key: ParamKeyMaxTokens, Label: "Max output tokens", Type: ParamInteger, Min: f(1), Max: f(128000), Step: f(1),
		Description: "Upper bound on a single response — thinking and answer tokens share this budget. Leave unset to let the batch size decide; set it too low and a batched reply can be cut off mid-answer."},
}

var thinkingParam = ParamDef{
	Key: ParamKeyThinking, Label: "Thinking", Type: ParamEnum,
	Options: []ParamOption{
		{Value: ThinkingAdaptive, Label: "Adaptive (default)"},
		{Value: ThinkingDisabled, Label: "Off"},
	},
	Description: "Adaptive lets the model decide when to reason before answering. Off is cheaper and faster but lower quality on anything that needs judgement.",
}

// familyParams maps each family to its parameters. Hand-maintained: a family is
// a wire contract with the vendor, so it changes when the vendor changes its
// API, not when it ships a model.
var familyParams = map[paramFamily][]ParamDef{
	familyNone:                       nil,
	familyClaudeEffort:               append(append([]ParamDef{}, claudeEffortParams...), thinkingParam),
	familyClaudeEffortAlwaysThinking: claudeEffortParams,
	familyClaudeBudget: {
		{Key: ParamKeyTemperature, Label: "Temperature", Type: ParamNumber, Min: f(0), Max: f(1), Step: f(0.1),
			Description: "Sampling randomness (Anthropic range is 0–1). 0 is focused; 1 is most varied. Leave unset to use the provider default."},
		{Key: ParamKeyTopP, Label: "Top P", Type: ParamNumber, Min: f(0), Max: f(1), Step: f(0.05),
			Description: "Nucleus sampling. Prefer tuning either temperature or top_p, not both."},
		{Key: ParamKeyTopK, Label: "Top K", Type: ParamInteger, Min: f(0), Step: f(1),
			Description: "Only sample from the K most likely tokens. 0/unset disables it."},
		{Key: ParamKeyMaxTokens, Label: "Max output tokens", Type: ParamInteger, Min: f(1), Max: f(64000), Step: f(1),
			Description: "Upper bound on a single response. Leave unset to let the batch size decide."},
		{Key: ParamKeyThinkingBudgetToks, Label: "Thinking budget (tokens)", Type: ParamInteger, Min: f(1024), Max: f(64000), Step: f(1),
			Description: "Enables thinking with this token budget for reasoning before the answer. Must be lower than Max output tokens. Leave unset to disable."},
	},
	familyOpenAIChat: {
		{Key: ParamKeyTemperature, Label: "Temperature", Type: ParamNumber, Min: f(0), Max: f(2), Step: f(0.1),
			Description: "Sampling randomness. 0 is focused/deterministic; higher (up to 2) is more varied. Defaults to 0 for translation work."},
		{Key: ParamKeyTopP, Label: "Top P", Type: ParamNumber, Min: f(0), Max: f(1), Step: f(0.05),
			Description: "Nucleus sampling: consider only the most probable tokens whose cumulative probability is ≤ this. Prefer tuning either temperature or top_p, not both."},
		{Key: ParamKeyMaxCompletionToks, Label: "Max output tokens", Type: ParamInteger, Min: f(1), Max: f(128000), Step: f(1),
			Description: "Upper bound on tokens generated in the response. Leave unset for the model's own limit."},
		{Key: ParamKeyPresencePenalty, Label: "Presence penalty", Type: ParamNumber, Min: f(-2), Max: f(2), Step: f(0.1),
			Description: "Positive values push the model to talk about new topics rather than repeat what's already appeared."},
		{Key: ParamKeyFrequencyPenalty, Label: "Frequency penalty", Type: ParamNumber, Min: f(-2), Max: f(2), Step: f(0.1),
			Description: "Positive values discourage repeating the same tokens, reducing verbatim repetition."},
		{Key: ParamKeySeed, Label: "Seed", Type: ParamInteger, Min: f(0), Step: f(1),
			Description: "Best-effort determinism: the same seed + inputs tends to return the same output."},
	},
	familyOpenAIReasoning: {
		{Key: ParamKeyReasoningEffort, Label: "Reasoning effort", Type: ParamEnum, Options: reasoningEffortOptions,
			Description: "How much the model reasons before answering. Minimal is fastest and cheapest; higher is more thorough on ambiguous source text. The provider default is Medium."},
		{Key: ParamKeyVerbosity, Label: "Verbosity", Type: ParamEnum, Options: verbosityOptions,
			Description: "How concise the answer is. Translation wants Low — the output contract is JSON, and extra prose is wasted tokens."},
		{Key: ParamKeyMaxCompletionToks, Label: "Max output tokens", Type: ParamInteger, Min: f(1), Max: f(128000), Step: f(1),
			Description: "Upper bound on the response — reasoning and answer tokens share this budget, so a cap sized for the answer alone can truncate a batched reply. Leave unset for the model's own limit."},
	},
}

// reasoningEffortOptions are the GPT-5 reasoning depths, cheapest first. Scoped
// to what the ORIGINAL GPT-5 series accepts: `none`, `xhigh` and `max` arrive
// with gpt-5.1+ / gpt-5.6 and error on gpt-5, so they stay out until a model
// that takes them is in the catalog.
var reasoningEffortOptions = []ParamOption{
	{Value: "minimal", Label: "Minimal"},
	{Value: "low", Label: "Low"},
	{Value: "medium", Label: "Medium (default)"},
	{Value: "high", Label: "High"},
}

// verbosityOptions control answer length on the GPT-5 series.
var verbosityOptions = []ParamOption{
	{Value: "low", Label: "Low"},
	{Value: "medium", Label: "Medium (default)"},
	{Value: "high", Label: "High"},
}

// ParamsFor returns the parameters model accepts under provider, never nil. An
// unknown model yields an empty slice rather than a guess — better to offer no
// knobs than knobs that 400.
func ParamsFor(provider, model string) []ParamDef {
	if p := familyParams[familyFromID(provider, model)]; p != nil {
		return p
	}
	return []ParamDef{}
}

// ValidateModelConfig checks a provider config's settings against what the
// chosen model accepts. Unknown keys, wrong types and out-of-range values are
// rejected, as are the two combinations the provider itself rejects. Returns a
// client-safe message ("" = valid); a nil/empty config is always valid.
func ValidateModelConfig(provider, model string, cfg map[string]any) string {
	if len(cfg) == 0 {
		return ""
	}
	// Name the model, not just the vendor: "temperature isn't supported" is
	// baffling until you know it's this model that dropped it.
	if msg := ValidateParams(ParamsFor(provider, model), cfg, prettyLabel(model)+" model"); msg != "" {
		return msg
	}
	return crossFieldRules(model, cfg)
}

// ValidateParams type- and range-checks a settings object against the
// parameters its subject accepts, rejecting unknown keys. `subject` names what
// does the rejecting, in the user's vocabulary ("Opus 5 model", "DeepL
// engine"). Returns a client-safe message; "" means valid.
//
// Shared with the MT engine options catalog (internal/mtengine), which is the
// same problem — a per-provider set of tunables stored as JSON and validated on
// save — with a different catalog behind it.
func ValidateParams(params []ParamDef, cfg map[string]any, subject string) string {
	if len(cfg) == 0 {
		return ""
	}
	defs := make(map[string]ParamDef, len(params))
	for _, d := range params {
		defs[d.Key] = d
	}
	for key, raw := range cfg {
		d, ok := defs[key]
		if !ok {
			return fmt.Sprintf("%q is not a setting the %s accepts", key, subject)
		}
		if msg := validateParam(d, raw); msg != "" {
			return msg
		}
	}
	return ""
}

// validateParam type- and range-checks one value against its definition.
func validateParam(d ParamDef, raw any) string {
	switch d.Type {
	case ParamBoolean:
		if _, ok := raw.(bool); !ok {
			return fmt.Sprintf("%s must be true or false", d.Label)
		}
	case ParamEnum:
		s, ok := raw.(string)
		if !ok || !enumHas(d.Options, s) {
			return fmt.Sprintf("%s has an invalid value", d.Label)
		}
	case ParamString:
		// Only the shape is ours to check — the value is an identifier the
		// provider issued, and it is the provider that knows whether it exists.
		if _, ok := raw.(string); !ok {
			return fmt.Sprintf("%s must be text", d.Label)
		}
	case ParamNumber, ParamInteger:
		// JSON numbers decode to float64.
		n, ok := raw.(float64)
		if !ok {
			return fmt.Sprintf("%s must be a number", d.Label)
		}
		if d.Type == ParamInteger && n != float64(int64(n)) {
			return fmt.Sprintf("%s must be a whole number", d.Label)
		}
		if d.Min != nil && n < *d.Min {
			return fmt.Sprintf("%s must be ≥ %v", d.Label, *d.Min)
		}
		if d.Max != nil && n > *d.Max {
			return fmt.Sprintf("%s must be ≤ %v", d.Label, *d.Max)
		}
	}
	return ""
}

// crossFieldRules rejects value COMBINATIONS the provider rejects. Each of
// these is a 400 at request time, so catching them on save is the difference
// between an inline form error and every translation failing later.
func crossFieldRules(model string, cfg map[string]any) string {
	// A thinking budget must leave room for the answer.
	if budget, ok := numSetting(cfg, ParamKeyThinkingBudgetToks); ok {
		if maxTok, ok := numSetting(cfg, ParamKeyMaxTokens); ok && budget >= maxTok {
			return "Thinking budget must be lower than Max output tokens"
		}
	}
	// Opus 5 accepts thinking=off only at effort high or below.
	if model == modelClaudeOpus5 && strSetting(cfg, ParamKeyThinking) == ThinkingDisabled {
		switch strSetting(cfg, ParamKeyEffort) {
		case "xhigh", "max":
			return "Opus 5 can't turn thinking off above High effort — lower the effort or leave thinking on"
		}
	}
	return ""
}

func numSetting(cfg map[string]any, key string) (float64, bool) {
	v, ok := cfg[key].(float64)
	return v, ok
}

func strSetting(cfg map[string]any, key string) string {
	s, _ := cfg[key].(string)
	return s
}

func enumHas(opts []ParamOption, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

// OpenAIMaxTokensField is the request field that caps output for a model.
// Reasoning models (the GPT-5 series) reject `max_tokens` outright and take
// `max_completion_tokens` instead; every other OpenAI model
// takes the classic field. Exported because the chat adapter builds its own
// request body and must agree with the translate path.
func OpenAIMaxTokensField(model string) string {
	if familyFromID(ProviderOpenAI, model) == familyOpenAIReasoning {
		return ParamKeyMaxCompletionToks
	}
	return "max_tokens"
}
