package parser

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// legacyLinksSource has an inline element (<small>) that is a chip in the
// run's numbering but not in the numbering that counts only formatting
// elements and line breaks, ahead of two links that differ only in href.
const legacyLinksSource = `<html><body><p><small>N</small> <a href="/x">X</a> and <a href="/y">Y</a>.</p></body></html>`

// legacyLinksRich is that block's source rich text where only formatting
// elements and line breaks are chips, as it is stored for segments read that
// way.
const legacyLinksRich = `N <g id="1" ctype="x-html-a" original_markup="&lt;a href=&quot;/x&quot;&gt;">X</g> and <g id="2" ctype="x-html-a" original_markup="&lt;a href=&quot;/y&quot;&gt;">Y</g>.`

// legacyLinksText is that block's stored source text.
const legacyLinksText = "N X and Y."

func mustParseHTML(t *testing.T, src string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func reconstructHTML(t *testing.T, p Parser, src, key, translated string) string {
	t.Helper()
	out, err := p.Reconstruct([]byte(src), map[string]string{key: translated})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	return string(out)
}

func wantHTMLContains(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Fatalf("export:\n%s\nwant it to contain:\n%s", out, want)
	}
}

// The formatting-only rich text this test file stores is what the parser's
// own reading of a block in that numbering gives, so the cases below test the
// real stored form.
func TestHTMLFormattingOnlyRichTextOfABlock(t *testing.T) {
	segs, err := HTML{}.Parse([]byte(legacyLinksSource))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 || segs[0].RichText == legacyLinksRich {
		t.Fatalf("the run's own rich text must differ from the formatting-only one: %+v", segs)
	}
	units := collectHTMLUnits(mustParseHTML(t, legacyLinksSource), 0)
	if _, got, _ := formattingOnlyChips(units[0].block); got != legacyLinksRich {
		t.Fatalf("formatting-only rich text = %q, want %q", got, legacyLinksRich)
	}
}

// A translation whose segment stores the formatting-only source rich text
// reads every chip by that numbering, so reordered links keep their own hrefs
// even where a chip id would fit an element in the run's numbering.
func TestHTMLStoredFormattingOnlyChipsKeepTheirElements(t *testing.T) {
	p := HTMLWithStoredSource(map[string]StoredSource{"seg:1": {Text: legacyLinksText, RichText: legacyLinksRich}})
	stored := `<g id="2" ctype="x-html-a">Ye</g> y <g id="1" ctype="x-html-a">Equis</g>.`
	out := reconstructHTML(t, p, legacyLinksSource, "seg:1", stored)
	wantHTMLContains(t, out, `<p><a href="/y">Ye</a> y <a href="/x">Equis</a>.</p>`)

	// A chip the source never had is no reason to read the others otherwise.
	stray := `<g id="1" ctype="x-html-a">Equis</g><x id="9" ctype="lb"/>y <g id="2" ctype="x-html-a">Ye</g>.`
	out = reconstructHTML(t, p, legacyLinksSource, "seg:1", stray)
	wantHTMLContains(t, out, `<a href="/x">Equis</a><br/>y <a href="/y">Ye</a>.`)
}

// A segment stored with chips that carry no original_markup is read the same
// way: chip ids, types and text decide, not the markup copy.
func TestHTMLStoredChipsWithoutMarkupCopyKeepTheirElements(t *testing.T) {
	bare := `N <g id="1" ctype="x-html-a">X</g> and <g id="2" ctype="x-html-a">Y</g>.`
	p := HTMLWithStoredSource(map[string]StoredSource{"seg:1": {Text: legacyLinksText, RichText: bare}})
	stored := `<g id="2" ctype="x-html-a">Ye</g> y <g id="1" ctype="x-html-a">Equis</g>.`
	wantHTMLContains(t, reconstructHTML(t, p, legacyLinksSource, "seg:1", stored), `<p><a href="/y">Ye</a> y <a href="/x">Equis</a>.</p>`)
}

// A block stored as one segment that the parser splits into several (a list item
// holding a list) gets its stored translation over the whole block, as one
// piece: no source text of the later pieces is left beside it, and no link
// is written twice.
func TestHTMLStoredWholeBlockTranslationReplacesTheBlock(t *testing.T) {
	src := `<html><body><ul><li>Intro <a href="/a">A</a><ul><li>Sub</li></ul>Tail</li></ul></body></html>`
	storedSource := `Intro <g id="1" ctype="x-html-a" original_markup="&lt;a href=&quot;/a&quot;&gt;">A</g>SubTail`
	p := HTMLWithStoredSource(map[string]StoredSource{"seg:1": {Text: "Intro ASubTail", RichText: storedSource}})
	out := reconstructHTML(t, p, src, "seg:1", `Intro ES <g id="1" ctype="x-html-a">A</g> Sub ES Tail ES`)
	wantHTMLContains(t, out, `<ul><li>Intro ES <a href="/a">A</a> Sub ES Tail ES</li></ul>`)
	if strings.Count(out, `href="/a"`) != 1 || strings.Contains(out, ">Sub<") || strings.Contains(out, "Tail<") {
		t.Fatalf("source text or a duplicate link left beside the translation:\n%s", out)
	}
}

// A translation whose segment stores the run's own source, or that comes
// without a stored source, reads the run's numbering.
func TestHTMLRunNumberingStaysForCurrentTranslations(t *testing.T) {
	segs, err := HTML{}.Parse([]byte(legacyLinksSource))
	if err != nil {
		t.Fatal(err)
	}
	current := `<g id="1" ctype="x-html-small">N</g> <g id="3" ctype="x-html-a">Ye</g> y <g id="2" ctype="x-html-a">Equis</g>.`
	want := `<p><small>N</small> <a href="/y">Ye</a> y <a href="/x">Equis</a>.</p>`
	for name, p := range map[string]Parser{
		"stored run source": HTMLWithStoredSource(map[string]StoredSource{"seg:1": {Text: segs[0].Text, RichText: segs[0].RichText}}),
		"no stored source":  HTML{},
	} {
		t.Run(name, func(t *testing.T) {
			wantHTMLContains(t, reconstructHTML(t, p, legacyLinksSource, "seg:1", current), want)
		})
	}
}

// A block stored as one segment with no chips at all, that the parser splits
// into several (paragraphs in a quote), is told apart by its stored text: the
// stored translation replaces the whole block and no source text is left. A
// one-paragraph block stored the same way as the run keeps the run.
func TestHTMLStoredBlockWithoutChipsReplacesTheBlock(t *testing.T) {
	src := `<html><body><blockquote><p>A</p><p>B</p></blockquote></body></html>`
	p := HTMLWithStoredSource(map[string]StoredSource{"seg:1": {Text: "AB"}})
	out := reconstructHTML(t, p, src, "seg:1", "TA TB")
	wantHTMLContains(t, out, `<blockquote>TA TB</blockquote>`)
	if strings.Contains(out, "<p>B</p>") {
		t.Fatalf("source text left beside the translation:\n%s", out)
	}

	single := `<html><body><blockquote><p>Hello</p></blockquote></body></html>`
	p = HTMLWithStoredSource(map[string]StoredSource{"seg:1": {Text: "Hello"}})
	wantHTMLContains(t, reconstructHTML(t, p, single, "seg:1", "Hola"), `<blockquote><p>Hola</p></blockquote>`)
}

// A block holding one paragraph in indented markup (as editors and CMSs
// write it) is still one piece: its stored translation fills the paragraph,
// which keeps its element and attributes, with or without chips.
func TestHTMLStoredIndentedSingleParagraphKeepsIt(t *testing.T) {
	src := "<html><body><table><tr><td>\n  <p class=\"note\">Hello</p>\n</td></tr></table></body></html>"
	p := HTMLWithStoredSource(map[string]StoredSource{"seg:1": {Text: "\n  Hello\n"}})
	wantHTMLContains(t, reconstructHTML(t, p, src, "seg:1", "Hola"), "<td>\n  <p class=\"note\">Hola</p>\n</td>")

	chips := "<html><body><table><tr><td>\n  <p class=\"note\"><small>N</small> <a href=\"/x\">X</a> and <a href=\"/y\">Y</a>.</p>\n</td></tr></table></body></html>"
	stored := StoredSource{Text: "\n  N X and Y.\n", RichText: "\n  " + legacyLinksRich + "\n"}
	p = HTMLWithStoredSource(map[string]StoredSource{"seg:1": stored})
	out := reconstructHTML(t, p, chips, "seg:1", `<g id="2" ctype="x-html-a">Ye</g> y <g id="1" ctype="x-html-a">Equis</g>.`)
	wantHTMLContains(t, out, "<td>\n  <p class=\"note\"><a href=\"/y\">Ye</a> y <a href=\"/x\">Equis</a>.</p>\n</td>")

	// Line breaks inside the paragraph too: the stored source holds them raw,
	// the run holds them collapsed, and it is still the same text.
	wrapped := "<html><body><table><tr><td>\n  <p class=\"note\">Plug the WAN\n    cable in.</p>\n</td></tr></table></body></html>"
	p = HTMLWithStoredSource(map[string]StoredSource{"seg:1": {Text: "\n  Plug the WAN\n    cable in.\n"}})
	wantHTMLContains(t, reconstructHTML(t, p, wrapped, "seg:1", "Conecte el cable WAN."), "<td>\n  <p class=\"note\">Conecte el cable WAN.</p>\n</td>")
}

// A legacy block whose one run holds only part of its text or chips (the rest
// sits in a <pre>, an untranslated block, or a line break outside the run)
// gets its stored translation over the whole block, so nothing is written
// twice.
func TestHTMLStoredBlockWithTextOutsideTheRunReplacesTheBlock(t *testing.T) {
	for name, c := range map[string]struct{ src, text, rich, translated, want, gone string }{
		"pre": {
			src:  `<html><body><ul><li>Run this:<pre>npm install</pre></li></ul></body></html>`,
			text: "Run this:npm install", translated: "Ejecute esto:npm install",
			want: `<li>Ejecute esto:npm install</li>`, gone: "<pre>",
		},
		"untranslated block": {
			src:  `<html><body><ul><li>Hi <div translate="no">Acme</div></li></ul></body></html>`,
			text: "Hi Acme", translated: "Hola Acme",
			want: `<li>Hola Acme</li>`, gone: `translate="no"`,
		},
		"line break outside the run": {
			src:  `<html><body><table><tr><td><br><p>Hello</p></td></tr></table></body></html>`,
			text: "\nHello", rich: `<x id="1" ctype="lb"/>Hello`, translated: `<x id="1" ctype="lb"/>Hola`,
			want: `<td><br/>Hola</td>`, gone: "<p>",
		},
		"non-breaking space outside the run": {
			src:  `<html><body><table><tr><td>&nbsp;<p>Hello</p></td></tr></table></body></html>`,
			text: "\u00a0Hello", translated: "\u00a0Hola",
			want: "<td>\u00a0Hola</td>", gone: "<p>",
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := HTMLWithStoredSource(map[string]StoredSource{"seg:1": {Text: c.text, RichText: c.rich}})
			out := reconstructHTML(t, p, c.src, "seg:1", c.translated)
			wantHTMLContains(t, out, c.want)
			if strings.Contains(out, c.gone) {
				t.Fatalf("source left beside the translation:\n%s", out)
			}
		})
	}
}

// An element marked translate="no" that a stored translation carries as a
// paired chip comes back whole, attributes and text as in the source.
func TestHTMLStoredChipOverUntranslatedElementKeepsIt(t *testing.T) {
	src := `<html><body><p>Use <span translate="no" class="brand">Acme <b>Pro</b></span> today.</p></body></html>`
	stored := `Use <g id="1" ctype="x-html-span" original_markup="&lt;span translate=&quot;no&quot; class=&quot;brand&quot;&gt;">Acme <g id="2" ctype="bold">Pro</g></g> today.`
	units := collectHTMLUnits(mustParseHTML(t, src), 0)
	if _, got, _ := formattingOnlyChips(units[0].block); got != stored {
		t.Fatalf("formatting-only rich text = %q, want %q", got, stored)
	}
	p := HTMLWithStoredSource(map[string]StoredSource{"seg:1": {Text: "Use Acme Pro today.", RichText: stored}})
	out := reconstructHTML(t, p, src, "seg:1", `Usa <g id="1" ctype="x-html-span">Acme <g id="2" ctype="bold">Pro</g></g> hoy.`)
	wantHTMLContains(t, out, `<p>Usa <span translate="no" class="brand">Acme <b>Pro</b></span> hoy.</p>`)
}
