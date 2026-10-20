package mt

import (
	"context"
	"strings"
	"testing"
)

// LLMEngine must advertise the tag-preserving capability, or translateChunk
// keeps routing tagged rows down the plain path and the tags get projected
// instead of translated.
var _ TagPreservingEngine = LLMEngine{}

func TestLLMEngine_TranslateTagged_EncodesAndDecodes(t *testing.T) {
	var got TranslateRequest
	engine := LLMEngine{
		EngineName: "ai:test",
		Fn: func(_ context.Context, req TranslateRequest) ([]TranslateResult, error) {
			got = req
			// Stand in for the model: keep the chips, move one.
			return []TranslateResult{{Text: `<1>Klicken Sie </1><2>Aktualisieren</2><1> an.</1>`}}, nil
		},
	}

	src := `<g id="1" ctype="italic">Click </g><g id="2" ctype="bold">Refresh</g>`
	res, err := engine.TranslateTagged(context.Background(), TranslateRequest{
		SourceLang: "en", TargetLang: "de", Texts: []string{src},
	})
	if err != nil {
		t.Fatalf("TranslateTagged: %v", err)
	}

	if got.Texts[0] != `<1>Click </1><2>Refresh</2>` {
		t.Errorf("model saw %q, want chip-encoded source", got.Texts[0])
	}
	if !got.Tagged {
		t.Error("Tagged must be set so the tag contract reaches the prompt")
	}

	want := `<g id="1" ctype="italic">Klicken Sie </g><g id="2" ctype="bold">Aktualisieren</g>` +
		`<g id="1" ctype="italic"> an.</g>`
	if res[0].Text != want {
		t.Errorf("decoded:\n got %q\nwant %q", res[0].Text, want)
	}
}

// A chip the model dropped stays dropped — no repair, no crash. The missing tag
// is then visible to the reviewer and the tag QA rule.
func TestLLMEngine_TranslateTagged_DroppedChip(t *testing.T) {
	engine := LLMEngine{
		Fn: func(_ context.Context, _ TranslateRequest) ([]TranslateResult, error) {
			return []TranslateResult{{Text: `Klicken Sie Aktualisieren an.`}}, nil
		},
	}
	res, err := engine.TranslateTagged(context.Background(), TranslateRequest{
		Texts: []string{`<g id="1" ctype="bold">Click Refresh</g>`},
	})
	if err != nil {
		t.Fatalf("TranslateTagged: %v", err)
	}
	if strings.Contains(res[0].Text, "<g") {
		t.Errorf("dropped chip must not be re-invented, got %q", res[0].Text)
	}
}

// The plain path must not claim to be tagged — an untagged batch would
// otherwise carry the tag contract for no reason.
func TestLLMEngine_Translate_NotTagged(t *testing.T) {
	var got TranslateRequest
	engine := LLMEngine{
		Fn: func(_ context.Context, req TranslateRequest) ([]TranslateResult, error) {
			got = req
			return []TranslateResult{{Text: "hallo"}}, nil
		},
	}
	if _, err := engine.Translate(context.Background(), TranslateRequest{Texts: []string{"hello"}}); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if got.Tagged {
		t.Error("Tagged must stay false on the plain path")
	}
}
