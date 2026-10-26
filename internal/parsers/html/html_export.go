package html

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"openloc/internal/parsers/common"
)

// The HTML export writes a stored translation into the customer's file, and a
// translation is whatever an editor saved. Only the editor's own bare marks
// and the source's inline elements may come out as elements.

// editorFormattingRE matches the closing tag of the formatting the editor
// applies itself: bold, italic, subscript, superscript. A translation that
// carries one is the editor's own HTML, its text already escaped.
var editorFormattingRE = regexp.MustCompile(`</(?:strong|em|sub|sup)>`)

// editorTagRE matches, at the start of a string, a bare tag the editor
// writes: its marks and the Shift+Enter line break. They carry no attributes.
var editorTagRE = regexp.MustCompile(`^(?:</?(?:strong|em|sub|sup|u|s|code)>|<br\s*/?>)`)

var (
	// editorParagraphBreakRE matches the editor's paragraph split, which a
	// segment exports as a line break; editorParagraphTagRE matches any other
	// paragraph tag, which is dropped.
	editorParagraphBreakRE = regexp.MustCompile(`^</p>\s*<p>`)
	editorParagraphTagRE   = regexp.MustCompile(`^</?p>`)
	// editorLinkRE matches the editor's link start tag; the href is the only
	// attribute kept.
	editorLinkRE = regexp.MustCompile(`^<a(?:\s[^<>]*)?>`)
	linkHrefRE   = regexp.MustCompile(`\shref="([^"]*)"`)
	safeLinkRE   = regexp.MustCompile(`(?i)^(?:https?:|mailto:)`)
	chipCtypeRE  = regexp.MustCompile(`\sctype="([\w.-]+)"`)
)

// htmlTranslationMarkup turns a stored translation into HTML for a run.
// Stored text is characters, so every run outside the chips is escaped before
// the chips become elements: a literal "<b>" or "&lt;" stays text. Editor
// text keeps the tags the editor writes. Each chip becomes a placeholder
// element that restoreHTMLChips swaps for the source's own element, so a
// translation adds no element or attribute the source does not have.
func htmlTranslationMarkup(translated string) string {
	editorHTML := editorFormattingRE.MatchString(translated)
	return escapeOutsideChips(translated, editorHTML)
}

// escapeOutsideChips turns each <g>/<x> chip tag into a placeholder element
// that keeps only the chip's id and ctype, and turns every other '<' into text.
// Plain text is HTML-escaped as well. With editorHTML the text is already
// escaped and is copied as is, the editor's bare tags stay tags, a paragraph
// split becomes <br>, and its links keep a web or mail href.
func escapeOutsideChips(s string, editorHTML bool) string {
	return escapeAroundChips(s, editorHTML, htmlChipPlaceholder)
}

// escapeTextKeepChips escapes a stored translation's text and every '<' that
// is not a chip, and keeps the chip tags as they are, so the result can be
// joined with editor HTML and read again by htmlTranslationMarkup.
func escapeTextKeepChips(s string) string {
	return escapeAroundChips(s, false, func(_ common.ChipTag, raw string) string { return raw })
}

// escapeAroundChips is escapeOutsideChips with chip writing up to chip, which
// gets each scanned chip tag and its raw text.
func escapeAroundChips(s string, editorHTML bool, chip func(tag common.ChipTag, raw string) string) string {
	text := html.EscapeString
	if editorHTML {
		text = func(s string) string { return s }
	}
	var b strings.Builder
	for s != "" {
		lt := strings.IndexByte(s, '<')
		if lt < 0 {
			b.WriteString(text(s))
			break
		}
		b.WriteString(text(s[:lt]))
		s = s[lt:]
		if tag, next, ok := common.ScanChipTag(s, 0); ok {
			b.WriteString(chip(tag, s[:next]))
			s = s[next:]
			continue
		}
		if editorHTML {
			if brk := editorParagraphBreakRE.FindString(s); brk != "" {
				b.WriteString("<br>")
				s = s[len(brk):]
				continue
			}
			if tag := editorParagraphTagRE.FindString(s); tag != "" {
				s = s[len(tag):]
				continue
			}
			if tag := editorTagRE.FindString(s); tag != "" {
				b.WriteString(tag)
				s = s[len(tag):]
				continue
			}
			if tag := editorLinkRE.FindString(s); tag != "" {
				b.WriteString(editorLinkStart(tag))
				s = s[len(tag):]
				continue
			}
			if strings.HasPrefix(s, "</a>") {
				b.WriteString("</a>")
				s = s[len("</a>"):]
				continue
			}
		}
		b.WriteString("&lt;")
		s = s[1:]
	}
	return b.String()
}

// Placeholder elements stand for chips while a translation is parsed as HTML.
const (
	htmlPairedChip = "chip-g"
	htmlVoidChip   = "chip-x"
)

// htmlChipPlaceholder writes a scanned chip tag as a placeholder element with
// the chip's id and ctype. Anything else in raw (an event handler, a style, a
// translator-written original_markup) is dropped.
func htmlChipPlaceholder(tag common.ChipTag, raw string) string {
	if tag.Closing {
		if tag.Name == "g" {
			return "</" + htmlPairedChip + ">"
		}
		return ""
	}
	ctype := ""
	if m := chipCtypeRE.FindStringSubmatch(raw); m != nil {
		ctype = m[1]
	}
	if tag.Name == "x" {
		return fmt.Sprintf(`<%s data-id="%d" data-ctype="%s"></%s>`, htmlVoidChip, tag.Id, ctype, htmlVoidChip)
	}
	return fmt.Sprintf(`<%s data-id="%d" data-ctype="%s">`, htmlPairedChip, tag.Id, ctype)
}

// editorLinkStart rebuilds an editor link's start tag with its href alone,
// and only for a web or mail address; any other link keeps no href.
func editorLinkStart(tag string) string {
	m := linkHrefRE.FindStringSubmatch(tag)
	if m == nil {
		return "<a>"
	}
	href := html.UnescapeString(m[1])
	if !safeLinkRE.MatchString(strings.TrimSpace(href)) {
		return "<a>"
	}
	return `<a href="` + html.EscapeString(href) + `">`
}

// joinedHTMLMarkup is the HTML for a run whose block was split into
// sentences, from its pieces. Only a translation can be editor HTML: when one
// carries the editor's formatting, every other piece has its text escaped and
// its chips kept, and the block is read as editor HTML; otherwise the whole
// block is stored text. Source text that spells an editor tag stays text.
func joinedHTMLMarkup(pieces []common.SentencePiece) string {
	isEditor := func(p common.SentencePiece) bool { return p.Translated && editorFormattingRE.MatchString(p.Text) }
	editorHTML := false
	for _, p := range pieces {
		editorHTML = editorHTML || isEditor(p)
	}
	if !editorHTML {
		return escapeOutsideChips(common.JoinPieces(pieces), false)
	}
	var b strings.Builder
	for _, p := range pieces {
		if isEditor(p) {
			b.WriteString(p.Text)
		} else {
			b.WriteString(escapeTextKeepChips(p.Text))
		}
	}
	return escapeOutsideChips(b.String(), true)
}

// replaceHTMLRun writes a run's markup (htmlTranslationMarkup or
// joinedHTMLMarkup) over the run's nodes in the document.
func replaceHTMLRun(u htmlUnit, markup string, inlines []*html.Node) error {
	// A run inside a block a whole-block translation already replaced is gone.
	for _, n := range u.nodes {
		if n.Parent != u.parent {
			return nil
		}
	}
	fragment, err := html.ParseFragment(strings.NewReader(markup), u.parent)
	if err != nil {
		return fmt.Errorf("html translation %s: %w", u.key, err)
	}
	next := u.nodes[len(u.nodes)-1].NextSibling
	for _, n := range u.nodes {
		u.parent.RemoveChild(n)
	}
	holder := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, n := range fragment {
		holder.AppendChild(n)
	}
	restoreHTMLChips(holder, inlines)
	for holder.FirstChild != nil {
		n := holder.FirstChild
		holder.RemoveChild(n)
		u.parent.InsertBefore(n, next)
	}
	return nil
}

// restoreHTMLChips swaps every placeholder below n for the source element its
// chip id names. A paired chip whose id the source lacks becomes the bare
// element its ctype names, or just its content; a void one becomes a line
// break when its ctype says so and is dropped otherwise.
func restoreHTMLChips(n *html.Node, inlines []*html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		restoreHTMLChips(c, inlines)
		if c.Type == html.ElementNode && (c.Data == htmlPairedChip || c.Data == htmlVoidChip) {
			for _, r := range htmlChipReplacement(c, inlines) {
				n.InsertBefore(r, c)
			}
			n.RemoveChild(c)
		}
		c = next
	}
}

// htmlChipSource is the source element a chip names: the id-th inline of the
// list when its type is the chip's, else nil.
func htmlChipSource(inlines []*html.Node, rawID, ctype string) *html.Node {
	id, err := strconv.Atoi(rawID)
	if err != nil || id < 1 || id > len(inlines) {
		return nil
	}
	if ctype != "" && htmlNodeCtype(inlines[id-1]) != ctype {
		return nil
	}
	return inlines[id-1]
}

// htmlNodeCtype is the ctype the parser gives a run's inline node.
func htmlNodeCtype(n *html.Node) string {
	switch {
	case n.Type == html.CommentNode:
		return "x-html-comment"
	case n.DataAtom == atom.Br:
		return "lb"
	}
	return htmlCtype(n.Data)
}

func htmlChipReplacement(chip *html.Node, inlines []*html.Node) []*html.Node {
	ctype := getAttr(chip, "data-ctype")
	src := htmlChipSource(inlines, getAttr(chip, "data-id"), ctype)
	// An element that is not translated comes back whole, whatever the chip
	// holds.
	if src != nil && hasAttr(src, "translate", "no") {
		return []*html.Node{cloneHTMLNode(src, true)}
	}
	paired := src != nil && htmlPairedSource(src)

	if chip.Data == htmlVoidChip {
		switch {
		case src != nil && !paired:
			return []*html.Node{cloneHTMLNode(src, true)}
		case src == nil && ctype == "lb":
			return []*html.Node{{Type: html.ElementNode, Data: "br", DataAtom: atom.Br}}
		}
		return nil
	}

	var wrapper *html.Node
	switch {
	case paired:
		wrapper = cloneHTMLNode(src, false)
	case src == nil:
		if name, ok := common.InlineCtypeToHTMLMap[ctype]; ok {
			wrapper = &html.Node{Type: html.ElementNode, Data: name, DataAtom: atom.Lookup([]byte(name))}
		}
	}
	var children []*html.Node
	for c := chip.FirstChild; c != nil; c = chip.FirstChild {
		chip.RemoveChild(c)
		children = append(children, c)
	}
	if wrapper == nil {
		return children
	}
	for _, c := range children {
		wrapper.AppendChild(c)
	}
	return []*html.Node{wrapper}
}

// htmlPairedSource reports whether a run's inline element is a paired chip,
// one whose content is translated.
func htmlPairedSource(n *html.Node) bool {
	return n.Type == html.ElementNode && n.DataAtom != atom.Br &&
		!htmlVoidElements[n.DataAtom] && !htmlOpaqueElements[n.DataAtom] &&
		!hasAttr(n, "translate", "no")
}

// cloneHTMLNode copies n with its attributes, and its subtree when deep.
func cloneHTMLNode(n *html.Node, deep bool) *html.Node {
	c := &html.Node{
		Type: n.Type, DataAtom: n.DataAtom, Data: n.Data, Namespace: n.Namespace,
		Attr: append([]html.Attribute(nil), n.Attr...),
	}
	if deep {
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			c.AppendChild(cloneHTMLNode(ch, true))
		}
	}
	return c
}
