package parser

import (
	"fmt"
	"strings"
)

// XML parses arbitrary XML documents.
//
// Built against SCHEMA ST4 (Quanos) TMS exports — <tms>/<tmsnode> with HTML-ish
// content markup — but the extraction rules are structural, not vocabulary
// specific, so DocBook, DITA and in-house schemas work the same way.
//
// Android strings.xml keeps its own parser; DetectFormat routes <resources>
// documents there and everything else here.
type XML struct{}

// maxXMLContextLen caps the context string shown next to a segment. Enough to
// identify the enclosing CMS node, short enough not to swamp the grid.
const maxXMLContextLen = 120

// xmlSegmentSpec is one translatable unit: where it lives in the source, what
// it says, and which inline elements it contains.
type xmlSegmentSpec struct {
	key     string
	rng     TextRange
	content xmlContent
	node    *xmlNode
	context string
}

func (p XML) Parse(content []byte) ([]Segment, error) {
	doc, err := parseXMLDoc(content)
	if err != nil {
		return nil, err
	}

	specs := collectXMLSegments(doc)
	segments := make([]Segment, 0, len(specs))
	for i, spec := range specs {
		segments = append(segments, Segment{
			Key:      spec.key,
			Text:     spec.content.plain,
			RichText: spec.content.rich,
			Position: i,
			Context:  spec.context,
		})
	}
	return segments, nil
}

// Reconstruct splices translations into the original bytes.
//
// It re-derives the same segment ranges from the same source, so byte ranges
// never have to be persisted. Everything outside a translated range — DOCTYPE,
// comments, attribute quoting, self-closing forms, inter-element whitespace,
// the ST4 file-hash — is copied through untouched.
func (p XML) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	doc, err := parseXMLDoc(original)
	if err != nil {
		return nil, err
	}

	specs := collectXMLSegments(doc)
	refs := buildReferenceIndex(doc)

	// A cross-reference renders the DEFINING segment's translation. Resolved
	// here rather than carried in the chip, so it always reflects the current
	// translation of the term.
	refContent := func(n *xmlNode) (string, bool) {
		key := refs.referenceKey(n)
		if key == "" {
			return "", false
		}
		term, ok := translations[key]
		return term, ok && term != ""
	}

	edits := make([]spliceEdit, 0, len(specs))
	for _, spec := range specs {
		translated, ok := translations[spec.key]
		if !ok || translated == "" {
			continue
		}
		edits = append(edits, spliceEdit{
			rng:  spec.rng,
			text: xmlChipsToMarkup(translated, spec.content.inlines, doc.src, refContent),
		})
	}
	return []byte(splice(doc.src, edits)), nil
}

// collectXMLSegments walks the document and returns every translatable unit in
// document order. Parse and Reconstruct both go through here, which is what
// keeps their keys and ranges in lockstep.
func collectXMLSegments(doc *xmlDoc) []xmlSegmentSpec {
	var specs []xmlSegmentSpec
	// Built once per document: segment keys come from node position, so this
	// doesn't depend on the segments themselves.
	refs := buildReferenceIndex(doc)

	var walk func(n *xmlNode)
	walk = func(n *xmlNode) {
		if !n.isTranslatable() {
			return
		}
		if isSegmentContainer(doc.src, n) {
			specs = append(specs, segmentsFor(doc, n, refs)...)
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(doc.root)

	return specs
}

// isSegmentContainer reports whether n's content forms a translatable unit:
// it has text, and everything nested inside it is inline markup.
func isSegmentContainer(src string, n *xmlNode) bool {
	return n.hasText(src) && n.childrenAreInline(src)
}

// segmentsFor turns a container into one or more segments — more than one when
// its text splits into sentences.
func segmentsFor(doc *xmlDoc, container *xmlNode, refs referenceIndex) []xmlSegmentSpec {
	node, rng := trimmedContentRange(doc.src, container)
	if rng.Start >= rng.End {
		return nil
	}

	key := xmlNodePath(node)
	context := xmlNodeContext(doc, node)

	full := buildXMLContent(doc.src, rng, node, refs)
	sentences := SplitSentences(full.plain, full.canBreakAt())
	if len(sentences) == 0 {
		return nil
	}

	// Single sentence: keep the key clean, no #N suffix.
	if len(sentences) == 1 {
		// The container's own trimmed range, not the text's. A leading or
		// trailing placeholder contributes no plain text, so deriving the
		// range from text offsets would leave it outside the segment and the
		// chip would vanish.
		srcRange := rng
		return []xmlSegmentSpec{{
			key:     key,
			rng:     srcRange,
			content: buildXMLContent(doc.src, srcRange, node, refs),
			node:    node,
			context: context,
		}}
	}

	specs := make([]xmlSegmentSpec, 0, len(sentences))
	for i, s := range sentences {
		srcRange := full.sourceRange(s)
		// Sentence boundaries only apply BETWEEN sentences; the outer edges
		// belong to the container, so a zero-width chip at either end stays
		// inside the segment that owns it.
		if i == 0 {
			srcRange.Start = rng.Start
		}
		if i == len(sentences)-1 {
			srcRange.End = rng.End
		}
		specs = append(specs, xmlSegmentSpec{
			key: fmt.Sprintf("%s#%d", key, i+1),
			rng: srcRange,
			// Rebuild per sentence so chip ids restart at 1, matching how
			// every other parser numbers tags within a segment.
			content: buildXMLContent(doc.src, srcRange, node, refs),
			node:    node,
			context: context,
		})
	}
	return specs
}

// trimmedContentRange narrows a container down to the bytes actually worth
// translating, and descends through sole wrapper elements.
//
// Two adjustments, both taken from the reference XLIFF:
//
//   - `<p><b>General specifications</b> </p>` yields "General specifications",
//     not a segment consisting of one bold chip. Descending into the wrapper
//     leaves <b> outside the spliced range, so it survives export untouched.
//   - `<p>… is Desflurane<br /><br /></p>` yields the sentence without two
//     trailing line-break chips a translator would have to carry around.
func trimmedContentRange(src string, n *xmlNode) (*xmlNode, TextRange) {
	node := n
	for {
		rng := TextRange{Start: node.innerStart, End: node.innerEnd}
		rng = trimRangeEdges(src, node, rng)

		wrapper := soleWrapperChild(src, node, rng)
		if wrapper == nil {
			return node, rng
		}
		node = wrapper
	}
}

// trimRangeEdges pulls whitespace and boundary-only empty elements out of a
// range. Empty elements only count at the edges — a <br/> between two clauses
// stays in, as a chip.
func trimRangeEdges(src string, n *xmlNode, rng TextRange) TextRange {
	for rng.Start < rng.End {
		trimmed := false

		if adv := leadingSpaceLen(src, rng); adv > 0 {
			rng.Start += adv
			trimmed = true
		}
		if adv := trailingSpaceLen(src, rng); adv > 0 {
			rng.End -= adv
			trimmed = true
		}
		if c := edgeEmptyChild(src, n, rng, true); c != nil {
			rng.Start = c.outerEnd
			trimmed = true
		}
		if c := edgeEmptyChild(src, n, rng, false); c != nil {
			rng.End = c.outerStart
			trimmed = true
		}

		if !trimmed {
			break
		}
	}
	return rng
}

// leadingSpaceLen returns the byte length of whitespace at the start of rng.
func leadingSpaceLen(src string, rng TextRange) int {
	s := src[rng.Start:rng.End]
	return len(s) - len(strings.TrimLeft(s, " \t\r\n"))
}

// trailingSpaceLen returns the byte length of whitespace at the end of rng.
func trailingSpaceLen(src string, rng TextRange) int {
	s := src[rng.Start:rng.End]
	return len(s) - len(strings.TrimRight(s, " \t\r\n"))
}

// edgeEmptyChild returns the child element sitting flush against the leading
// (or trailing) edge of rng when it carries no text — a <br/> or an empty
// <span/>. Returns nil otherwise.
func edgeEmptyChild(src string, n *xmlNode, rng TextRange, leading bool) *xmlNode {
	for _, c := range n.children {
		if c.outerStart < rng.Start || c.outerEnd > rng.End {
			continue
		}
		flush := c.outerEnd == rng.End
		if leading {
			flush = c.outerStart == rng.Start
		}
		if flush && !c.hasText(src) {
			return c
		}
	}
	return nil
}

// soleWrapperChild returns the single element child that spans the whole of
// rng, meaning the range is nothing but that element. Returns nil when the
// range holds text of its own or more than one element.
func soleWrapperChild(src string, n *xmlNode, rng TextRange) *xmlNode {
	var only *xmlNode
	for _, c := range n.children {
		if c.outerStart < rng.Start || c.outerEnd > rng.End {
			continue
		}
		if only != nil {
			return nil
		}
		only = c
	}
	if only == nil || only.isEmpty() {
		return nil
	}
	if only.outerStart != rng.Start || only.outerEnd != rng.End {
		return nil
	}
	if !only.hasText(src) {
		return nil
	}
	return only
}

// xmlNodePath builds a stable, self-describing key: an element path with
// sibling indices where a name repeats, e.g.
// /tms/tmsnode[24]/content/table-container/table/tbody/tr[8]/td[3]/p[2].
//
// Chosen over the sequential seg:N scheme the HTML and Markdown parsers use because
// it survives unrelated edits elsewhere in the document and tells you where a
// stray "Range" cell came from when you're staring at the segment grid.
func xmlNodePath(n *xmlNode) string {
	if n.keys.path != "" {
		return n.keys.path
	}
	part := n.name
	if idx, needed := siblingIndex(n); needed {
		part = fmt.Sprintf("%s[%d]", n.name, idx)
	}
	if n.parent == nil {
		n.keys.path = "/" + part
	} else {
		n.keys.path = xmlNodePath(n.parent) + "/" + part
	}
	return n.keys.path
}

// siblingIndex returns n's 1-based position among same-named siblings, and
// whether an index is needed to disambiguate at all.
func siblingIndex(n *xmlNode) (int, bool) {
	p := n.parent
	if p == nil {
		return 1, false
	}
	if !p.keys.siblingsIndexed {
		counts := make(map[string]int)
		for _, c := range p.children {
			counts[c.name]++
			c.keys.sibIdx = counts[c.name]
		}
		for _, c := range p.children {
			c.keys.sibNeeded = counts[c.name] > 1
		}
		p.keys.siblingsIndexed = true
	}
	return n.keys.sibIdx, n.keys.sibNeeded
}

// xmlNodeContext returns a short label describing where the segment sits —
// the title of the nearest ancestor that has one. In ST4 files that's the
// <tmsnode> title, which is what tells a translator that an isolated "Range"
// belongs to a specification table. A title is not its own context.
func xmlNodeContext(doc *xmlDoc, n *xmlNode) string {
	if n.parent == nil {
		return ""
	}
	titles, texts := xmlTitles(doc, n.parent)
	switch {
	case titles[0] != nil && titles[0] != n:
		return texts[0]
	case titles[0] == n && titles[1] != nil:
		return texts[1]
	}
	return inheritedXMLContext(doc, n.parent.parent)
}

// inheritedXMLContext is the first title text among n's children, else n's
// parent's, and so on up; "" past the root.
func inheritedXMLContext(doc *xmlDoc, n *xmlNode) string {
	if n == nil {
		return ""
	}
	if !n.keys.inheritedFound {
		titles, texts := xmlTitles(doc, n)
		if titles[0] != nil {
			n.keys.inherited = texts[0]
		} else {
			n.keys.inherited = inheritedXMLContext(doc, n.parent)
		}
		n.keys.inheritedFound = true
	}
	return n.keys.inherited
}

// xmlTitles returns n's first two title children with non-empty text, and
// their text as a context label.
func xmlTitles(doc *xmlDoc, n *xmlNode) ([2]*xmlNode, [2]string) {
	if !n.keys.titlesFound {
		found := 0
		for _, c := range n.children {
			if c.name != "title" {
				continue
			}
			text, _ := xmlUnescape(doc.src[c.innerStart:c.innerEnd])
			text = strings.TrimSpace(collapseXMLSpace(text))
			if text == "" {
				continue
			}
			if len([]rune(text)) > maxXMLContextLen {
				text = string([]rune(text)[:maxXMLContextLen]) + "…"
			}
			n.keys.titles[found], n.keys.titleTexts[found] = c, text
			if found++; found == 2 {
				break
			}
		}
		n.keys.titlesFound = true
	}
	return n.keys.titles, n.keys.titleTexts
}

// collapseXMLSpace squeezes runs of whitespace into single spaces, for display
// strings only — never for segment text, which keeps its source spacing.
func collapseXMLSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
