package pptx

import (
	"fmt"
	"openloc/internal/parsers/common"
	"strings"
)

// SmartArt keeps its text twice. The data part (ppt/diagrams/data*.xml)
// holds the diagram's points, and that is where segments come from. The
// drawing part (drawing*.xml) caches the shapes as last laid out, and
// viewers draw from it, so export writes each translation into both.
//
// A drawing shape names the point it shows by modelId. Usually that is a
// presentation point whose prSet@presAssocID names the data point; sometimes
// it is the data point itself. A drawing paragraph is matched to a data
// paragraph with the same text among that point's paragraphs, else among all
// of the diagram's. A paragraph that cannot be matched, or whose sentences
// and chips do not line up with its match, keeps its source text and is
// counted, so the export can report it.

// smartArt adds the units of a SmartArt diagram and links its drawing.
func (c *pptxCollector) smartArt(slidePart, dataPart string) error {
	if c.visited[dataPart] {
		return nil
	}
	c.visited[dataPart] = true
	doc, err := c.pkg.doc(dataPart)
	if err != nil {
		return err
	}

	presAssoc := map[string]string{}
	byModel := map[string][]*pptxUnit{}
	var units []*pptxUnit
	for _, pt := range xmlChild(doc.Root, "ptLst").ChildrenNamed("pt") {
		id := pt.Attr("modelId")
		switch pt.Attr("type") {
		case "pres":
			if set := xmlChild(pt, "prSet"); set != nil && set.Attr("presAssocID") != "" {
				presAssoc[id] = set.Attr("presAssocID")
			}
			continue
		case "parTrans", "sibTrans":
			continue
		}
		added := c.textBody(dataPart, doc, xmlChild(pt, "t"), c.context("smartart"))
		byModel[id] = append(byModel[id], added...)
		units = append(units, added...)
	}
	if len(units) == 0 {
		return nil
	}

	drawingPart, err := c.smartArtDrawing(slidePart, doc)
	if err != nil || drawingPart == "" || c.visited[drawingPart] {
		return err
	}
	c.visited[drawingPart] = true
	ddoc, err := c.pkg.doc(drawingPart)
	if err != nil {
		return err
	}
	m := &smartArtMirror{part: drawingPart, src: ddoc.Src, units: units, byModel: byModel, presAssoc: presAssoc, used: map[*pptxUnit]bool{}}
	return m.walk(xmlChild(ddoc.Root, "spTree"), 0)
}

// smartArtDrawing returns the drawing part of a diagram, or "" when it has
// none. The data part names it by a relationship id of the slide.
func (c *pptxCollector) smartArtDrawing(slidePart string, data *common.XMLDoc) (string, error) {
	var relID string
	for _, ext := range xmlChild(data.Root, "extLst").ChildrenNamed("ext") {
		if dm := xmlChild(ext, "dataModelExt"); dm != nil {
			relID = dm.Attr("relId")
			break
		}
	}
	if relID == "" {
		return "", nil
	}
	rels, err := c.pkg.relsOf(slidePart)
	if err != nil {
		return "", err
	}
	for _, r := range rels {
		if r.id != relID || !strings.HasSuffix(r.typ, pptxRelDiagramDrawing) {
			continue
		}
		if f, ok := c.pkg.entry(r.target); ok {
			return f.Name, nil
		}
	}
	return "", nil
}

// smartArtMirror links the paragraphs of a drawing part to the data units
// they show.
type smartArtMirror struct {
	part      string
	src       string
	units     []*pptxUnit
	byModel   map[string][]*pptxUnit
	presAssoc map[string]string
	used      map[*pptxUnit]bool
}

func (m *smartArtMirror) walk(tree *common.XMLNode, depth int) error {
	if depth > maxPPTXShapeDepth {
		return fmt.Errorf("%w: SmartArt shapes nested more than %d levels in %s", common.ErrPPTXMalformed, maxPPTXShapeDepth, m.part)
	}
	for _, n := range tree.ChildrenOrNone() {
		switch n.Name {
		case "grpSp":
			if err := m.walk(n, depth+1); err != nil {
				return err
			}
		case "sp":
			model := n.Attr("modelId")
			if a, ok := m.presAssoc[model]; ok {
				model = a
			}
			for _, p := range xmlChild(n, "txBody").ChildrenNamed("p") {
				para := newPPTXParagraph(m.src, p)
				if para == nil {
					continue
				}
				m.link(para, model)
			}
		}
	}
	return nil
}

// link attaches a drawing paragraph to the data unit it shows.
func (m *smartArtMirror) link(para *pptxParagraph, model string) {
	u := m.match(para, m.byModel[model])
	if u == nil {
		u = m.match(para, m.units)
	}
	if u == nil {
		// Text the data part does not hold. It can only go stale when the
		// diagram is translated, so it is counted with the diagram's first
		// unit.
		m.units[0].unmirrored++
		return
	}
	m.used[u] = true
	if !mirrorFits(u.para, para) {
		u.unmirrored++
		return
	}
	u.mirrors = append(u.mirrors, pptxMirror{part: m.part, para: para})
}

// match returns the candidate with the same text, preferring one no other
// drawing paragraph took yet.
func (m *smartArtMirror) match(para *pptxParagraph, candidates []*pptxUnit) *pptxUnit {
	var taken *pptxUnit
	for _, u := range candidates {
		if u.para.plain != para.plain {
			continue
		}
		if !m.used[u] {
			return u
		}
		if taken == nil {
			taken = u
		}
	}
	return taken
}

// mirrorFits reports whether a drawing paragraph can take a data paragraph's
// translations: the same sentences, with chips of the same kinds in the same
// order, so chip ids mean the same thing in both.
func mirrorFits(data, drawing *pptxParagraph) bool {
	if len(data.sentences) != len(drawing.sentences) {
		return false
	}
	for i, s := range data.sentences {
		d := drawing.sentences[i]
		if s.rng != d.rng || len(s.chips) != len(d.chips) {
			return false
		}
		for j := range s.chips {
			if data.pieces[s.chips[j]].kind != drawing.pieces[d.chips[j]].kind {
				return false
			}
		}
	}
	return true
}
