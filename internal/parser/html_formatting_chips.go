package parser

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// formattingOnlyChips reads a block the way segments keyed seg:N store their
// source when only formatting elements (htmlCtypeMap) and line breaks are
// chips: every other element contributes its text alone, and the whole block
// is one segment. It returns that plain text, that rich text ("" when the
// block has no chip) and the element behind each chip id. A translation whose
// segment stores this source numbers its chips the same way (see
// HTMLWithStoredSource).
func formattingOnlyChips(block *html.Node) (plain, rich string, inlines []*html.Node) {
	var p, b strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		switch c.Type {
		case html.TextNode:
			p.WriteString(c.Data)
			b.WriteString(c.Data)
		case html.ElementNode:
			if c.DataAtom == atom.Br {
				inlines = append(inlines, c)
				p.WriteString("\n")
				fmt.Fprintf(&b, `<x id="%d" ctype="lb"/>`, len(inlines))
				return
			}
			if ctype, ok := htmlCtypeMap[c.Data]; ok {
				inlines = append(inlines, c)
				fmt.Fprintf(&b, `<g id="%d" ctype="%s"%s>`, len(inlines), ctype, chipOriginalMarkup(htmlStartTag(c), len(c.Attr)))
				for gc := c.FirstChild; gc != nil; gc = gc.NextSibling {
					walk(gc)
				}
				b.WriteString("</g>")
				return
			}
			for gc := c.FirstChild; gc != nil; gc = gc.NextSibling {
				walk(gc)
			}
		}
	}
	for c := block.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	if len(inlines) == 0 {
		return p.String(), "", nil
	}
	return p.String(), b.String(), inlines
}

// chipNumbering is the element list a translation's chip ids index: the
// run's own inlines, or, when the segment's stored source is the block's
// formatting-only reading and not the run's, that reading's chips. Text and
// rich text both decide, so a block without chips is told apart by its text;
// a chip's original_markup copy does not, since segments may be stored
// without it. wholeBlock reports that the stored translation is of the whole
// block and the run holds only part of it, so it replaces the whole block.
func (p HTML) chipNumbering(u htmlUnit) (inlines []*html.Node, wholeBlock bool) {
	if p.stored == nil || u.block == nil {
		return u.inlines, false
	}
	stored, ok := (*p.stored)[u.key]
	if !ok || sameSource(stored, u.plain, u.rich) {
		return u.inlines, false
	}
	plain, rich, chips := formattingOnlyChips(u.block)
	if sameSource(stored, plain, rich) {
		return chips, !runHoldsBlock(u, plain, chips)
	}
	return u.inlines, false
}

// runHoldsBlock reports whether the run holds all of its block's text and
// chips, so that what the block has outside the run is whitespace HTML
// collapses (a non-breaking space is text). The run's text is collapsed and
// the block's raw, so both are compared collapsed.
func runHoldsBlock(u htmlUnit, blockPlain string, chips []*html.Node) bool {
	if collapseHTMLSpace(u.plain) != collapseHTMLSpace(blockPlain) {
		return false
	}
	inRun := map[*html.Node]bool{}
	for _, n := range u.nodes {
		inRun[n] = true
	}
	for _, c := range chips {
		for c != nil && c.Parent != u.parent {
			c = c.Parent
		}
		if c == nil || !inRun[c] {
			return false
		}
	}
	return true
}

// collapseHTMLSpace is s with each run of HTML whitespace as one space and none
// at the ends.
func collapseHTMLSpace(s string) string {
	return strings.Join(strings.FieldsFunc(s, isHTMLSpace), " ")
}

// isHTMLSpace reports whether r is whitespace HTML collapses.
func isHTMLSpace(r rune) bool {
	return strings.ContainsRune(htmlSpace, r)
}

// sameSource reports whether a stored source is this plain and rich text,
// ignoring the chips' original_markup copies.
func sameSource(stored StoredSource, plain, rich string) bool {
	return stored.Text == plain && chipSkeleton(stored.RichText) == chipSkeleton(rich)
}

// htmlSpace is the whitespace HTML collapses.
const htmlSpace = " \t\n\f\r"

var chipMarkupCopyRE = regexp.MustCompile(` original_markup="[^"]*"`)

// chipSkeleton is rich text without the chips' original_markup copies.
func chipSkeleton(rich string) string {
	return chipMarkupCopyRE.ReplaceAllString(rich, "")
}
