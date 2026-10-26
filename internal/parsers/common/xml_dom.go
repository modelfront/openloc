package common

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// Byte-offset DOM for the generic XML parser.
//
// Every node records the byte ranges of its start tag, inner content and end
// tag within the ORIGINAL document. That's what lets Reconstruct splice
// translations into the original bytes instead of re-serializing the tree —
// the ST4 files we target carry xml:space="preserve", a DOCTYPE, a file-hash
// attribute and a 25-line settings comment, none of which survive a
// token-and-rewrite round trip.

// XMLNode is one element in the byte-offset DOM.
//
// Offsets carve the element into three slices of the source document:
//
//	src[outerStart:innerStart]  the start tag, e.g. `<p class="x">`
//	src[innerStart:innerEnd]    the inner content
//	src[innerEnd:outerEnd]      the end tag, e.g. `</p>`
//
// A self-closing element (`<br />`) has innerStart == innerEnd == outerEnd, so
// its start tag slice is the whole element and its end tag slice is empty.
type XMLNode struct {
	Name     string
	Attrs    []xml.Attr
	Parent   *XMLNode
	Children []*XMLNode

	// skips are byte ranges of non-text markup directly inside this element —
	// comments, processing instructions, directives. They sit in the gaps
	// between child elements but are NOT text: the ST4 root carries a 25-line
	// <!--TMS Settings--> comment, and counting it as content would make the
	// whole document look like one mixed-content segment.
	skips []TextRange

	OuterStart int
	InnerStart int
	InnerEnd   int
	OuterEnd   int

	Keys xmlKeyCache
}

// xmlKeyCache memoizes what a node's segment key and context derive from: its
// place among its siblings and the titles above it. Filled lazily once the
// tree is complete, so each parent's children are scanned once, not once per
// segment.
type xmlKeyCache struct {
	path string

	// Set on a node once its children's sibIdx/sibNeeded are filled.
	siblingsIndexed bool
	sibIdx          int
	sibNeeded       bool

	// The node's first two element children named title with non-empty
	// text, and the context inherited from the node and its ancestors.
	TitlesFound    bool
	Titles         [2]*XMLNode
	TitleTexts     [2]string
	InheritedFound bool
	Inherited      string
}

// XMLDoc is a parsed document plus its source bytes.
type XMLDoc struct {
	Src  string
	Root *XMLNode
	// nodes is how many nodes the document built, and attrs how many
	// attributes they keep.
	Nodes int
	Attrs int
}

// startTag returns the element's start tag exactly as written in the source,
// attributes and quoting intact. Used to restore inline elements on export —
// a <meta-ref> must come back with its linkid.
func (n *XMLNode) StartTag(src string) string { return src[n.OuterStart:n.InnerStart] }

// endTag returns the element's end tag as written, or "" when self-closing.
func (n *XMLNode) EndTag(src string) string { return src[n.InnerEnd:n.OuterEnd] }

// isEmpty reports whether the element has no inner content at all.
func (n *XMLNode) IsEmpty() bool { return n.InnerStart == n.InnerEnd }

// attr returns the value of the named attribute, ignoring namespace.
func (n *XMLNode) Attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// ParseXMLDoc builds the byte-offset DOM.
//
// Offset mechanics: XML tokens are contiguous, so the decoder's InputOffset()
// after token N is exactly where token N+1 starts. Recording the offset before
// and after each token therefore brackets it precisely. Go emits a synthetic
// EndElement for self-closing tags without advancing the offset, which is what
// collapses innerStart/innerEnd/outerEnd onto the same position.
func ParseXMLDoc(content []byte) (*XMLDoc, error) {
	return ParseXMLDocWith(content, XMLParseOptions{})
}

// XMLParseOptions limit what ParseXMLDocWith builds.
type XMLParseOptions struct {
	// skip names elements whose content the caller never reads: such an
	// element gets a node, its descendants do not.
	Skip map[string]bool
	// maxNodes, when positive, is how many nodes the document may build;
	// one more is ErrTooManyElements.
	MaxNodes int
	// maxAttrs, when positive, is how many attributes the built nodes may
	// keep in total; one more is ErrTooManyElements.
	MaxAttrs int
}

// ErrTooManyElements is a document with more elements than maxNodes, or
// more attributes on them than maxAttrs.
var ErrTooManyElements = errors.New("xml: too many elements")

// StartTagsWithin reports whether every start tag in src is at most max
// bytes long. The XML decoder builds all of a tag's attributes before the
// caller sees the tag, so one tag with millions of attributes costs memory
// that no node count can catch; this scan refuses it first. Comments, CDATA
// and processing instructions are skipped; '>' inside a quoted attribute
// value does not end a tag. Malformed input is left to the decoder.
func StartTagsWithin(src []byte, max int) bool {
	skipTo := func(rest []byte, end string) int {
		k := bytes.Index(rest, []byte(end))
		if k < 0 {
			return len(rest)
		}
		return k + len(end)
	}
	for i := 0; i < len(src); {
		j := bytes.IndexByte(src[i:], '<')
		if j < 0 {
			return true
		}
		i += j
		rest := src[i:]
		switch {
		case bytes.HasPrefix(rest, []byte("<!--")):
			i += skipTo(rest, "-->")
			continue
		case bytes.HasPrefix(rest, []byte("<![CDATA[")):
			i += skipTo(rest, "]]>")
			continue
		case bytes.HasPrefix(rest, []byte("<?")):
			i += skipTo(rest, "?>")
			continue
		}
		var quote byte
		k := 1
		for ; k < len(rest) && k <= max; k++ {
			c := rest[k]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
				continue
			}
			if c == '"' || c == '\'' {
				quote = c
				continue
			}
			if c == '>' {
				break
			}
		}
		if k > max {
			return false
		}
		i += k + 1
	}
	return true
}

func ParseXMLDocWith(content []byte, opts XMLParseOptions) (*XMLDoc, error) {
	src := string(content)
	dec := xml.NewDecoder(strings.NewReader(src))
	// HTML-ish vocabularies (and ST4 content) use named entities beyond the
	// five XML predefines. Without this, &nbsp; is a hard parse error.
	dec.Entity = xml.HTMLEntity

	doc := &XMLDoc{Src: src}
	var stack []*XMLNode
	// skipDepth counts the open elements inside a skipped element, the
	// skipped element itself included.
	skipDepth := 0

	for {
		before := dec.InputOffset()
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xml: decode at byte %d: %w", before, err)
		}
		after := dec.InputOffset()

		switch t := tok.(type) {
		case xml.StartElement:
			if skipDepth > 0 {
				skipDepth++
				continue
			}
			if opts.Skip[t.Name.Local] {
				skipDepth = 1
			}
			doc.Nodes++
			doc.Attrs += len(t.Attr)
			if (opts.MaxNodes > 0 && doc.Nodes > opts.MaxNodes) || (opts.MaxAttrs > 0 && doc.Attrs > opts.MaxAttrs) {
				return nil, ErrTooManyElements
			}
			node := &XMLNode{
				Name:       t.Name.Local,
				Attrs:      append([]xml.Attr(nil), t.Attr...),
				OuterStart: int(before),
				InnerStart: int(after),
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				node.Parent = parent
				parent.Children = append(parent.Children, node)
			} else if doc.Root == nil {
				doc.Root = node
			}
			stack = append(stack, node)

		case xml.EndElement:
			if skipDepth > 1 {
				skipDepth--
				continue
			}
			skipDepth = 0
			if len(stack) == 0 {
				return nil, fmt.Errorf("xml: unbalanced end element </%s>", t.Name.Local)
			}
			node := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			node.InnerEnd = int(before)
			node.OuterEnd = int(after)

		case xml.Comment, xml.ProcInst, xml.Directive:
			if len(stack) > 0 && skipDepth == 0 {
				top := stack[len(stack)-1]
				top.skips = append(top.skips, TextRange{Start: int(before), End: int(after)})
			}
		}
	}

	if doc.Root == nil {
		return nil, fmt.Errorf("xml: no root element")
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("xml: unclosed element <%s>", stack[len(stack)-1].Name)
	}
	return doc, nil
}

// --- structural predicates ---

// blockedRanges returns the byte ranges inside n that are not text: child
// elements and skipped markup, in source order.
func (n *XMLNode) blockedRanges() []TextRange {
	blocked := make([]TextRange, 0, len(n.Children)+len(n.skips))
	for _, c := range n.Children {
		blocked = append(blocked, TextRange{Start: c.OuterStart, End: c.OuterEnd})
	}
	blocked = append(blocked, n.skips...)
	sort.Slice(blocked, func(i, j int) bool { return blocked[i].Start < blocked[j].Start })
	return blocked
}

// textGaps returns the byte ranges of n's direct text content — everything
// between its children and skipped markup, plus the head and tail.
func (n *XMLNode) textGaps() []TextRange {
	var gaps []TextRange
	p := n.InnerStart
	for _, b := range n.blockedRanges() {
		if b.Start > p {
			gaps = append(gaps, TextRange{Start: p, End: b.Start})
		}
		if b.End > p {
			p = b.End
		}
	}
	if n.InnerEnd > p {
		gaps = append(gaps, TextRange{Start: p, End: n.InnerEnd})
	}
	return gaps
}

// gapsWithin returns n's direct-text ranges clipped to [from, to).
func (n *XMLNode) GapsWithin(from, to int) []TextRange {
	var out []TextRange
	for _, g := range n.textGaps() {
		if g.End <= from || g.Start >= to {
			continue
		}
		out = append(out, TextRange{Start: max(g.Start, from), End: min(g.End, to)})
	}
	return out
}

// hasDirectText reports whether n has non-whitespace text of its own (not
// counting text inside child elements).
func (n *XMLNode) hasDirectText(src string) bool {
	for _, g := range n.textGaps() {
		if strings.TrimSpace(src[g.Start:g.End]) != "" {
			return true
		}
	}
	return false
}

// hasText reports whether n's subtree contains any non-whitespace text.
func (n *XMLNode) HasText(src string) bool {
	if n.hasDirectText(src) {
		return true
	}
	for _, c := range n.Children {
		if c.HasText(src) {
			return true
		}
	}
	return false
}

// defaultInlineElements are treated as inline markup wherever they appear —
// they become tag chips inside a segment rather than splitting it. Unknown
// vocabularies are handled by the mixed-content heuristic in childrenAreInline
// instead of by name.
var defaultInlineElements = map[string]bool{
	"b":      true,
	"i":      true,
	"em":     true,
	"strong": true,
	"u":      true,
	"sup":    true,
	"sub":    true,
	"br":     true,
	"span":   true,
	"a":      true,
	"code":   true,
	"tt":     true,
	"small":  true,
	"big":    true,
}

// childrenAreInline reports whether every element child of n counts as inline
// markup, which is what makes n a candidate segment container.
//
// Two signals, either sufficient per child:
//   - the child's name is in defaultInlineElements
//   - n has mixed content (its own text alongside elements), which means the
//     elements are embedded in a sentence — this is what catches <meta-ref>
//     and any other unknown vocabulary without hardcoding names
func (n *XMLNode) ChildrenAreInline(src string) bool {
	if len(n.Children) == 0 {
		return true
	}
	mixed := n.hasDirectText(src)
	for _, c := range n.Children {
		if !mixed && !defaultInlineElements[c.Name] {
			return false
		}
	}
	return true
}

// isTranslatable reports whether n opts out of translation. Mirrors the
// conventions the HTML and Android parsers already honour.
func (n *XMLNode) IsTranslatable() bool {
	return n.Attr("translate") != "no" && n.Attr("translatable") != "false"
}

// referenceElementNames are cross-reference elements recognised by name, for
// vocabularies that don't mark them with a link attribute.
var referenceElementNames = map[string]bool{
	"meta-ref": true,
}

// isReferencePlaceholder reports whether n is a cross-reference whose text is
// generated from another node rather than authored here.
//
// ST4's `<meta-ref linkid="datanode_…">Eco</meta-ref>` renders the value of the
// node it points at — a node that is itself a translatable segment in the same
// file. The text sitting inside the reference is a cached rendering, so
// translating it would duplicate the term (and invite the two copies to
// disagree). The CMS re-resolves it on publish.
//
// Such an element becomes a single non-editable placeholder chip: its content
// stays out of the segment, out of the word count, and out of TM lookup, which
// is what lets our source text line up with a TM built by a tool that models it
// the same way (`<ph>` in TMX).
//
// Distinct from isTranslatable: `translate="no"` skips the subtree entirely and
// emits no chip. A reference still needs one — the translator must see that
// something belongs there, and export needs an anchor to restore it.
func (n *XMLNode) IsReferencePlaceholder() bool {
	// Attribute first: it generalises past any single vocabulary's element name.
	if n.Attr("linkid") != "" {
		return true
	}
	return referenceElementNames[n.Name]
}

// --- text decoding with an offset map ---

// XMLUnescape decodes XML character references and named entities, returning
// the decoded text alongside an index map: offsets[i] is the byte offset in
// raw that produced decoded byte i. offsets has len(plain)+1 entries, the last
// being len(raw), so a decoded range maps cleanly onto a source range.
//
// The offset map is the whole point — sentence splitting works on decoded
// text, but splicing has to happen at raw byte positions, and "&lt;" is four
// raw bytes for one decoded one.
func XMLUnescape(raw string) (string, []int) {
	// Fast path: no entities means decoded and raw are byte-identical.
	if !strings.ContainsRune(raw, '&') {
		offsets := make([]int, len(raw)+1)
		for i := range offsets {
			offsets[i] = i
		}
		return raw, offsets
	}

	var b strings.Builder
	offsets := make([]int, 0, len(raw)+1)

	i := 0
	for i < len(raw) {
		if raw[i] != '&' {
			b.WriteByte(raw[i])
			offsets = append(offsets, i)
			i++
			continue
		}

		semi := strings.IndexByte(raw[i:], ';')
		// Entity names are short; a distant or missing ';' means this is a
		// bare ampersand, not a reference.
		if semi < 0 || semi > 32 {
			b.WriteByte(raw[i])
			offsets = append(offsets, i)
			i++
			continue
		}

		body := raw[i+1 : i+semi]
		decoded, ok := decodeXMLEntity(body)
		if !ok {
			b.WriteByte(raw[i])
			offsets = append(offsets, i)
			i++
			continue
		}

		// Every byte of the decoded value maps back to the '&' that produced
		// it, so a break can never land inside a reference.
		for j := 0; j < len(decoded); j++ {
			offsets = append(offsets, i)
		}
		b.WriteString(decoded)
		i += semi + 1
	}

	offsets = append(offsets, len(raw))
	return b.String(), offsets
}

// decodeXMLEntity decodes the body of an entity reference (the part between
// '&' and ';'), returning the replacement text.
func decodeXMLEntity(body string) (string, bool) {
	if body == "" {
		return "", false
	}
	if body[0] == '#' {
		var code int
		var err error
		if len(body) > 1 && (body[1] == 'x' || body[1] == 'X') {
			_, err = fmt.Sscanf(body[2:], "%x", &code)
		} else {
			_, err = fmt.Sscanf(body[1:], "%d", &code)
		}
		if err != nil || code <= 0 || code > utf8.MaxRune {
			return "", false
		}
		return string(rune(code)), true
	}
	switch body {
	case "lt":
		return "<", true
	case "gt":
		return ">", true
	case "amp":
		return "&", true
	case "quot":
		return `"`, true
	case "apos":
		return "'", true
	}
	if v, ok := xml.HTMLEntity[body]; ok {
		return v, true
	}
	return "", false
}

// --- cross-reference resolution ---

// ReferenceIndex maps a defining node's identity to the segment key that will
// carry its text, so a cross-reference can be resolved to the segment whose
// translation it renders.
//
// The key is "<tmsnode id>\x00<meta name>": an ST4 reference names both, via
// the tail of its linkid (`datanode_1001_200000000001` → `200000000001`) and
// its `name` attribute, which matches the defining `<meta>`'s.
type ReferenceIndex map[string]string

func refIndexKey(nodeID, metaName string) string { return nodeID + "\x00" + metaName }

// BuildReferenceIndex walks the document once and records where every named
// `<meta>` lives. Segment keys come from XMLNodePath, which depends only on a
// node's position, so this can run before segments are collected.
func BuildReferenceIndex(doc *XMLDoc) ReferenceIndex {
	idx := ReferenceIndex{}
	if doc == nil || doc.Root == nil {
		return idx
	}

	var walk func(n *XMLNode, containerID string)
	walk = func(n *XMLNode, containerID string) {
		if id := n.Attr("id"); id != "" {
			containerID = id
		}
		if n.Name == "meta" && containerID != "" {
			if name := n.Attr("name"); name != "" {
				idx[refIndexKey(containerID, name)] = XMLNodePath(n)
			}
		}
		for _, c := range n.Children {
			walk(c, containerID)
		}
	}
	walk(doc.Root, "")
	return idx
}

// referenceKey returns the segment key whose translation n renders, or "" when
// n isn't a resolvable cross-reference — a linkid pointing outside this
// document, most commonly. Callers fall back to the reference's own content.
func (idx ReferenceIndex) ReferenceKey(n *XMLNode) string {
	linkid := n.Attr("linkid")
	if linkid == "" {
		return ""
	}
	// The node id is the last underscore-separated component.
	nodeID := linkid[strings.LastIndex(linkid, "_")+1:]
	if nodeID == "" {
		return ""
	}
	return idx[refIndexKey(nodeID, n.Attr("name"))]
}

// childrenOrNone returns n's children, or none for a nil n.
func (n *XMLNode) ChildrenOrNone() []*XMLNode {
	if n == nil {
		return nil
	}
	return n.Children
}

// childrenNamed returns n's child elements with the given local name. A nil
// n has none.
func (n *XMLNode) ChildrenNamed(name string) []*XMLNode {
	if n == nil {
		return nil
	}
	var out []*XMLNode
	for _, c := range n.Children {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// XMLNodePath builds a stable, self-describing key: an element path with
// sibling indices where a name repeats, e.g.
// /tms/tmsnode[24]/content/table-container/table/tbody/tr[8]/td[3]/p[2].
//
// Chosen over the sequential seg:N scheme the HTML and Markdown parsers use because
// it survives unrelated edits elsewhere in the document and tells you where a
// stray "Range" cell came from when you're staring at the segment grid.
func XMLNodePath(n *XMLNode) string {
	if n.Keys.path != "" {
		return n.Keys.path
	}
	part := n.Name
	if idx, needed := siblingIndex(n); needed {
		part = fmt.Sprintf("%s[%d]", n.Name, idx)
	}
	if n.Parent == nil {
		n.Keys.path = "/" + part
	} else {
		n.Keys.path = XMLNodePath(n.Parent) + "/" + part
	}
	return n.Keys.path
}

// siblingIndex returns n's 1-based position among same-named siblings, and
// whether an index is needed to disambiguate at all.
func siblingIndex(n *XMLNode) (int, bool) {
	p := n.Parent
	if p == nil {
		return 1, false
	}
	if !p.Keys.siblingsIndexed {
		counts := make(map[string]int)
		for _, c := range p.Children {
			counts[c.Name]++
			c.Keys.sibIdx = counts[c.Name]
		}
		for _, c := range p.Children {
			c.Keys.sibNeeded = counts[c.Name] > 1
		}
		p.Keys.siblingsIndexed = true
	}
	return n.Keys.sibIdx, n.Keys.sibNeeded
}

func XMLAttr(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
