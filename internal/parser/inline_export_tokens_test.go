package parser

import (
	"strings"
	"testing"
)

// A source tag is kept as a browser reads it: a raw '&' in a quoted URL, an
// entity in the text, a valueless or unquoted attribute, an autolink, a
// comment holding '>' and i18next's numbered tags all come back as written.
func TestSourceInlineMarkupKeepsSourceTagsAsBrowserReadsThem(t *testing.T) {
	for _, c := range []struct{ source, translated string }{
		{`Go <a href="https://example.com/?a=1&b=2">here</a>`, `Gehe <a href="https://example.com/?a=1&b=2">hierhin</a>`},
		{`Price:&nbsp;10&nbsp;€ &amp; more`, `Preis:&nbsp;10&nbsp;€ &amp; mehr`},
		{`<input type="checkbox" checked> Remember me`, `<input type="checkbox" checked> Angemeldet bleiben`},
		{`Contact <support@example.com>`, `Kontakt <support@example.com>`},
		{`See <https://example.com>`, `Siehe <https://example.com>`},
		{`Hi <!-- a > b --> there`, `Hallo <!-- a > b --> dort`},
		{`<a href="/x" target=_blank>Open</a>`, `<a href="/x" target=_blank>Öffnen</a>`},
		{`<a href="/help" title="What's new">news</a>`, `<a href="/help" title="What's new">Neues</a>`},
		{`<0>Hello</0> <1/> world`, `<0>Hallo</0> <1/> Welt`},
	} {
		if out := sourceInlineMarkup(c.translated, c.source); out != c.translated {
			t.Errorf("source %q: export changed\n %q\nto\n %q", c.source, c.translated, out)
		}
		if foreign := ForeignTags(c.translated, c.source); len(foreign) != 0 {
			t.Errorf("source %q: reported %q as foreign", c.source, foreign)
		}
	}
}

// The same tag written another way — attributes in another order, other
// quotes, an entity for a character — is the source's tag, and is written as
// the source writes it.
func TestSourceInlineMarkupMatchesTagsByMeaning(t *testing.T) {
	const source = `<a href="/x?a=1&amp;b=2" title='Hi'>x</a>`
	const translated = `<a title="Hi" href="/x?a=1&b=2">y</a>`
	if out := sourceInlineMarkup(translated, source); out != `<a href="/x?a=1&amp;b=2" title='Hi'>y</a>` {
		t.Errorf("export wrote %q, want the source's tag", out)
	}
	if foreign := ForeignTags(translated, source); len(foreign) != 0 {
		t.Errorf("reported %q as foreign", foreign)
	}
}

// A tag the source lacks is text even where a browser would read what follows
// it as raw text, so nothing inside a refused <title> or <textarea> becomes
// markup; an allowed one does not hide what follows it either.
func TestSourceInlineMarkupEscapesInsideRawTextElements(t *testing.T) {
	for _, c := range []struct{ source, translated string }{
		{`Hello`, `<title><img src=x onerror=alert(1)></title>`},
		{`<textarea>x</textarea>`, `<textarea><img src=x onerror=alert(1)></textarea>`},
		{`<svg><title>x</title></svg>`, `<svg><title><img src=x onerror=alert(1)></title></svg>`},
	} {
		out := sourceInlineMarkup(c.translated, c.source)
		if strings.Contains(out, "<img") {
			t.Errorf("source %q: <img reached the export: %q", c.source, out)
		}
		if foreign := ForeignTags(c.translated, c.source); len(foreign) == 0 {
			t.Errorf("source %q: ForeignTags reported nothing", c.source)
		}
	}
}

// What ForeignTags reports is exactly what the export writes as text.
func TestForeignTagsMatchesExport(t *testing.T) {
	const source = `Click <a href="https://example.com/">here</a>`
	const translated = `Klick <a href="https://example.com/">hier</a> <b>x</b> <!-- c --> <img src=y> a<b`
	out := sourceInlineMarkup(translated, source)
	foreign := ForeignTags(translated, source)
	want := []string{"<b>", "</b>", "<!-- c -->", "<img src=y>"}
	if strings.Join(foreign, "|") != strings.Join(want, "|") {
		t.Errorf("ForeignTags = %q, want %q", foreign, want)
	}
	for _, tag := range foreign {
		if !strings.Contains(out, tagTextEscaper.Replace(tag)) {
			t.Errorf("%q is reported but not written as text: %q", tag, out)
		}
	}
}

// A '<' in text that is no tag to a browser can become one once the app fills
// in a placeholder or joins strings, so it is written as an entity, unless the
// source has the same '<…>' run (i18next's <0>, the source's own <{{name}}>).
// It is not refused at confirm.
func TestSourceInlineMarkupEscapesTextAngleBrackets(t *testing.T) {
	for _, c := range []struct{ source, translated, want string }{
		{`Hello {{name}}`, `Hallo <{{name}}>`, `Hallo &lt;{{name}}>`},
		{`Hello %s`, `Hallo <%s>`, `Hallo &lt;%s>`},
		{`Hello <{{name}}>`, `Hallo <{{name}}>`, `Hallo <{{name}}>`},
		{`Hello <%s>`, `Hallo <%s> und <%d>`, `Hallo <%s> und &lt;%d>`},
		{`<0>Hello</0> <1/>`, `<0>Hallo</0> <2/>`, `<0>Hallo</0> &lt;2/>`},
	} {
		if out := sourceInlineMarkup(c.translated, c.source); out != c.want {
			t.Errorf("source %q: export wrote %q, want %q", c.source, out, c.want)
		}
		if foreign := ForeignTags(c.translated, c.source); len(foreign) != 0 {
			t.Errorf("source %q: reported %q as foreign", c.source, foreign)
		}
	}
}

// A matching tag is written in the source's form, so nothing rides along on
// it: an end tag's attributes and a repeated attribute (a browser ignores
// both) are dropped.
func TestSourceInlineMarkupWritesNothingExtraOnSourceTags(t *testing.T) {
	const source = `Use <b>bold</b> and <a href="/x">a link</a>`
	out := sourceInlineMarkup(`Nutze <b>fett</b x="<img src=x onerror=alert(1)>"> und <a href="/x" href="<img src=y>">Link</a>`, source)
	if strings.Contains(out, "<img") {
		t.Errorf("a tag rode along on a source tag: %q", out)
	}
	if want := `Nutze <b>fett</b> und <a href="/x">Link</a>`; out != want {
		t.Errorf("export wrote %q, want %q", out, want)
	}
}

// A CDATA section is a comment to the tokenizer but text to a browser inside
// SVG, so it is never reused; a comment the end of the string leaves open is
// never reused either, even when the source ends the same way.
func TestSourceInlineMarkupNeverReusesCDATAOrOpenComment(t *testing.T) {
	for _, c := range []struct{ source, translated, want string }{
		{`<svg><![CDATA[x>`, `<svg><![CDATA[x>`, `<svg>&lt;![CDATA[x&gt;`},
		{`Hi <!-- a > b`, `Hallo <!-- a > b`, `Hallo &lt;!-- a > b`},
	} {
		if out := sourceInlineMarkup(c.translated, c.source); out != c.want {
			t.Errorf("source %q: export wrote %q, want %q", c.source, out, c.want)
		}
	}
}
