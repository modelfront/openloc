package pptx

import (
	"openloc/internal/parsers/common"
	"strings"
)

// pptxAlt is the alt text of a shape or picture: the descr attribute of its
// cNvPr. It is plain text, with no chips.
type pptxAlt struct {
	value string
	// valueRange is where the raw attribute value sits in the part.
	valueRange common.TextRange
	sentences  []pptxSentence
}

// altText adds a unit for the alt text of a shape's non-visual properties.
func (c *pptxCollector) altText(part string, doc *common.XMLDoc, nv *common.XMLNode) {
	cNvPr := xmlChild(nv, "cNvPr")
	if cNvPr == nil {
		return
	}
	tag := cNvPr.StartTag(doc.Src)
	r, ok := common.AttrValueRange(tag, "descr")
	if !ok {
		return
	}
	alt := &pptxAlt{
		value:      cNvPr.Attr("descr"),
		valueRange: common.TextRange{Start: cNvPr.OuterStart + r.Start, End: cNvPr.OuterStart + r.End},
	}
	if !hasWords(alt.value) {
		return
	}
	for _, rng := range common.SplitSentences(alt.value, nil) {
		alt.sentences = append(alt.sentences, pptxSentence{rng: rng, text: alt.value[rng.Start:rng.End]})
	}
	if len(alt.sentences) == 0 {
		return
	}
	c.units = append(c.units, &pptxUnit{
		part:    part,
		key:     part + common.XMLNodePath(cNvPr) + "@descr",
		context: c.context("alt"),
		alt:     alt,
	})
}

// rebuild returns the escaped attribute value with each sentence that has a
// translation replaced.
func (a *pptxAlt) rebuild(trans []string, has []bool) string {
	var b strings.Builder
	cursor := 0
	for k, s := range a.sentences {
		b.WriteString(a.value[cursor:s.rng.Start])
		if has[k] {
			b.WriteString(pptxPlainOf(trans[k]))
		} else {
			b.WriteString(s.text)
		}
		cursor = s.rng.End
	}
	b.WriteString(a.value[cursor:])
	return common.XMLEscapeAttrValue(b.String())
}
