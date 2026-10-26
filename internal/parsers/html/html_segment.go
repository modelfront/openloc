package html

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"openloc/internal/parsers/common"
)

// HTML segmentation: the elements below start and end a segment; every other
// element inside one is an inline tag that the export puts back exactly as the
// source has it. A block inside a block closes the outer segment, so the text
// before it, the nested block and the text after it are separate segments.
// Translatable attributes are segments of their own.

func atomSet(atoms ...atom.Atom) map[atom.Atom]bool {
	m := make(map[atom.Atom]bool, len(atoms))
	for _, a := range atoms {
		m[a] = true
	}
	return m
}

// htmlBlockElements start and end a segment.
var htmlBlockElements = atomSet(
	atom.Html, atom.Head, atom.Body, atom.Title,
	atom.Address, atom.Article, atom.Aside, atom.Blockquote, atom.Caption,
	atom.Colgroup, atom.Dd, atom.Details, atom.Dialog, atom.Div, atom.Dl,
	atom.Dt, atom.Fieldset, atom.Figcaption, atom.Figure, atom.Footer,
	atom.Form, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
	atom.Header, atom.Hgroup, atom.Hr, atom.Legend, atom.Li, atom.Main,
	atom.Menu, atom.Nav, atom.Ol, atom.Optgroup, atom.Option, atom.P,
	atom.Section, atom.Select, atom.Summary, atom.Table, atom.Tbody,
	atom.Td, atom.Tfoot, atom.Th, atom.Thead, atom.Tr, atom.Ul,
)

// htmlSkipBlocks end a segment and are never translated.
var htmlSkipBlocks = atomSet(atom.Pre)

// htmlOpaqueElements are never translated. Inside a segment each one is a
// single placeholder tag that carries the whole element.
var htmlOpaqueElements = atomSet(
	atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Textarea,
	atom.Svg, atom.Math, atom.Iframe, atom.Object, atom.Video, atom.Audio,
	atom.Canvas,
)

// htmlVoidElements have no content; inside a segment each is a placeholder tag.
var htmlVoidElements = atomSet(
	atom.Area, atom.Base, atom.Col, atom.Embed, atom.Img, atom.Input,
	atom.Link, atom.Meta, atom.Param, atom.Source, atom.Track, atom.Wbr,
)

// htmlLegacyBlocks and htmlLegacySkip give the segment keys their numbers:
// each outermost element of the first set that holds text is seg:N. Files
// imported before the segmentation above still find their translations.
var (
	htmlLegacyBlocks = atomSet(
		atom.P, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
		atom.Li, atom.Td, atom.Th, atom.Dt, atom.Dd, atom.Figcaption,
		atom.Blockquote, atom.Title, atom.Caption,
	)
	htmlLegacySkip = atomSet(atom.Script, atom.Style, atom.Code, atom.Pre, atom.Noscript)
)

// htmlCtypeMap maps HTML element names to XLIFF ctype values per the OASIS
// XLIFF 1.2 HTML profile. Other elements get "x-html-<name>".
var htmlCtypeMap = map[string]string{
	"b":      "bold",
	"i":      "italic",
	"em":     "x-html-em",
	"strong": "x-html-strong",
	"u":      "underlined",
	"a":      "x-html-a",
	"span":   "x-html-span",
	"sub":    "x-html-sub",
	"sup":    "x-html-sup",
	"code":   "x-html-code",
}

func htmlCtype(name string) string {
	if ctype, ok := htmlCtypeMap[name]; ok {
		return ctype
	}
	return "x-html-" + name
}

// htmlAttrSegments names the attributes translated per element.
var htmlAttrSegments = map[atom.Atom][]string{
	atom.Img:   {"alt", "title"},
	atom.A:     {"title"},
	atom.Abbr:  {"title"},
	atom.Input: {"alt", "value", "title", "placeholder"},
	atom.Td:    {"abbr"},
	atom.Th:    {"abbr"},
}

// htmlInputNoText are input types whose attributes are never translated.
var htmlInputNoText = map[string]bool{"file": true, "hidden": true, "image": true, "password": true}

// htmlUnit is one segment and where it lives in the document: a run of a
// parent's children, or an attribute of an element.
type htmlUnit struct {
	key, plain, rich, context string

	parent  *html.Node   // run: the element whose children the run is
	nodes   []*html.Node // run: the children, in order
	inlines []*html.Node // run: the element behind chip id i+1

	elem *html.Node // attribute
	attr string

	isRun  bool
	owner  int // legacy number of the enclosing legacy block, 0 if none
	anchor int // legacy number of the last legacy block entered

	// block is the legacy block of the run keyed seg:N after it, nil for
	// every other unit.
	block *html.Node
}

type htmlCollector struct {
	legacy    map[*html.Node]int
	hasBlock  map[*html.Node]bool
	units     []htmlUnit
	anchor    int
	owner     int
	ownerNode *html.Node // the legacy block behind owner
}

// collectHTMLUnits finds every segment of doc in document order. firstSeq is
// the number of segments already taken (the meta description).
func collectHTMLUnits(doc *html.Node, firstSeq int) []htmlUnit {
	c := &htmlCollector{
		legacy:   map[*html.Node]int{},
		hasBlock: map[*html.Node]bool{},
		anchor:   firstSeq,
	}
	seq := firstSeq
	numberLegacyBlocks(doc, c.legacy, &seq)
	markBlockHolders(doc, c.hasBlock)
	c.container(doc)
	assignHTMLKeys(c.units)
	return c.units
}

// numberLegacyBlocks numbers each outermost legacy block that holds text.
func numberLegacyBlocks(n *html.Node, out map[*html.Node]int, seq *int) {
	if n.Type == html.ElementNode {
		if hasAttr(n, "translate", "no") || htmlLegacySkip[n.DataAtom] {
			return
		}
		if htmlLegacyBlocks[n.DataAtom] {
			if hasText(n) {
				*seq++
				out[n] = *seq
			}
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		numberLegacyBlocks(c, out, seq)
	}
}

func hasText(n *html.Node) bool {
	if n.Type == html.TextNode {
		return strings.TrimSpace(n.Data) != ""
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if hasText(c) {
			return true
		}
	}
	return false
}

// markBlockHolders records every element with a block element below it, and
// reports whether n is or holds one.
func markBlockHolders(n *html.Node, out map[*html.Node]bool) bool {
	if n.Type == html.ElementNode && htmlOpaqueElements[n.DataAtom] {
		return false
	}
	holds := false
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if markBlockHolders(c, out) {
			holds = true
		}
	}
	if holds {
		out[n] = true
	}
	return holds || n.Type == html.ElementNode && (htmlBlockElements[n.DataAtom] || htmlSkipBlocks[n.DataAtom])
}

// inRun reports whether n belongs inside a segment rather than ending one.
func (c *htmlCollector) inRun(n *html.Node) bool {
	switch n.Type {
	case html.TextNode, html.CommentNode:
		return true
	case html.ElementNode:
		if htmlOpaqueElements[n.DataAtom] {
			return true
		}
		return !htmlBlockElements[n.DataAtom] && !htmlSkipBlocks[n.DataAtom] && !c.hasBlock[n]
	}
	return false
}

// container walks n's children, cutting them into runs at every block.
func (c *htmlCollector) container(n *html.Node) {
	var run []*html.Node
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if c.inRun(ch) {
			run = append(run, ch)
			continue
		}
		c.flushRun(n, run)
		run = nil
		if ch.Type == html.ElementNode {
			c.block(ch)
		}
	}
	c.flushRun(n, run)
}

// block handles an element that ends a segment.
func (c *htmlCollector) block(n *html.Node) {
	if hasAttr(n, "translate", "no") || htmlSkipBlocks[n.DataAtom] {
		return
	}
	prevOwner, prevNode := c.owner, c.ownerNode
	if num, ok := c.legacy[n]; ok {
		c.owner, c.anchor, c.ownerNode = num, num, n
	}
	c.container(n)
	c.addAttrUnits(n)
	c.owner, c.ownerNode = prevOwner, prevNode
}

func (c *htmlCollector) addAttrUnits(n *html.Node) {
	names := htmlAttrSegments[n.DataAtom]
	if len(names) == 0 || n.DataAtom == atom.Input && htmlInputNoText[strings.ToLower(getAttr(n, "type"))] {
		return
	}
	for _, a := range n.Attr {
		if a.Namespace != "" || strings.TrimSpace(a.Val) == "" || !containsString(names, a.Key) {
			continue
		}
		c.units = append(c.units, htmlUnit{
			plain: a.Val, elem: n, attr: a.Key,
			context: n.Data + ":" + a.Key,
			owner:   c.owner, anchor: c.anchor,
		})
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// flushRun adds a segment for a run of inline content that has text, followed
// by the attribute segments of its inline elements.
func (c *htmlCollector) flushRun(parent *html.Node, nodes []*html.Node) {
	if len(nodes) == 0 {
		return
	}
	r := htmlRun{collector: c}
	attrsFrom := len(c.units)
	for _, n := range nodes {
		r.node(n, false)
	}
	if !r.prose {
		return
	}
	rich := ""
	if len(r.inlines) > 0 {
		rich = trimTrailingSpace(r.rich.String())
	}
	unit := htmlUnit{
		plain: strings.TrimRight(r.plain.String(), " "), rich: rich,
		parent: parent, nodes: nodes, inlines: r.inlines,
		isRun: true, owner: c.owner, anchor: c.anchor, block: c.ownerNode,
	}
	c.units = append(c.units, htmlUnit{})
	copy(c.units[attrsFrom+1:], c.units[attrsFrom:])
	c.units[attrsFrom] = unit
}

// htmlRun builds a run's plain and rich text. Chip ids count from 1 in the
// order the elements open.
//
// Whitespace is written as the browser renders it: a run of markup whitespace
// becomes one space, and none is kept at the run's edges or after a line
// break. A space stays on its side of every tag: the browser would collapse
// some of them, but an empty element may be filled by a script, and sentence
// boundaries depend on whether a space sits inside a chip.
type htmlRun struct {
	collector   *htmlCollector
	plain, rich strings.Builder
	inlines     []*html.Node
	prose       bool // text outside <code>

	started   bool // something visible is written: text, a void chip, a closed element
	lineStart bool // just after a line break
	pending   bool // a collapsed space waits for the next text or opening tag
}

// text writes a text node's content with its whitespace collapsed.
func (r *htmlRun) text(s string) {
	for _, c := range s {
		if isHTMLSpace(c) {
			r.pending = r.started && !r.lineStart
			continue
		}
		r.flushSpace()
		r.plain.WriteRune(c)
		r.rich.WriteRune(c)
		r.started, r.lineStart = true, false
	}
}

// trimTrailingSpace drops the collapsed spaces at the end of a run's rich
// text, including those just inside its closing tags.
func trimTrailingSpace(rich string) string {
	closing := ""
	for {
		switch {
		case strings.HasSuffix(rich, "</g>"):
			rich, closing = rich[:len(rich)-len("</g>")], "</g>"+closing
		case strings.HasSuffix(rich, " "):
			rich = rich[:len(rich)-1]
		default:
			return rich + closing
		}
	}
}

// flushSpace writes a waiting collapsed space.
func (r *htmlRun) flushSpace() {
	if r.pending {
		r.plain.WriteByte(' ')
		r.rich.WriteByte(' ')
		r.pending = false
	}
}

func (r *htmlRun) chip(n *html.Node) int {
	r.inlines = append(r.inlines, n)
	return len(r.inlines)
}

func (r *htmlRun) node(n *html.Node, inCode bool) {
	switch n.Type {
	case html.TextNode:
		r.text(n.Data)
		if !inCode && strings.TrimSpace(n.Data) != "" {
			r.prose = true
		}
		return
	case html.CommentNode:
		r.flushSpace()
		fmt.Fprintf(&r.rich, `<x id="%d" ctype="x-html-comment"/>`, r.chip(n))
		return
	case html.ElementNode:
	default:
		return
	}
	switch {
	case n.DataAtom == atom.Br:
		r.pending = false
		r.plain.WriteString("\n")
		fmt.Fprintf(&r.rich, `<x id="%d" ctype="lb"/>`, r.chip(n))
		r.started, r.lineStart = true, true
	case htmlOpaqueElements[n.DataAtom] || hasAttr(n, "translate", "no"):
		r.flushSpace()
		fmt.Fprintf(&r.rich, `<x id="%d" ctype="%s"%s/>`, r.chip(n), htmlCtype(n.Data), common.ChipOriginalMarkup(htmlStartTag(n), len(n.Attr)))
		r.started, r.lineStart = true, false
	case htmlVoidElements[n.DataAtom]:
		r.flushSpace()
		fmt.Fprintf(&r.rich, `<x id="%d" ctype="%s"%s/>`, r.chip(n), htmlCtype(n.Data), common.ChipOriginalMarkup(htmlStartTag(n), len(n.Attr)))
		r.started, r.lineStart = true, false
		r.collector.addAttrUnits(n)
	default:
		// The start tag travels with the chip so the editor can show the
		// href, class and other attributes.
		r.flushSpace()
		fmt.Fprintf(&r.rich, `<g id="%d" ctype="%s"%s>`, r.chip(n), htmlCtype(n.Data), common.ChipOriginalMarkup(htmlStartTag(n), len(n.Attr)))
		r.collector.addAttrUnits(n)
		code := inCode || n.DataAtom == atom.Code
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			r.node(ch, code)
		}
		r.flushSpace()
		r.rich.WriteString("</g>")
		r.started = true
	}
}

// assignHTMLKeys gives the first run inside each legacy block that block's
// key, seg:N, and every other unit seg:N.k, where N is the last legacy block
// entered and k counts from 1.
func assignHTMLKeys(units []htmlUnit) {
	claimed := map[int]bool{}
	sub := map[int]int{}
	for i := range units {
		u := &units[i]
		if u.isRun && u.owner > 0 && !claimed[u.owner] {
			claimed[u.owner] = true
			u.key = fmt.Sprintf("seg:%d", u.owner)
			continue
		}
		u.block = nil
		sub[u.anchor]++
		u.key = fmt.Sprintf("seg:%d.%d", u.anchor, sub[u.anchor])
	}
}
