package parser

import (
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// In a text format (JSON, PO, TXT, XLSX) whose strings are HTML, the export
// writes a translation's text as is, except that a tag survives only when the
// source string has the same tag as a browser reads it: same element, same
// attributes once decoded. Any other tag, comment or doctype is written
// escaped, as text. Both strings are read with the HTML tokenizer browsers
// follow, so what counts as a tag here is what counts as one in the
// customer's page.

// tagTextEscaper writes a tag's angle brackets as entities.
var tagTextEscaper = strings.NewReplacer("<", "&lt;", ">", "&gt;")

// neverReused are elements a translation never writes, even when the source
// has one: their content is code or a document, or a browser reads it as raw
// text, where a later source tag could end the element early.
var neverReused = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true, "embed": true,
	"title": true, "textarea": true, "xmp": true, "noscript": true, "noembed": true,
	"noframes": true, "plaintext": true,
}

// chipElements are the elements a chip turns back into. A chip stored
// without its attributes reads as the bare element, which carries nothing
// the source's element does not.
var chipElements = func() map[string]bool {
	set := map[string]bool{}
	for _, el := range inlineCtypeToHTMLMap {
		set[el] = true
	}
	return set
}()

// lineBreakKey is the line break. It is always allowed: Shift+Enter in a
// segment with chips saves one, and it carries nothing but a break.
var lineBreakKey = startTagKey("br", nil)

// htmlToken is one token of an HTML string: text, or markup — a tag, comment
// or doctype — with the key it is matched by ("" never matches).
type htmlToken struct {
	raw     string
	markup  bool
	cut     bool // markup the end of the string cut off before its '>'
	key     string
	element string // element name of a start tag
}

// htmlTokens splits s into tokens as a browser does. With rawText false the
// content of a raw-text element (script, title…) is read as markup too, so a
// refused <title> cannot hide a tag after it from the check. Markup with no
// '>' — a '<' that never closes, at the end of s — is cut.
func htmlTokens(s string, rawText bool) []htmlToken {
	z := html.NewTokenizer(strings.NewReader(s))
	var toks []htmlToken
	for {
		tt := z.Next()
		raw := string(z.Raw()) // copied before the accessors below decode in place
		tok := htmlToken{raw: raw, markup: tt != html.TextToken}
		switch tt {
		case html.ErrorToken:
			// The tokenizer reads a string, so the error is the end of it;
			// raw holds a tag cut short there.
			if raw != "" {
				toks = append(toks, htmlToken{raw: raw, markup: true, cut: !strings.Contains(raw, ">")})
			}
			return toks
		case html.StartTagToken, html.SelfClosingTagToken:
			name, attrs := tagNameAttrs(z)
			if !rawText {
				z.NextIsNotRawText()
			}
			if !neverReused[name] {
				tok.key, tok.element = startTagKey(name, attrs), name
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); !neverReused[string(name)] {
				tok.key = "</" + string(name)
			}
		case html.CommentToken:
			switch {
			case strings.HasPrefix(raw, "<!--") && !strings.HasSuffix(raw, "-->") && !strings.HasSuffix(raw, "--!>"):
				// Left open by the end of the string.
				tok.cut = true
			case !strings.HasPrefix(raw, "<![CDATA["):
				// A CDATA section is text to a browser inside SVG, ending
				// later than the tokenizer's comment: never reused.
				tok.key = "!" + raw
			}
		}
		if tok.markup && !strings.Contains(raw, ">") {
			tok.cut = true
		}
		toks = append(toks, tok)
	}
}

// tagNameAttrs reads the current tag's name and attributes, decoded. The
// tokenizer keeps the first of a repeated attribute, as a browser does.
func tagNameAttrs(z *html.Tokenizer) (string, [][2]string) {
	name, more := z.TagName()
	var attrs [][2]string
	for more {
		var k, v []byte
		k, v, more = z.TagAttr()
		attrs = append(attrs, [2]string{string(k), string(v)})
	}
	return string(name), attrs
}

// startTagKey identifies a start tag by its element and decoded attributes,
// in any order and however they are quoted.
func startTagKey(name string, attrs [][2]string) string {
	sort.Slice(attrs, func(i, j int) bool { return attrs[i][0] < attrs[j][0] })
	var b strings.Builder
	b.WriteString("<" + name)
	for _, a := range attrs {
		b.WriteString("\x00" + a[0] + "\x00" + a[1])
	}
	return b.String()
}

// sourceTagSet maps the key of each piece of markup a translation may write
// to the form it is written in: every tag and comment of the source but the
// neverReused elements, as the source writes it; an end tag bare, since a
// browser ignores its attributes; the bare start tag of each chip element the
// source has; the line break; and each '<…>' run in the source's text. Only
// the source's own bytes reach the export, so nothing can ride along on a
// matching tag (an ignored repeated attribute, an end tag's attributes).
func sourceTagSet(source string) map[string]string {
	set := map[string]string{lineBreakKey: "<br>"}
	for _, tok := range htmlTokens(source, true) {
		if !tok.markup {
			for rest := tok.raw; ; {
				run, next, ok := textAngleRun(rest)
				if !ok {
					break
				}
				if run != "" {
					set[textRunKey(run)] = run
				}
				rest = next
			}
			continue
		}
		if tok.key == "" || tok.cut {
			continue
		}
		form := tok.raw
		if strings.HasPrefix(tok.key, "</") {
			form = tok.key + ">"
		}
		if _, ok := set[tok.key]; !ok || tok.key == lineBreakKey {
			set[tok.key] = form
		}
		if chipElements[tok.element] {
			if bare := startTagKey(tok.element, nil); set[bare] == "" {
				set[bare] = "<" + tok.element + ">"
			}
		}
	}
	return set
}

// textAngleRun finds the first '<' of text s: run is the '<…>' run it starts
// ("" when no '>' follows) and next the text after the run, or after the '<'
// alone. ok is false when s has no '<'.
func textAngleRun(s string) (run, next string, ok bool) {
	lt := strings.IndexByte(s, '<')
	if lt < 0 {
		return "", s, false
	}
	if gt := strings.IndexByte(s[lt:], '>'); gt >= 0 {
		return s[lt : lt+gt+1], s[lt+gt+1:], true
	}
	return "", s[lt+1:], true
}

// textRunKey is the key of a '<…>' run of text.
func textRunKey(run string) string { return "t" + run }

// writeText writes text with every '<' as an entity, but a '<…>' run the
// source has: a '<' that is no tag to a browser can become one once the
// customer's app fills in a placeholder or joins strings.
func writeText(b *strings.Builder, text string, allowed map[string]string) {
	for {
		lt := strings.IndexByte(text, '<')
		if lt < 0 {
			b.WriteString(text)
			return
		}
		b.WriteString(text[:lt])
		text = text[lt:]
		if run, next, _ := textAngleRun(text); run != "" && allowed[textRunKey(run)] != "" {
			b.WriteString(run)
			text = next
			continue
		}
		b.WriteString("&lt;")
		text = text[1:]
	}
}

// filterTags writes markup with each markup token allowed has in allowed's
// form and every other one escaped, and returns those in order. Text is written as is but for
// its '<' (writeText). Cut markup is not reported, but each '<' in it is
// written as an entity, so markup after the string in the customer's page cannot close
// it; the same holds for a '<' in text.
func filterTags(markup string, allowed map[string]string) (string, []string) {
	var b strings.Builder
	var foreign []string
	for _, tok := range htmlTokens(markup, false) {
		if tok.cut {
			b.WriteString(strings.ReplaceAll(tok.raw, "<", "&lt;"))
			continue
		}
		if !tok.markup {
			writeText(&b, tok.raw, allowed)
			continue
		}
		if form := allowed[tok.key]; tok.key != "" && form != "" {
			b.WriteString(form)
			continue
		}
		b.WriteString(tagTextEscaper.Replace(tok.raw))
		foreign = append(foreign, tok.raw)
	}
	return b.String(), foreign
}

// inlineExport turns a stored translation back into the inline HTML a text
// format writes for a string whose source is source, and returns the tags it
// writes as text. Each chip's start tag comes from the source's chip of the
// same id; a tag the source does not have is escaped.
func inlineExport(translated, source string) (string, []string) {
	_, sourceRich := DetectInlineTags(source)
	return filterTags(XLIFFInlineToHTMLFromSource(translated, sourceRich), sourceTagSet(source))
}

// sourceInlineMarkup is the string the export writes for translated.
func sourceInlineMarkup(translated, source string) string {
	out, _ := inlineExport(translated, source)
	return out
}

// ForeignTags returns, in order, the tags the export writes as text for
// translated, a stored translation as TextFormatTranslation picks it, whose
// source string is source.
func ForeignTags(translated, source string) []string {
	_, foreign := inlineExport(translated, source)
	return foreign
}
