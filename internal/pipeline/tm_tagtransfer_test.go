package pipeline

import (
	"strings"
	"testing"
)

func TestTMTargetRichText(t *testing.T) {
	const srcRich = `Fill the tank with H<g id="1" ctype="x-html-sub">2</g>O before the first run.`
	const srcPlain = `Fill the tank with H2O before the first run.`
	const tgtPlain = `Remplissez le réservoir avec H2O avant la première utilisation.`
	const want = `Remplissez le réservoir avec H<g id="1" ctype="x-html-sub">2</g>O avant la première utilisation.`

	t.Run("projects source tags onto a plain TM target", func(t *testing.T) {
		if got := tmTargetRichText(srcRich, srcPlain, tgtPlain, ""); got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})

	t.Run("remaps a TMX-tagged TM target onto the source's tags", func(t *testing.T) {
		const tgtRich = `Remplissez le réservoir avec H<bpt i="1" x="1">&lt;1&gt;</bpt>2<ept i="1">&lt;/1&gt;</ept>O avant la première utilisation.`
		if got := tmTargetRichText(srcRich, srcPlain, tgtPlain, tgtRich); got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})

	t.Run("tag-free source yields nothing", func(t *testing.T) {
		if got := tmTargetRichText("", "Hello", "Bonjour", ""); got != "" {
			t.Errorf("want empty, got %q", got)
		}
	})
}

// Our parser makes each ST4 <meta-ref> a paired <g>, while a TMX export carries
// the same construct as a standalone <ph> placeholder. Three logical tags on
// each side, but 6 vs 4 raw tags — the remap must not be fooled by the raw count.
func TestTMTargetRichText_PairVsPlaceholder(t *testing.T) {
	const srcRich = `For H<g id="1" ctype="x-html-sub">2</g>O, the flow rate can be set to <g id="2" ctype="x-xml-meta-ref">Low</g> or <g id="3" ctype="x-xml-meta-ref">High</g>.`
	const srcPlain = `For H2O, the flow rate can be set to Low or High.`
	const tgtPlain = `Für H2O kann die Durchflussrate auf  oder  eingestellt werden.`
	const tgtRich = `Für H<bpt i="1" x="1">&lt;1&gt;</bpt>2<ept i="1">&lt;/1&gt;</ept>O kann die Durchflussrate auf <ph x="2">&lt;2/&gt;</ph> oder <ph x="3">&lt;3/&gt;</ph> eingestellt werden.`

	got := tmTargetRichText(srcRich, srcPlain, tgtPlain, tgtRich)
	t.Logf("got: %q", got)

	if got == "" {
		t.Fatal("no rich text produced — the tags were dropped")
	}
	if strings.Contains(got, "bpt") || strings.Contains(got, "<ph") {
		t.Errorf("foreign TMX markup survived: %q", got)
	}
	if !strings.Contains(got, `<g id="1" ctype="x-html-sub">2</g>`) {
		t.Errorf("pair→pair should keep the TM's text: %q", got)
	}
	// A placeholder's payload is its display form, not translatable text —
	// carrying it would write a literal "<2/>" into the exported document.
	if strings.Contains(got, "2/") {
		t.Errorf("placeholder display payload leaked into the target: %q", got)
	}
	if !strings.Contains(got, `<g id="2" ctype="x-xml-meta-ref"></g>`) {
		t.Errorf("placeholder should map to an empty source pair: %q", got)
	}
}
