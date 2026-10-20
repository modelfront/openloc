package mtengine

import (
	"openloc/internal/ai"
	"openloc/internal/mtdefaults"
)

// The request options each MT provider accepts, stored on mt_engines.settings
// and applied to the outbound call by internal/mt.
//
// This is what makes two engines on ONE key worth having: same DeepL account,
// one formal and one informal. The shape is ai.ParamDef because it is the same
// problem the LLM parameter editor already solves — a per-provider set of
// tunables, stored as JSON, validated on save, rendered from the definition —
// and the settings panel on the frontend renders both from that one type.
//
// Only options the provider's translate call actually takes. A knob we invent
// here is a field that silently does nothing, which is worse than its absence.
// Deliberately NOT included:
//   - DeepL `tag_handling` / `outline_detection`: set per request from whether
//     the segment carries inline tags (internal/mt/deepl.go). A user value
//     would be overwritten, or would break tag transfer if it weren't.
//   - Google `format`: same story — it tracks tag mode, not preference.

// Option keys. internal/mt/factory.go reads the same keys as string literals
// (it cannot import this package), so keep the two in sync: a mismatch drops
// the option silently.
const (
	// DeepL
	OptFormality          = "formality"
	OptGlossaryID         = "glossary_id"
	OptPreserveFormatting = "preserve_formatting"
	OptSplitSentences     = "split_sentences"
	OptModelType          = "model_type"
	// Google
	OptModel = "model"
)

var deepLParams = []ai.ParamDef{
	{
		Key: OptFormality, Label: "Formality", Type: ai.ParamEnum,
		Options: []ai.ParamOption{
			{Value: "default", Label: "Default"},
			{Value: "prefer_more", Label: "More formal (if supported)"},
			{Value: "prefer_less", Label: "Less formal (if supported)"},
			{Value: "more", Label: "More formal (fail if unsupported)"},
			{Value: "less", Label: "Less formal (fail if unsupported)"},
		},
		Description: "How formal the translation should sound. Only some target languages support it — the two \"if supported\" values fall back instead of failing.",
	},
	{
		Key: OptGlossaryID, Label: "Glossary ID", Type: ai.ParamString,
		Description: "A glossary created in your DeepL account. It applies only to the language pairs the glossary was made for.",
	},
	{
		Key: OptPreserveFormatting, Label: "Preserve formatting", Type: ai.ParamBoolean,
		Description: "Keep the source's punctuation and capitalization instead of letting DeepL correct them.",
	},
	{
		Key: OptSplitSentences, Label: "Split sentences", Type: ai.ParamEnum,
		Options: []ai.ParamOption{
			{Value: "0", Label: "Never"},
			{Value: "1", Label: "On punctuation and newlines"},
			{Value: "nonewlines", Label: "On punctuation only"},
		},
		Description: "How DeepL breaks a segment into sentences before translating.",
	},
	{
		Key: OptModelType, Label: "Model", Type: ai.ParamEnum,
		Options: []ai.ParamOption{
			{Value: "latency_optimized", Label: "Faster"},
			{Value: "quality_optimized", Label: "Higher quality"},
			{Value: "prefer_quality_optimized", Label: "Higher quality if available"},
		},
		Description: "Trade speed against quality. Higher quality is not offered for every language pair.",
	},
}

var googleParams = []ai.ParamDef{
	{
		Key: OptModel, Label: "Model", Type: ai.ParamEnum,
		Options: []ai.ParamOption{
			{Value: "nmt", Label: "Neural (nmt)"},
			{Value: "base", Label: "Phrase-based (base)"},
		},
		Description: "Which Google translation model to call. Neural is the default and is better for most text.",
	},
}

// paramsByType is the whole catalog. A type absent here takes no options, which
// is the honest answer for the built-ins: LibreTranslate and mock have nothing
// to tune, and their credentials are the deployment's anyway.
var paramsByType = map[string][]ai.ParamDef{
	EngineTypeDeepL:  deepLParams,
	EngineTypeGoogle: googleParams,
}

// ParamsFor returns the options an engine type accepts, never nil.
func ParamsFor(engineType string) []ai.ParamDef {
	if p := paramsByType[engineType]; p != nil {
		return p
	}
	return []ai.ParamDef{}
}

// ValidateSettings checks an engine's stored options against what its type
// accepts. Returns a client-safe message; "" means valid.
func ValidateSettings(engineType string, cfg map[string]any) string {
	return ai.ValidateParams(ParamsFor(engineType), cfg, DisplayType(engineType)+" engine")
}

// CatalogEntry is one buildable engine type, as the create form needs it: what
// to call it, whether it asks for a key, and the options it takes.
type CatalogEntry struct {
	EngineType string        `json:"engine_type"`
	Name       string        `json:"name"`
	NeedsToken bool          `json:"needs_token"`
	Params     []ai.ParamDef `json:"params"`
}

// Catalog is what a user may BUILD here: the key-backed providers, plus
// whatever this deployment supplies for free.
//
// The second group is environment-dependent (`mtdefaults.Available`), which is
// the point of asking rather than hardcoding: a production deployment supplies
// no mock, so no form can offer to create one. It is also what makes the
// built-ins deletable without that being a one-way door — you can add one back.
func Catalog() []CatalogEntry {
	out := []CatalogEntry{}
	for _, t := range []string{EngineTypeDeepL, EngineTypeGoogle} {
		out = append(out, CatalogEntry{
			EngineType: t, Name: DisplayType(t), NeedsToken: NeedsToken(t), Params: ParamsFor(t),
		})
	}
	for _, s := range mtdefaults.Available() {
		out = append(out, CatalogEntry{
			EngineType: s.EngineType, Name: s.Name, NeedsToken: false, Params: ParamsFor(s.EngineType),
		})
	}
	return out
}

// suppliedURL is the endpoint this deployment gives an engine type, or nil when
// the type is not one it supplies. Create reads it from here rather than from
// the request: LibreTranslate's URL must never be a field (standing rule), and
// an engine created without one could not reach anything.
func suppliedURL(engineType string) (*string, bool) {
	for _, s := range mtdefaults.Available() {
		if s.EngineType == engineType {
			return s.ApiUrl, true
		}
	}
	return nil, false
}
