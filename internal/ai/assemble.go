package ai

import (
	"encoding/json"
	"openloc/internal/segmentprompt"
	"strings"

	"openloc/internal/langcode"
)

// Prompt assembly, exposed for preview. What a run sends the model is the
// tenant's instruction text plus a platform-owned suffix — the batched output
// contract. One implementation: the UI reads this one rather than deriving its
// own copy of the appended half.

// The tokens a system prompt may carry for the job's language pair,
// substituted into the instruction text at send time; everything else in a
// system prompt is sent verbatim. The pair is named by the instruction and
// nowhere else, so the author can see and reword it.
//
// Only these two: per-segment context travels in the segment body, where it
// is chosen with checkboxes.
const (
	TokenSourceLang = "<source_lang>"
	TokenTargetLang = "<target_lang>"
)

// substituteLanguages resolves the two tokens against the job's pair, naming
// each language rather than spelling out its code: "Austrian German (de-AT)".
// See langcode.Label for why both halves go.
//
// A code the label cannot name passes through untouched, which is also what
// makes this safe for the preview: with no job in hand the caller passes the
// `<source_lang>` / `<target_lang>` markers as the values, and they come out
// the other side unchanged.
func substituteLanguages(text, sourceLang, targetLang string) string {
	return strings.NewReplacer(
		TokenSourceLang, langcode.Label(sourceLang),
		TokenTargetLang, langcode.Label(targetLang),
	).Replace(text)
}

// Output modes. A prompt's mode is stored as four output flags and derived by
// aiprompt.Mode; these are the slugs that derivation yields.
const (
	ModeMT    = "mt"
	ModeQE    = "qe"
	ModeAPE   = "ape"
	ModeQEAPE = "qe_ape"
)

// PromptPreview is one prompt, resolved the way a run would resolve it.
type PromptPreview struct {
	OutputFormat string `json:"output_format"`
	// Instruction is the tenant's own text (or the built-in stand-in when the
	// prompt is empty) — the editable half.
	Instruction string `json:"instruction"`
	// Appended is the platform-owned half, read-only for the tenant.
	Appended string `json:"appended"`
	// SystemPrompt is Instruction + Appended: the exact string that goes on the
	// wire as the system message.
	SystemPrompt string `json:"system_prompt"`
}

// AssemblePrompt resolves a prompt draft for the given mode and language pair.
// An unknown mode is treated as QE+APE, which is what an LLM engine row with a
// non-MT prompt runs (see aiprompt.Mode).
//
// sourceLang/targetLang are codes, resolved into names by substituteLanguages.
// A caller with no concrete job in hand passes the `<source_lang>` /
// `<target_lang>` markers instead and gets a readable shape back.
//
// model decides how much of the output contract the suffix still spells out —
// a model the provider will hold to a JSON schema is not told the shape twice.
// Empty assumes a schema, so the prose contract is left out: the shape follows
// from the mode, and only a model known to take no schema gets the long form.
func AssemblePrompt(mode, promptText, sourceLang, targetLang, model string) PromptPreview {
	return AssemblePromptWithContext(mode, promptText, sourceLang, targetLang, model, nil)
}

// AssemblePromptWithContext is AssemblePrompt for a prompt whose per-segment
// context is known. The context decides the segment body PreviewContext
// renders, not the appended contract — that is shape only.
func AssemblePromptWithContext(mode, promptText, sourceLang, targetLang, model string, segmentContext []string) PromptPreview {
	var instruction string
	var formats promptFormats
	if mode == ModeMT {
		cfg := TranslateConfig{
			Provider: ProviderForModelID(model), Model: model,
			PromptText: promptText, SourceLang: sourceLang, TargetLang: targetLang,
			SegmentContext: segmentContext,
		}
		instruction, formats = translateInstruction(cfg), translateFormats(cfg)
	} else {
		cfg := PredictConfig{
			Provider: ProviderForModelID(model), Model: model,
			PromptText: promptText, SourceLang: sourceLang, TargetLang: targetLang,
			SegmentContext: segmentContext,
			Mode:           mode,
		}
		instruction, formats = predictInstruction(cfg), predictFormats(cfg)
	}
	appended := formats.OutputFormat
	return PromptPreview{
		OutputFormat: formats.OutputFormat,
		Instruction:  instruction,
		Appended:     appended,
		SystemPrompt: instruction + appended,
	}
}

// PreviewContext uses the runtime context builder and batch envelope with example values.
func PreviewContext(mode string, keys []string) (json.RawMessage, error) {
	content := segmentprompt.Build(segmentprompt.FieldsFor(mode, keys), segmentprompt.Values{
		SourceText: "<source text>", TargetText: "<target text>",
		TMMatches: []segmentprompt.TMMatch{{Source: "<TM source>", Target: "<TM target>", Score: 85}},
		Terms:     []segmentprompt.Term{{Source: "<source term>", Target: "<target term>", Status: "approved"}},
	})
	body, err := batchUserMessage([]batchItem{{ID: "1", Content: content}})
	return json.RawMessage(body), err
}
