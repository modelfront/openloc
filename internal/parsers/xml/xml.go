package xml

import (
	"fmt"
	"openloc/internal/parsers/common"
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
	rng     common.TextRange
	content xmlContent
	node    *common.XMLNode
	context string
}

func (p XML) Parse(content []byte) ([]common.Segment, error) {
	doc, err := common.ParseXMLDoc(content)
	if err != nil {
		return nil, err
	}

	specs := collectXMLSegments(doc)
	segments := make([]common.Segment, 0, len(specs))
	for i, spec := range specs {
		segments = append(segments, common.Segment{
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
	doc, err := common.ParseXMLDoc(original)
	if err != nil {
		return nil, err
	}

	specs := collectXMLSegments(doc)
	refs := common.BuildReferenceIndex(doc)

	// A cross-reference renders the DEFINING segment's translation. Resolved
	// here rather than carried in the chip, so it always reflects the current
	// translation of the term.
	refContent := func(n *common.XMLNode) (string, bool) {
		key := refs.ReferenceKey(n)
		if key == "" {
			return "", false
		}
		term, ok := translations[key]
		return term, ok && term != ""
	}

	edits := make([]common.SpliceEdit, 0, len(specs))
	for _, spec := range specs {
		translated, ok := translations[spec.key]
		if !ok || translated == "" {
			continue
		}
		edits = append(edits, common.SpliceEdit{
			Rng:  spec.rng,
			Text: xmlChipsToMarkup(translated, spec.content.inlines, doc.Src, refContent),
		})
	}
	return []byte(common.Splice(doc.Src, edits)), nil
}

// collectXMLSegments walks the document and returns every translatable unit in
// document order. Parse and Reconstruct both go through here, which is what
// keeps their keys and ranges in lockstep.
func collectXMLSegments(doc *common.XMLDoc) []xmlSegmentSpec {
	var specs []xmlSegmentSpec
	// Built once per document: segment keys come from node position, so this
	// doesn't depend on the segments themselves.
	refs := common.BuildReferenceIndex(doc)

	var walk func(n *common.XMLNode)
	walk = func(n *common.XMLNode) {
		if !n.IsTranslatable() {
			return
		}
		if isSegmentContainer(doc.Src, n) {
			specs = append(specs, segmentsFor(doc, n, refs)...)
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(doc.Root)

	return specs
}

// isSegmentContainer reports whether n's content forms a translatable unit:
// it has text, and everything nested inside it is inline markup.
func isSegmentContainer(src string, n *common.XMLNode) bool {
	return n.HasText(src) && n.ChildrenAreInline(src)
}

// segmentsFor turns a container into one or more segments — more than one when
// its text splits into sentences.
func segmentsFor(doc *common.XMLDoc, container *common.XMLNode, refs common.ReferenceIndex) []xmlSegmentSpec {
	node, rng := trimmedContentRange(doc.Src, container)
	if rng.Start >= rng.End {
		return nil
	}

	key := common.XMLNodePath(node)
	context := xmlNodeContext(doc, node)

	full := buildXMLContent(doc.Src, rng, node, refs)
	sentences := common.SplitSentences(full.plain, full.canBreakAt())
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
			content: buildXMLContent(doc.Src, srcRange, node, refs),
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
			content: buildXMLContent(doc.Src, srcRange, node, refs),
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
func trimmedContentRange(src string, n *common.XMLNode) (*common.XMLNode, common.TextRange) {
	node := n
	for {
		rng := common.TextRange{Start: node.InnerStart, End: node.InnerEnd}
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
func trimRangeEdges(src string, n *common.XMLNode, rng common.TextRange) common.TextRange {
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
			rng.Start = c.OuterEnd
			trimmed = true
		}
		if c := edgeEmptyChild(src, n, rng, false); c != nil {
			rng.End = c.OuterStart
			trimmed = true
		}

		if !trimmed {
			break
		}
	}
	return rng
}

// leadingSpaceLen returns the byte length of whitespace at the start of rng.
func leadingSpaceLen(src string, rng common.TextRange) int {
	s := src[rng.Start:rng.End]
	return len(s) - len(strings.TrimLeft(s, " \t\r\n"))
}

// trailingSpaceLen returns the byte length of whitespace at the end of rng.
func trailingSpaceLen(src string, rng common.TextRange) int {
	s := src[rng.Start:rng.End]
	return len(s) - len(strings.TrimRight(s, " \t\r\n"))
}

// edgeEmptyChild returns the child element sitting flush against the leading
// (or trailing) edge of rng when it carries no text — a <br/> or an empty
// <span/>. Returns nil otherwise.
func edgeEmptyChild(src string, n *common.XMLNode, rng common.TextRange, leading bool) *common.XMLNode {
	for _, c := range n.Children {
		if c.OuterStart < rng.Start || c.OuterEnd > rng.End {
			continue
		}
		flush := c.OuterEnd == rng.End
		if leading {
			flush = c.OuterStart == rng.Start
		}
		if flush && !c.HasText(src) {
			return c
		}
	}
	return nil
}

// soleWrapperChild returns the single element child that spans the whole of
// rng, meaning the range is nothing but that element. Returns nil when the
// range holds text of its own or more than one element.
func soleWrapperChild(src string, n *common.XMLNode, rng common.TextRange) *common.XMLNode {
	var only *common.XMLNode
	for _, c := range n.Children {
		if c.OuterStart < rng.Start || c.OuterEnd > rng.End {
			continue
		}
		if only != nil {
			return nil
		}
		only = c
	}
	if only == nil || only.IsEmpty() {
		return nil
	}
	if only.OuterStart != rng.Start || only.OuterEnd != rng.End {
		return nil
	}
	if !only.HasText(src) {
		return nil
	}
	return only
}

// xmlNodeContext returns a short label describing where the segment sits —
// the title of the nearest ancestor that has one. In ST4 files that's the
// <tmsnode> title, which is what tells a translator that an isolated "Range"
// belongs to a specification table. A title is not its own context.
func xmlNodeContext(doc *common.XMLDoc, n *common.XMLNode) string {
	if n.Parent == nil {
		return ""
	}
	titles, texts := xmlTitles(doc, n.Parent)
	switch {
	case titles[0] != nil && titles[0] != n:
		return texts[0]
	case titles[0] == n && titles[1] != nil:
		return texts[1]
	}
	return inheritedXMLContext(doc, n.Parent.Parent)
}

// inheritedXMLContext is the first title text among n's children, else n's
// parent's, and so on up; "" past the root.
func inheritedXMLContext(doc *common.XMLDoc, n *common.XMLNode) string {
	if n == nil {
		return ""
	}
	if !n.Keys.InheritedFound {
		titles, texts := xmlTitles(doc, n)
		if titles[0] != nil {
			n.Keys.Inherited = texts[0]
		} else {
			n.Keys.Inherited = inheritedXMLContext(doc, n.Parent)
		}
		n.Keys.InheritedFound = true
	}
	return n.Keys.Inherited
}

// xmlTitles returns n's first two title children with non-empty text, and
// their text as a context label.
func xmlTitles(doc *common.XMLDoc, n *common.XMLNode) ([2]*common.XMLNode, [2]string) {
	if !n.Keys.TitlesFound {
		found := 0
		for _, c := range n.Children {
			if c.Name != "title" {
				continue
			}
			text, _ := common.XMLUnescape(doc.Src[c.InnerStart:c.InnerEnd])
			text = strings.TrimSpace(collapseXMLSpace(text))
			if text == "" {
				continue
			}
			if len([]rune(text)) > maxXMLContextLen {
				text = string([]rune(text)[:maxXMLContextLen]) + "…"
			}
			n.Keys.Titles[found], n.Keys.TitleTexts[found] = c, text
			if found++; found == 2 {
				break
			}
		}
		n.Keys.TitlesFound = true
	}
	return n.Keys.Titles, n.Keys.TitleTexts
}

// collapseXMLSpace squeezes runs of whitespace into single spaces, for display
// strings only — never for segment text, which keeps its source spacing.
func collapseXMLSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
