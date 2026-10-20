package parser

import "strings"

// pptxAlt is the alt text of a shape or picture: the descr attribute of its
// cNvPr. It is plain text, with no chips.
type pptxAlt struct {
	value string
	// valueRange is where the raw attribute value sits in the part.
	valueRange TextRange
	sentences  []pptxSentence
}

// altText adds a unit for the alt text of a shape's non-visual properties.
func (c *pptxCollector) altText(part string, doc *xmlDoc, nv *xmlNode) {
	cNvPr := xmlChild(nv, "cNvPr")
	if cNvPr == nil {
		return
	}
	tag := cNvPr.startTag(doc.src)
	r, ok := attrValueRange(tag, "descr")
	if !ok {
		return
	}
	alt := &pptxAlt{
		value:      cNvPr.attr("descr"),
		valueRange: TextRange{Start: cNvPr.outerStart + r.Start, End: cNvPr.outerStart + r.End},
	}
	if !hasWords(alt.value) {
		return
	}
	for _, rng := range SplitSentences(alt.value, nil) {
		alt.sentences = append(alt.sentences, pptxSentence{rng: rng, text: alt.value[rng.Start:rng.End]})
	}
	if len(alt.sentences) == 0 {
		return
	}
	c.units = append(c.units, &pptxUnit{
		part:    part,
		key:     part + xmlNodePath(cNvPr) + "@descr",
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
	return xmlEscapeAttrValue(b.String())
}
