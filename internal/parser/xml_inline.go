package parser

import (
	"fmt"
	"strconv"
	"strings"
)

// Inline tag handling for the generic XML parser.
//
// Import turns inline elements into XLIFF chips; export turns them back. The
// chip's id is authoritative on the way back, NOT its ctype or position — a
// translator reordering chips (routine in German) must still get the right tag
// in the right place, and the original start tag is restored verbatim so
// attributes survive. That's non-negotiable for ST4 <meta-ref> elements, whose
// linkid points at a CMS node.
//
// This is why XLIFFInlineToHTML (inline_tag.go) can't be reused here: it maps
// ctype back to a bare tag name, dropping every attribute, and emits HTML5
// void syntax (`<br>`) that isn't well-formed XML.

// xmlInlineCtypes maps known inline element names to standard XLIFF ctype
// values so the editor renders familiar chips (bold, italic, line break).
// Anything else gets "x-xml-<name>", which the editor treats as a generic
// inline chip and the QA tag checks handle by id.
var xmlInlineCtypes = map[string]string{
	"b":      "bold",
	"strong": "x-html-strong",
	"i":      "italic",
	"em":     "x-html-em",
	"u":      "underlined",
	"sup":    "x-html-sup",
	"sub":    "x-html-sub",
	"a":      "x-html-a",
	"span":   "x-html-span",
	"code":   "x-html-code",
	"br":     "lb",
}

// xmlCtypeFor returns the ctype label for an element name.
func xmlCtypeFor(name string) string {
	if c, ok := xmlInlineCtypes[name]; ok {
		return c
	}
	return "x-xml-" + name
}

// xmlContent is the result of rendering a byte range of the source document
// into translator-facing text.
type xmlContent struct {
	// plain is the segment text with inline markup removed.
	plain string
	// rich is plain plus XLIFF <g>/<x> chips; empty when there are no inline
	// elements, matching the convention the other parsers use.
	rich string
	// offsets maps plain byte index → source byte offset, with len(plain)+1
	// entries so a plain range converts to a source range.
	offsets []int
	// breakable[i] reports whether plain byte i sits in text owned directly by
	// the container, rather than inside an inline element. A sentence break
	// may only land on breakable text — cutting a chip in half is not an option.
	breakable []bool
	// owners[i] is the outermost inline element that plain byte i came from,
	// or nil for text the container owns directly. A sentence may legitimately
	// BEGIN or END inside an inline element ("<meta-ref>Eco</meta-ref>: q =
	// ..."), in which case its source range has to cover the whole element,
	// not just the text inside it.
	owners []*xmlNode
	// inlines lists the inline elements in document order. Chip id N refers to
	// inlines[N-1].
	inlines []*xmlNode
}

// buildXMLContent renders src[rng.Start:rng.End] as segment content, treating
// every element of node's subtree that falls inside rng as inline markup.
func buildXMLContent(src string, rng TextRange, node *xmlNode, refs referenceIndex) xmlContent {
	c := xmlContent{}
	var plain, rich strings.Builder
	tagID := 0

	// emitText appends a raw source slice as decoded plain text, extending the
	// offset, breakable and owner maps in step. owner is the outermost inline
	// element this text sits inside, or nil at container level.
	emitText := func(start, end int, owner *xmlNode) {
		if start >= end {
			return
		}
		decoded, offsets := xmlUnescape(src[start:end])
		plain.WriteString(decoded)
		rich.WriteString(decoded)
		for i := 0; i < len(decoded); i++ {
			c.offsets = append(c.offsets, start+offsets[i])
			c.breakable = append(c.breakable, owner == nil)
			c.owners = append(c.owners, owner)
		}
	}

	// emitSynthetic appends text that has no source bytes behind it (the
	// newline standing in for a line break). It anchors to the element that
	// produced it and is never breakable.
	emitSynthetic := func(s string, owner *xmlNode) {
		plain.WriteString(s)
		for i := 0; i < len(s); i++ {
			c.offsets = append(c.offsets, owner.outerStart)
			c.breakable = append(c.breakable, false)
			c.owners = append(c.owners, owner)
		}
	}

	// emitGaps writes the parent's direct text between two positions, stepping
	// over comments and other skipped markup so they never reach the segment.
	emitGaps := func(parent *xmlNode, from, to int, owner *xmlNode) {
		for _, g := range parent.gapsWithin(from, to) {
			emitText(g.Start, g.End, owner)
		}
	}

	var walk func(parent *xmlNode, from, to int, owner *xmlNode)
	walk = func(parent *xmlNode, from, to int, owner *xmlNode) {
		p := from
		for _, child := range parent.children {
			if child.outerStart < from || child.outerEnd > to {
				continue
			}
			emitGaps(parent, p, child.outerStart, owner)

			tagID++
			id := tagID
			c.inlines = append(c.inlines, child)
			ctype := xmlCtypeFor(child.name)

			// Nested inline elements report the OUTERMOST one as their owner,
			// so expanding a sentence range covers the whole construct.
			childOwner := owner
			if childOwner == nil {
				childOwner = child
			}

			// Carry the element's attributes forward so the editor can show
			// them on hover — an ST4 <meta-ref> is only identifiable by its
			// linkid, and the ctype alone can't express that.
			origin := chipOriginalMarkup(child.startTag(src), len(child.attrs))

			// A resolvable cross-reference records WHICH segment supplies its
			// text, so the editor can show the translated term and export can
			// write it into the element.
			if key := refs.referenceKey(child); key != "" {
				origin += ` ref_key="` + xmlEscapeAttr(key) + `"`
			}

			// A cross-reference is one opaque placeholder: emitting the
			// self-closing form and NOT walking into it is what keeps its
			// generated text out of the segment — and therefore out of the
			// word count, TM lookup and QA.
			if child.isEmpty() || child.isReferencePlaceholder() {
				fmt.Fprintf(&rich, `<x id="%d" ctype="%s"%s/>`, id, ctype, origin)
				if child.name == "br" {
					emitSynthetic("\n", childOwner)
				}
			} else {
				fmt.Fprintf(&rich, `<g id="%d" ctype="%s"%s>`, id, ctype, origin)
				walk(child, child.innerStart, child.innerEnd, childOwner)
				rich.WriteString("</g>")
			}
			p = child.outerEnd
		}
		emitGaps(parent, p, to, owner)
	}
	walk(node, rng.Start, rng.End, nil)

	c.offsets = append(c.offsets, rng.End)
	c.plain = plain.String()
	if tagID > 0 {
		c.rich = rich.String()
	}
	return c
}

// canBreakAt returns the veto callback SplitSentences needs: a break is only
// allowed where the terminator it follows is breakable text.
func (c xmlContent) canBreakAt() func(int) bool {
	return breakVeto(c.breakable)
}

// sourceRange converts a plain-text range into a source byte range, widening
// it to whole inline elements at either edge.
//
// Without the widening, a sentence beginning inside a chip
// ("<meta-ref>Eco</meta-ref>: q [l/min] = …") would map to an offset after the
// element's start tag, and rebuilding that range would drop both the tag and
// the text inside it.
func (c xmlContent) sourceRange(r TextRange) TextRange {
	start := c.offsets[r.Start]
	if r.Start < len(c.owners) && c.owners[r.Start] != nil {
		start = c.owners[r.Start].outerStart
	}

	end := c.offsets[r.End]
	if r.End > r.Start && r.End-1 < len(c.owners) && c.owners[r.End-1] != nil {
		end = c.owners[r.End-1].outerEnd
	}
	return TextRange{Start: start, End: end}
}

// refContentFunc yields the text to place inside a cross-reference on export:
// the defining segment's translation when there is one.
type refContentFunc func(n *xmlNode) (string, bool)

// xmlChipsToMarkup converts a translated string containing XLIFF chips back
// into XML markup, restoring each chip's original element from inlines.
//
// Lookup is by chip id, so reordered chips land correctly and a chip the
// translator deleted simply leaves its tag out (the missing-tags QA check
// flags that). Unknown ids degrade to their inner text rather than emitting
// a broken tag.
func xmlChipsToMarkup(text string, inlines []*xmlNode, src string, refContent refContentFunc) string {
	if !strings.Contains(text, "<g ") && !strings.Contains(text, "<x ") {
		return xmlEscapeText(text)
	}

	var out strings.Builder
	// Stack of end tags owed to still-open <g> chips.
	var open []string

	i := 0
	for i < len(text) {
		if text[i] != '<' {
			j := strings.IndexByte(text[i:], '<')
			if j < 0 {
				out.WriteString(xmlEscapeText(text[i:]))
				break
			}
			out.WriteString(xmlEscapeText(text[i : i+j]))
			i += j
			continue
		}

		tag, next, ok := scanChipTag(text, i)
		if !ok {
			// Not a chip — a stray '<' in the translation. Escape it.
			out.WriteString("&lt;")
			i++
			continue
		}

		switch {
		case tag.closing:
			if len(open) > 0 {
				out.WriteString(open[len(open)-1])
				open = open[:len(open)-1]
			}
		case tag.name == "x":
			if n := chipNode(inlines, tag.id); n != nil {
				// A cross-reference gets the DEFINING segment's translation
				// written into it. A delivered ST4 translation carries the
				// translated term inside the reference
				// (`<meta-ref …>Sparmodus</meta-ref>` in st4_delivered_de.xml),
				// so ST4 does not fill it in at publish — exporting the source
				// content would ship an untranslated term.
				if term, ok := refContent(n); ok {
					out.WriteString(n.startTag(src))
					out.WriteString(xmlEscapeText(term))
					out.WriteString(n.endTag(src))
					break
				}
				// Otherwise the element's whole byte range, verbatim. For a
				// genuinely self-closing `<br />` that IS the start tag, so
				// this is a generalisation rather than a special case.
				out.WriteString(src[n.outerStart:n.outerEnd])
			}
		case tag.selfClosing:
			// <g id="N"/> with no content — degenerate but well-formed.
			if n := chipNode(inlines, tag.id); n != nil {
				out.WriteString(n.startTag(src))
				out.WriteString(n.endTag(src))
			}
		default: // opening <g>
			if n := chipNode(inlines, tag.id); n != nil {
				out.WriteString(n.startTag(src))
				open = append(open, n.endTag(src))
			} else {
				// Unknown id: keep the inner text, drop the tag.
				open = append(open, "")
			}
		}
		i = next
	}

	// Close anything the translation left open.
	for k := len(open) - 1; k >= 0; k-- {
		out.WriteString(open[k])
	}
	return out.String()
}

// chipNode resolves a 1-based chip id against the inline element list.
func chipNode(inlines []*xmlNode, id int) *xmlNode {
	if id < 1 || id > len(inlines) {
		return nil
	}
	return inlines[id-1]
}

type chipTag struct {
	name        string
	id          int
	ctype       string
	closing     bool
	selfClosing bool
}

// scanChipTag parses an XLIFF chip tag starting at text[i], returning the tag
// and the index just past it. ok is false when text[i] doesn't start a chip.
func scanChipTag(text string, i int) (chipTag, int, bool) {
	end := strings.IndexByte(text[i:], '>')
	if end < 0 {
		return chipTag{}, 0, false
	}
	end += i
	raw := text[i : end+1]

	body := strings.TrimSuffix(strings.TrimPrefix(raw, "<"), ">")
	tag := chipTag{}
	if strings.HasPrefix(body, "/") {
		tag.closing = true
		body = strings.TrimPrefix(body, "/")
	}
	if strings.HasSuffix(body, "/") {
		tag.selfClosing = true
		body = strings.TrimSuffix(body, "/")
	}
	body = strings.TrimSpace(body)

	name, attrs, _ := strings.Cut(body, " ")
	if name != "g" && name != "x" {
		return chipTag{}, 0, false
	}
	tag.name = name
	tag.id = parseChipID(attrs)
	tag.ctype = chipAttr(attrs, "ctype")
	return tag, end + 1, true
}

// parseChipID pulls the numeric id out of a chip tag's attribute text.
func parseChipID(attrs string) int {
	id, err := strconv.Atoi(chipAttr(attrs, "id"))
	if err != nil {
		return 0
	}
	return id
}

// chipAttr returns the value of the double-quoted attribute name in a chip
// tag's attribute text, or "" when it is absent.
func chipAttr(attrs, name string) string {
	key := name + `="`
	for k := 0; k < len(attrs); {
		j := strings.Index(attrs[k:], key)
		if j < 0 {
			return ""
		}
		j += k
		// Only a whole attribute name counts: "id" must not match "ref_id".
		if j == 0 || isTagSpace(attrs[j-1]) {
			rest := attrs[j+len(key):]
			q := strings.IndexByte(rest, '"')
			if q < 0 {
				return ""
			}
			return rest[:q]
		}
		k = j + len(key)
	}
	return ""
}
