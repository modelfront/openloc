package pptx

import (
	"fmt"
	"openloc/internal/parsers/common"
	"strings"
)

// maxPPTXShapeDepth caps how deep groups and alternate content may nest. The
// walk recurses per level, and a crafted deck could otherwise nest deep
// enough to exhaust the stack, which no recover can catch.
const maxPPTXShapeDepth = 64

// Graphic frame content types, matched by suffix so the transitional and the
// Strict OOXML namespaces both work.
const (
	pptxURITable   = "/table"
	pptxURIDiagram = "/diagram"
)

// pptxUnit is one translatable place in a deck: a paragraph or an alt text.
// It becomes one segment per sentence.
type pptxUnit struct {
	part    string
	key     string
	context string
	para    *pptxParagraph
	alt     *pptxAlt
	// mirrors are the SmartArt drawing paragraphs that show this paragraph;
	// export writes the same translation into them.
	mirrors []pptxMirror
	// unmirrored counts drawing paragraphs that show this paragraph but
	// could not be matched to it, and keep their source text.
	unmirrored int
}

type pptxMirror struct {
	part string
	para *pptxParagraph
}

// sentences returns the unit's plain and rich text per segment.
func (u *pptxUnit) sentences() []pptxSentence {
	if u.para != nil {
		return u.para.sentences
	}
	return u.alt.sentences
}

// SentenceKey is the segment key of sentence i: the unit key, plus #N when
// the unit has more than one sentence.
func (u *pptxUnit) sentenceKey(i int) string {
	if len(u.sentences()) == 1 {
		return u.key
	}
	return fmt.Sprintf("%s#%d", u.key, i+1)
}

// pptxCollector walks a deck in the order a reader meets the text: slides as
// PowerPoint shows them; on each slide the title, then the other shapes in
// tree order, a shape's alt text right after its text; then the slide's
// notes.
type pptxCollector struct {
	pkg     *pptxPackage
	units   []*pptxUnit
	visited map[string]bool
	slide   int
	// titles are the current slide's title shapes, already collected.
	titles map[*common.XMLNode]bool
}

// collectPPTX returns every translatable unit in reading order. Parse and
// Reconstruct both go through here, which keeps their keys in lockstep.
func collectPPTX(pkg *pptxPackage) ([]*pptxUnit, error) {
	c := &pptxCollector{pkg: pkg, visited: map[string]bool{}}
	slides, err := pkg.slideParts()
	if err != nil {
		return nil, err
	}
	for i, part := range slides {
		if c.visited[part] {
			continue
		}
		c.visited[part] = true
		c.slide = i + 1
		if err := c.slideOrNotes(part, false); err != nil {
			return nil, err
		}
		notes, err := pkg.relatedByType(part, pptxRelNotesSlide)
		if err != nil {
			return nil, err
		}
		for _, n := range notes {
			if c.visited[n] {
				continue
			}
			c.visited[n] = true
			if err := c.slideOrNotes(n, true); err != nil {
				return nil, err
			}
		}
	}
	return c.units, nil
}

func (c *pptxCollector) context(kind string) string {
	if kind == "" {
		return fmt.Sprintf("slide:%d", c.slide)
	}
	return fmt.Sprintf("slide:%d/%s", c.slide, kind)
}

// slideOrNotes walks the shape tree of a slide or notes part. A notes page
// translates only its body placeholder: the slide image, number, header,
// footer and date placeholders hold no notes text.
func (c *pptxCollector) slideOrNotes(part string, notes bool) error {
	doc, err := c.pkg.doc(part)
	if err != nil {
		return err
	}
	tree := xmlPath(doc.Root, "cSld", "spTree")
	if tree == nil {
		return nil
	}
	if !notes {
		// The title comes first wherever the tree keeps it; the rest follow
		// in tree order.
		c.titles = map[*common.XMLNode]bool{}
		for _, sp := range tree.ChildrenNamed("sp") {
			if ph := xmlPath(sp, "nvSpPr", "nvPr", "ph"); ph != nil && (ph.Attr("type") == "title" || ph.Attr("type") == "ctrTitle") {
				c.titles[sp] = true
				c.shape(part, doc, sp)
			}
		}
		return c.walkTree(part, doc, tree, 0)
	}
	for _, sp := range tree.Children {
		if sp.Name != "sp" {
			continue
		}
		if ph := xmlPath(sp, "nvSpPr", "nvPr", "ph"); ph != nil && ph.Attr("type") == "body" {
			c.textBody(part, doc, xmlChild(sp, "txBody"), c.context("notes"))
		}
	}
	return nil
}

// walkTree walks the children of a shape tree or group.
func (c *pptxCollector) walkTree(part string, doc *common.XMLDoc, tree *common.XMLNode, depth int) error {
	if depth > maxPPTXShapeDepth {
		return fmt.Errorf("%w: shapes nested more than %d levels in %s", common.ErrPPTXMalformed, maxPPTXShapeDepth, part)
	}
	for _, n := range tree.Children {
		switch n.Name {
		case "sp":
			if !c.titles[n] {
				c.shape(part, doc, n)
			}
		case "grpSp":
			if err := c.walkTree(part, doc, n, depth+1); err != nil {
				return err
			}
			c.altText(part, doc, xmlChild(n, "nvGrpSpPr"))
		case "graphicFrame":
			if err := c.graphicFrame(part, doc, n); err != nil {
				return err
			}
			c.altText(part, doc, xmlChild(n, "nvGraphicFramePr"))
		case "pic":
			c.altText(part, doc, xmlChild(n, "nvPicPr"))
		case "cxnSp":
			c.altText(part, doc, xmlChild(n, "nvCxnSpPr"))
		case "AlternateContent":
			// The first choice is what current PowerPoint shows; the fallback
			// is for readers that do not know the feature.
			if choice := xmlChild(n, "Choice"); choice != nil {
				if err := c.walkTree(part, doc, choice, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// shape adds a shape's text, then its alt text.
func (c *pptxCollector) shape(part string, doc *common.XMLDoc, sp *common.XMLNode) {
	c.textBody(part, doc, xmlChild(sp, "txBody"), c.context(""))
	c.altText(part, doc, xmlChild(sp, "nvSpPr"))
}

// graphicFrame collects a table's cells or a SmartArt diagram's text. Charts
// and embedded objects are not translated.
func (c *pptxCollector) graphicFrame(part string, doc *common.XMLDoc, frame *common.XMLNode) error {
	data := xmlPath(frame, "graphic", "graphicData")
	if data == nil {
		return nil
	}
	uri := data.Attr("uri")
	switch {
	case strings.HasSuffix(uri, pptxURITable):
		for _, tr := range xmlChild(data, "tbl").ChildrenNamed("tr") {
			for _, tc := range tr.ChildrenNamed("tc") {
				c.textBody(part, doc, xmlChild(tc, "txBody"), c.context("table"))
			}
		}
	case strings.HasSuffix(uri, pptxURIDiagram):
		ids := xmlChild(data, "relIds")
		if ids == nil {
			return nil
		}
		dataPart, err := c.pkg.related(part, relAttr(ids, "dm"), pptxRelDiagramData)
		if err != nil {
			return err
		}
		return c.smartArt(part, dataPart)
	}
	return nil
}

// textBody adds a unit per paragraph with text.
func (c *pptxCollector) textBody(part string, doc *common.XMLDoc, body *common.XMLNode, context string) []*pptxUnit {
	var added []*pptxUnit
	for _, p := range body.ChildrenNamed("p") {
		para := newPPTXParagraph(doc.Src, p)
		if para == nil {
			continue
		}
		u := &pptxUnit{part: part, key: part + common.XMLNodePath(p), context: context, para: para}
		c.units = append(c.units, u)
		added = append(added, u)
	}
	return added
}
