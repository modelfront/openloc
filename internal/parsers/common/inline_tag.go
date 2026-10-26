package common

import (
	"fmt"
	"regexp"
	"strings"
)

// Shared inline-tag detection for non-HTML parsers (JSON, TXT, XLSX,
// Android XML, PO). When their text content embeds HTML inline markup, we
// detect it at parse time and emit XLIFF <g>/<x> rich text — same data shape
// the HTML parser produces. The display layer does not auto-chip plain text;
// chips come only from richText set at import.
//
// In-scope tags (chip set): paired <i>, <b>, <u>, <a ...>, <span ...>
// + self-closing <br>, <br/>, <hr>, <img>. Excluded on purpose: <strong>,
// <em>, <sub>, <sup>, <code> — those are inline FORMATTING marks rendered
// by TipTap's Bold/Italic/Subscript/Superscript extensions, and translators
// apply/round-trip them via Mod+B / Mod+I. Treating them as chips would
// break inline formatting.

// inlineHTMLCtypeMap maps HTML inline element names to the XLIFF ctype label
// we use when emitting <g> wrappers on import. Tight to the chip set above.
// The HTML parser's broader map (in html_segment.go) chips a wider set (<em>,
// <strong>…); this narrower set is deliberate for the non-HTML parsers, and
// the two maps must stay separate.
var inlineHTMLCtypeMap = map[string]string{
	"b":    "bold",
	"i":    "italic",
	"u":    "underlined",
	"a":    "x-html-a",
	"span": "x-html-span",
}

// Per-tag paired regexes (Go's RE2 has no backreferences, so we can't use a
// single regex with \1 — one regex per tag instead). The non-greedy `.*?`
// inside [\s\S] lets the match span across newlines without being greedy.
var pairedInlineTagREs = map[string]*regexp.Regexp{
	"i":    regexp.MustCompile(`<i(\s[^>]*)?>([\s\S]*?)</i>`),
	"b":    regexp.MustCompile(`<b(\s[^>]*)?>([\s\S]*?)</b>`),
	"u":    regexp.MustCompile(`<u(\s[^>]*)?>([\s\S]*?)</u>`),
	"a":    regexp.MustCompile(`<a(\s[^>]*)?>([\s\S]*?)</a>`),
	"span": regexp.MustCompile(`<span(\s[^>]*)?>([\s\S]*?)</span>`),
}

// HTML5 lets void elements omit the slash (`<br>` not just `<br/>`), so the
// regex matches either form. The optional space before the slash also covers
// `<br />`.
var selfClosingInlineTagRE = regexp.MustCompile(`<(br|hr|img)(\s[^>]*)?\s*/?>`)

// ChipOriginalMarkup renders the optional `original_markup` attribute, which
// carries an inline element's start tag verbatim so both the editor tooltip and
// the export path can recover its attributes.
//
// Returns "" when the element has no attributes: the ctype already implies the
// element name, so `<sup>` or `<b>` would only bloat every stored rich-text
// value to say what we already know. Only `<a href>`, `<span class>`,
// `<meta-ref linkid>` and friends carry anything worth keeping. Without it,
// export would rebuild a bare `<a>` and drop the URL.
func ChipOriginalMarkup(startTag string, attrCount int) string {
	if attrCount == 0 {
		return ""
	}
	return ` original_markup="` + XMLEscapeAttr(startTag) + `"`
}

// XMLUnescapeAttr reverses XMLEscapeAttr. `&amp;` is decoded LAST so an escaped
// `&amp;lt;` doesn't collapse into a `<`.
func XMLUnescapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return s
}

// StartTagElementName pulls `a` out of `<a href="…">`. Returns "" if the string
// isn't a start tag.
func StartTagElementName(startTag string) string {
	m := startTagNameRE.FindStringSubmatch(startTag)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

var startTagNameRE = regexp.MustCompile(`^<\s*([A-Za-z_][\w.:-]*)`)

var OriginalMarkupAttrRE = regexp.MustCompile(`original_markup="([^"]*)"`)

// originalMarkupOf extracts and decodes the `original_markup` attribute from a
// matched `<g …>…</g>` block.
//
// Scoped to the OPENING tag only: the block's content is translator text and
// could contain the literal string `original_markup="…"` without meaning it.
func originalMarkupOf(gBlock string) string {
	end := strings.IndexByte(gBlock, '>')
	if end < 0 {
		return ""
	}
	m := OriginalMarkupAttrRE.FindStringSubmatch(gBlock[:end+1])
	if len(m) < 2 {
		return ""
	}
	return XMLUnescapeAttr(m[1])
}

// DetectInlineTags scans the given text for chip-worthy HTML inline tags
// and returns:
//   - plain: text with tags stripped (just the inner content preserved)
//   - rich:  XLIFF rich text with <g id="N" ctype="X">...</g> for paired
//     tags and <x id="N" ctype="lb"/> for void elements. Empty string
//     when no chip-worthy tags were found, so callers treat empty
//     rich as "no inline formatting, plain suffices".
//
// Order: paired first so the rich buffer's inner content is preserved,
// then self-closing.
func DetectInlineTags(text string) (plain, rich string) {
	if !mightContainInlineTags(text) {
		return text, ""
	}

	plainBuf := text
	richBuf := text
	hasTags := false
	tagID := 0

	// Paired tags — iterate the chip-set tags in a stable order so the
	// emitted id="N" values are reproducible (helps round-trip tests).
	for _, tagName := range []string{"i", "b", "u", "a", "span"} {
		re := pairedInlineTagREs[tagName]
		ctype := inlineHTMLCtypeMap[tagName]

		// Plain: keep just the inner content.
		plainBuf = re.ReplaceAllStringFunc(plainBuf, func(match string) string {
			sub := re.FindStringSubmatch(match)
			if len(sub) < 3 {
				return match
			}
			return sub[2]
		})

		// Rich: wrap inner content with <g ctype>.
		richBuf = re.ReplaceAllStringFunc(richBuf, func(match string) string {
			sub := re.FindStringSubmatch(match)
			if len(sub) < 3 {
				return match
			}
			tagID++
			hasTags = true
			// sub[1] is the raw attribute text (" href=\"/foo\""), empty when
			// the tag has none.
			attrs := sub[1]
			origin := ""
			if strings.TrimSpace(attrs) != "" {
				origin = ChipOriginalMarkup("<"+tagName+attrs+">", 1)
			}
			return fmt.Sprintf(`<g id="%d" ctype="%s"%s>%s</g>`, tagID, ctype, origin, sub[2])
		})
	}

	// Self-closing — replace with newline (br/hr) or empty (img) in plain;
	// <x ctype="lb" original_tag="..."/> in rich.
	richBuf = selfClosingInlineTagRE.ReplaceAllStringFunc(richBuf, func(match string) string {
		sub := selfClosingInlineTagRE.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		tagID++
		hasTags = true
		return fmt.Sprintf(`<x id="%d" ctype="lb" original_tag="%s"/>`, tagID, sub[1])
	})
	plainBuf = selfClosingInlineTagRE.ReplaceAllStringFunc(plainBuf, func(match string) string {
		sub := selfClosingInlineTagRE.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		switch sub[1] {
		case "br", "hr":
			return "\n"
		default:
			return ""
		}
	})

	if !hasTags {
		return text, ""
	}
	return plainBuf, richBuf
}

// mightContainInlineTags is a quick sniff to avoid running regexes on every
// segment when nothing interesting is in the text. Plain content without
// `<` returns immediately — the common case.
func mightContainInlineTags(text string) bool {
	if !strings.Contains(text, "<") {
		return false
	}
	for tag := range inlineHTMLCtypeMap {
		if strings.Contains(text, "<"+tag) {
			return true
		}
	}
	return strings.Contains(text, "<br") ||
		strings.Contains(text, "<hr") ||
		strings.Contains(text, "<img")
}

// InlineCtypeToHTMLMap maps XLIFF ctype values back to HTML element names.
// Union of html_segment.go's ctypes + the non-HTML parser's chip set —
// covers both import origins on export.
var InlineCtypeToHTMLMap = map[string]string{
	"bold":          "b",
	"italic":        "i",
	"underlined":    "u",
	"x-html-a":      "a",
	"x-html-span":   "span",
	"x-html-em":     "em",
	"x-html-strong": "strong",
	"x-html-sub":    "sub",
	"x-html-sup":    "sup",
	"x-html-code":   "code",
}

var (
	xliffGToInlineHTMLRE = regexp.MustCompile(`<g\s+[^>]*ctype="([^"]*)"[^>]*>([\s\S]*?)</g>`)
	xliffXToInlineHTMLRE = regexp.MustCompile(`<x\s+[^>]*ctype="lb"[^>]*/>`)
	originalTagAttrRE    = regexp.MustCompile(`original_tag="([^"]+)"`)
)

// XLIFFInlineToHTML reverses DetectInlineTags for export — converts XLIFF
// <g>/<x> wrappers back to HTML inline form. Pass-through for text without
// rich markup. Runs the paired replacement in a bounded loop so nested <g>
// tags unwrap from inside out (one regex pass only handles one level of
// nesting at a time, since RE2 can't recurse).
func XLIFFInlineToHTML(text string) string {
	return xliffInlineToHTML(text, originalMarkupOf)
}

// XLIFFInlineToHTMLFromSource is XLIFFInlineToHTML with each chip's start tag
// taken from sourceRich, the source segment's rich text, by chip id. The
// translation's own original_markup is ignored; a chip the source lacks
// becomes the bare element its ctype names.
func XLIFFInlineToHTMLFromSource(text, sourceRich string) string {
	tags := map[string]string{}
	for _, start := range chipStartTagRE.FindAllString(sourceRich, -1) {
		if markup := originalMarkupOf(start); markup != "" {
			tags[chipIDOf(start)] = markup
		}
	}
	return xliffInlineToHTML(text, func(gBlock string) string {
		return tags[chipIDOf(gBlock)]
	})
}

var (
	chipStartTagRE = regexp.MustCompile(`<g\s[^>]*>`)
	chipIDAttrRE   = regexp.MustCompile(`^<g\s+id="([^"]*)"`)
)

// chipIDOf returns the id of a `<g id="…" …>` chip, "" when it has none.
func chipIDOf(gBlock string) string {
	m := chipIDAttrRE.FindStringSubmatch(gBlock)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// xliffInlineToHTML is XLIFFInlineToHTML with startTag naming the HTML start
// tag of a matched `<g …>…</g>` block, "" for the ctype's bare element.
func xliffInlineToHTML(text string, startTag func(gBlock string) string) string {
	if !strings.Contains(text, "<g ") && !strings.Contains(text, "<x ") {
		return text
	}

	result := text

	// Bounded loop to unwrap nested <g> — typical depth is 1–2, ceiling
	// at 8 prevents pathological inputs from spinning forever.
	for i := 0; i < 8; i++ {
		next := xliffGToInlineHTMLRE.ReplaceAllStringFunc(result, func(match string) string {
			sub := xliffGToInlineHTMLRE.FindStringSubmatch(match)
			if len(sub) < 3 {
				return match
			}
			ctype := sub[1]
			content := sub[2]

			// Prefer the original start tag when the parser
			// stored one, so attributes survive. Reconstructing from ctype
			// alone yields a bare `<a>` and silently loses every href.
			if start := startTag(match); start != "" {
				if name := StartTagElementName(start); name != "" {
					return start + content + "</" + name + ">"
				}
			}
			if htmlTag, ok := InlineCtypeToHTMLMap[ctype]; ok {
				return "<" + htmlTag + ">" + content + "</" + htmlTag + ">"
			}
			return content // unknown ctype: strip the <g>, keep inner text
		})
		if next == result {
			break
		}
		result = next
	}

	// Self-closing — restore original tag name when present, fall back to
	// <br> for chips stored without original_tag.
	result = xliffXToInlineHTMLRE.ReplaceAllStringFunc(result, func(match string) string {
		orig := originalTagAttrRE.FindStringSubmatch(match)
		if len(orig) >= 2 {
			switch orig[1] {
			case "br", "hr":
				return "<" + orig[1] + ">"
			case "img":
				return "" // <img> without src is degenerate, drop
			}
		}
		return "<br>"
	})

	return result
}
