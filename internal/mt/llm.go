package mt

import (
	"context"

	"openloc/internal/inlinetag"
)

// LLMTranslateFunc translates a batch source→target, preserving index
// alignment (result i ↔ req.Texts[i]). Results may carry a reviewer comment.
// Takes the whole request rather than bare texts so the pipeline's
// closure can use req.Rows to render segment prompts. Injected by
// the pipeline so this package stays decoupled from the concrete LLM client in
// internal/ai.
type LLMTranslateFunc func(ctx context.Context, req TranslateRequest) ([]TranslateResult, error)

// LLMEngine adapts an AI text-generation provider to the MT Engine interface
// (AI-as-MT). The transport (HTTP, prompt assembly, credentials) lives
// behind the injected Fn; this type only maps to/from the batch Engine contract
// so an AI engine of type 'mt' can be assigned wherever a classic MT engine can.
type LLMEngine struct {
	EngineName string
	Fn         LLMTranslateFunc
}

func (e LLMEngine) Name() string { return e.EngineName }

// takesAnyLanguage: the prompt names the job's locales as they are, so es-ar
// and zh-tw reach the model unchanged.
func (LLMEngine) takesAnyLanguage() {}

func (e LLMEngine) Translate(ctx context.Context, req TranslateRequest) ([]TranslateResult, error) {
	return e.Fn(ctx, req)
}

// TranslateTagged translates rich text with its inline tags intact.
// Tags travel as the compact chip labels the CAT editor shows —
// `<1>` … `</1>`, `{2}` — so the model can place them itself instead of us
// projecting them onto plain output afterwards.
//
// Encoding happens here, before Fn, on purpose: Fn renders the tenant's segment
// prompt from req.Texts, so <source_text> picks up the chip form with no change
// to the pipeline. The per-segment tables stay local — they are meaningless
// outside this call.
//
// A label the model drops, invents or mangles is NOT repaired: Decode leaves it
// in the text, where the reviewer and the tag QA rule can see it.
func (e LLMEngine) TranslateTagged(ctx context.Context, req TranslateRequest) ([]TranslateResult, error) {
	tables := make([]*inlinetag.Table, len(req.Texts))
	encoded := make([]string, len(req.Texts))
	for i, text := range req.Texts {
		encoded[i], tables[i] = inlinetag.Encode(text)
	}
	req.Texts = encoded
	req.Tagged = true

	results, err := e.Fn(ctx, req)
	if err != nil {
		return nil, err
	}
	for i := range results {
		if i >= len(tables) {
			break
		}
		results[i].Text, _ = inlinetag.Decode(results[i].Text, tables[i])
	}
	return results, nil
}
