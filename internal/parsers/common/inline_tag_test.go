package common

import (
	"regexp"
	"strings"
	"testing"
)

func TestDetectInlineTags_PlainTextPassThrough(t *testing.T) {
	plain, rich := DetectInlineTags("just plain text, no tags here")
	if plain != "just plain text, no tags here" {
		t.Errorf("plain pass-through: want unchanged, got %q", plain)
	}
	if rich != "" {
		t.Errorf("rich on tag-free input should be empty, got %q", rich)
	}
}

func TestDetectInlineTags_PairedItalic(t *testing.T) {
	plain, rich := DetectInlineTags(`Simplemente di: <i>"Alexa, pon música"</i>, y...`)
	if plain != `Simplemente di: "Alexa, pon música", y...` {
		t.Errorf("plain should strip <i> tags, got %q", plain)
	}
	if !strings.Contains(rich, `<g id="1" ctype="italic">"Alexa, pon música"</g>`) {
		t.Errorf("rich should wrap inner content in <g ctype=italic>, got %q", rich)
	}
}

func TestDetectInlineTags_PairedBold(t *testing.T) {
	plain, rich := DetectInlineTags("Press <b>Enter</b> to confirm")
	if plain != "Press Enter to confirm" {
		t.Errorf("plain: want 'Press Enter to confirm', got %q", plain)
	}
	if !strings.Contains(rich, `ctype="bold">Enter</g>`) {
		t.Errorf("rich: %q", rich)
	}
}

func TestDetectInlineTags_AnchorWithAttributes(t *testing.T) {
	plain, rich := DetectInlineTags(`Click <a href="/foo">here</a> to continue`)
	if plain != "Click here to continue" {
		t.Errorf("plain: %q", plain)
	}
	if !strings.Contains(rich, `ctype="x-html-a"`) || !strings.Contains(rich, `>here</g>`) {
		t.Errorf("rich should wrap 'here' in <g ctype=x-html-a>: %q", rich)
	}
	// The href has to be carried on the chip, or export
	// can't put it back.
	if !strings.Contains(rich, `original_markup="&lt;a href=&quot;/foo&quot;&gt;"`) {
		t.Errorf("rich should preserve the anchor's href: %q", rich)
	}
}

func TestDetectInlineTags_VoidElements(t *testing.T) {
	cases := []struct {
		in   string
		want string // expected rich snippet
	}{
		{"line one<br>line two", `<x id="1" ctype="lb" original_tag="br"/>`},
		{"line one<br/>line two", `<x id="1" ctype="lb" original_tag="br"/>`},
		{"line one<br />line two", `<x id="1" ctype="lb" original_tag="br"/>`},
		{"section<hr/>break", `<x id="1" ctype="lb" original_tag="hr"/>`},
	}
	for _, c := range cases {
		_, rich := DetectInlineTags(c.in)
		if !strings.Contains(rich, c.want) {
			t.Errorf("input %q: expected rich to contain %q, got %q", c.in, c.want, rich)
		}
	}
}

// Critical: TipTap inline-mark tags MUST NOT chip — they're applied
// via Mod+B / Mod+I and need to round-trip as formatting marks.
func TestDetectInlineTags_TipTapMarksNotChipped(t *testing.T) {
	cases := []string{
		"This is <strong>bold</strong>",
		"This is <em>italic</em>",
		"H<sub>2</sub>O",
		"x<sup>2</sup>",
	}
	for _, c := range cases {
		plain, rich := DetectInlineTags(c)
		if plain != c {
			t.Errorf("input %q: plain should be unchanged, got %q", c, plain)
		}
		if rich != "" {
			t.Errorf("input %q: rich should be empty (no chip), got %q", c, rich)
		}
	}
}

func TestXLIFFInlineToHTML_RoundTrip(t *testing.T) {
	cases := []struct {
		original string
		via      string // expected after round-trip via DetectInlineTags + XLIFFInlineToHTML
	}{
		{"plain text", "plain text"},
		// Pins that an <a> chip exports with its href.
		// `Click <a>here</a>` is wrong here — a dropped href breaks every
		// link in a translated HTML file.
		{`Click <a href="/foo">here</a>`, `Click <a href="/foo">here</a>`},
		{`See <span class="hl">this</span>`, `See <span class="hl">this</span>`},
		{`Press <b>Enter</b> now`, `Press <b>Enter</b> now`},
		{`one<br>two`, "one<br>two"},
		{`one<br/>two`, "one<br>two"},
	}
	for _, c := range cases {
		_, rich := DetectInlineTags(c.original)
		if rich == "" {
			// Only the tag-free case may skip the chip; it checks the
			// XLIFFInlineToHTML pass-through.
			if c.original != "plain text" {
				t.Errorf("no chip detected for %q", c.original)
				continue
			}
			if got := XLIFFInlineToHTML(c.original); got != c.via {
				t.Errorf("pass-through round-trip for %q: want %q, got %q", c.original, c.via, got)
			}
			continue
		}
		got := XLIFFInlineToHTML(rich)
		if got != c.via {
			t.Errorf("round-trip for %q: rich=%q, want %q, got %q", c.original, rich, c.via, got)
		}
	}
}

// Nested HTML: <a href="x"><b>foo</b></a> — inner first, then outer.
func TestDetectInlineTags_Nested(t *testing.T) {
	plain, rich := DetectInlineTags(`<a href="/x"><b>foo</b></a>`)
	if plain != "foo" {
		t.Errorf("nested plain: want 'foo', got %q", plain)
	}
	// Both wrappers must be present (in either order).
	if !strings.Contains(rich, `ctype="bold">foo</g>`) {
		t.Errorf("nested rich: missing bold wrapper, got %q", rich)
	}
	if !strings.Contains(rich, `ctype="x-html-a"`) {
		t.Errorf("nested rich: missing anchor wrapper, got %q", rich)
	}
}

// Multiple tag instances — all get distinct ids.
func TestDetectInlineTags_MultipleSameTag(t *testing.T) {
	_, rich := DetectInlineTags("Say <i>hello</i>, then <i>goodbye</i>")
	// Two distinct id="N" values for the two <i> wrappers.
	m := regexp.MustCompile(`<g id="(\d+)" ctype="italic"`).FindAllStringSubmatch(rich, -1)
	if len(m) != 2 {
		t.Fatalf("expected 2 italic wrappers, got %q", rich)
	}
	if m[0][1] == m[1][1] {
		t.Errorf("both italic wrappers have id=%q, want distinct ids: %q", m[0][1], rich)
	}
}

// TestChipOriginalMarkupOnlyWhenAttributesExist pins the size optimization.
// Emitting original_markup unconditionally would add a redundant copy of the
// element name to every bold/italic chip in every stored segment — the ctype
// already says `<b>`. Only tags carrying attributes have anything to preserve.
func TestChipOriginalMarkupOnlyWhenAttributesExist(t *testing.T) {
	_, withAttrs := DetectInlineTags(`Click <a href="/foo">here</a>`)
	if !strings.Contains(withAttrs, "original_markup=") {
		t.Errorf("an <a href> chip must carry original_markup: %q", withAttrs)
	}

	_, bare := DetectInlineTags(`Press <b>Enter</b>`)
	if strings.Contains(bare, "original_markup=") {
		t.Errorf("a bare <b> chip must NOT carry original_markup: %q", bare)
	}
}

// TestXLIFFInlineToHTMLWithoutOriginalMarkup pins that chips without
// original_markup export as a bare tag, so existing rich text stays
// exportable.
func TestXLIFFInlineToHTMLWithoutOriginalMarkup(t *testing.T) {
	legacy := `Click <g id="1" ctype="x-html-a">here</g> to continue`
	if got, want := XLIFFInlineToHTML(legacy), `Click <a>here</a> to continue`; got != want {
		t.Errorf("legacy rich text = %q, want %q", got, want)
	}
}

// TestXLIFFInlineToHTMLIgnoresOriginalMarkupInContent — the attribute lookup is
// scoped to the opening tag, so a translator who types the literal string
// `original_markup="…"` into their target can't hijack the restored tag.
func TestXLIFFInlineToHTMLIgnoresOriginalMarkupInContent(t *testing.T) {
	rich := `<g id="1" ctype="bold">original_markup="&lt;script&gt;"</g>`
	got := XLIFFInlineToHTML(rich)
	if strings.Contains(got, "<script>") {
		t.Errorf("content must not be treated as an attribute: %q", got)
	}
	if want := `<b>original_markup="&lt;script&gt;"</b>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestOriginalMarkupEscapingRoundTrip — attribute values carry quotes and
// ampersands; both have to survive the escape/unescape cycle intact, or an
// href with a query string comes back mangled.
func TestOriginalMarkupEscapingRoundTrip(t *testing.T) {
	original := `<a href="https://x.test/?a=1&b=2" title="He said &quot;hi&quot;">`
	attr := ChipOriginalMarkup(original, 1)
	rich := `<g id="1" ctype="x-html-a"` + attr + `>text</g>`

	got := XLIFFInlineToHTML(rich)
	if !strings.Contains(got, `href="https://x.test/?a=1&b=2"`) {
		t.Errorf("query-string href did not survive: %q", got)
	}
}
