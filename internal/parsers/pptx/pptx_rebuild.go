package pptx

import (
	"strings"

	"openloc/internal/langcode"
	"openloc/internal/parsers/common"
)

// pptxOut is one item of a rebuilt paragraph: a text run, or an element
// copied as it is (a source line break, a field, an opaque piece).
type pptxOut struct {
	raw  string
	text string
	rPr  *common.XMLNode
	// translated marks text written from a translation, which gets the
	// target language.
	translated bool
}

// rebuild returns the paragraph's markup with each sentence that has a
// translation (has[i]) replaced by trans[i]. Text around and between
// sentences, and sentences without a translation, keep their source runs.
func (p *pptxParagraph) rebuild(trans []string, has []bool, langs common.ExportLangs) string {
	var outs []pptxOut
	emitted := make([]bool, len(p.sentences))

	sourceText := func(piece, from, to int) {
		if from < to {
			outs = append(outs, pptxOut{text: p.plain[from:to], rPr: p.pieces[piece].rPr})
		}
	}
	sourceElement := func(piece int) {
		outs = append(outs, pptxOut{raw: p.pieceMarkup(piece)})
	}

	for _, s := range p.spans {
		piece := p.pieces[s.piece]
		if s.start == s.end {
			if k := p.sentenceAt(s); k >= 0 && has[k] {
				continue
			}
			sourceElement(s.piece)
			continue
		}
		cursor := s.start
		for k, sent := range p.sentences {
			a, b := max(s.start, sent.rng.Start), min(s.end, sent.rng.End)
			if a >= b {
				continue
			}
			if piece.kind == pptxText {
				sourceText(s.piece, cursor, a)
			}
			switch {
			case has[k]:
				if !emitted[k] {
					outs = append(outs, p.translationOuts(trans[k], sent.chips)...)
					emitted[k] = true
				}
			case piece.kind == pptxText:
				sourceText(s.piece, a, b)
			default:
				sourceElement(s.piece)
			}
			cursor = b
		}
		switch {
		case piece.kind == pptxText:
			sourceText(s.piece, cursor, s.end)
		case cursor == s.start:
			sourceElement(s.piece)
		}
	}

	var b strings.Builder
	b.WriteString(p.node.StartTag(p.src))
	if p.pPr != nil {
		b.WriteString(p.src[p.pPr.OuterStart:p.pPr.OuterEnd])
	}
	p.writeOuts(&b, outs, langs)
	if p.endRPr != nil {
		last := len(p.sentences) - 1
		b.WriteString(p.runProps(p.endRPr, has[last], langs))
	}
	b.WriteString(p.node.EndTag(p.src))
	return b.String()
}

// sentenceAt returns the sentence a zero-width span sits inside, or -1.
func (p *pptxParagraph) sentenceAt(s pptxSpan) int {
	for k, sent := range p.sentences {
		if inSentence(s, sent.rng) {
			return k
		}
	}
	return -1
}

// translationOuts turns a translation into runs. Text outside chips gets the
// base run properties; text in <g id="N"> gets the properties of the run
// chip N came from. <x id="N"/> writes chip N's element. A chip with
// ctype="lb" that is not a source line break, and "\n" in the text, become
// a:br. Any other unknown chip is dropped.
func (p *pptxParagraph) translationOuts(tr string, chips []int) []pptxOut {
	var outs []pptxOut
	stack := []*common.XMLNode{p.pieces[p.basePiece].rPr}
	current := func() *common.XMLNode { return stack[len(stack)-1] }

	lineBreak := func() {
		outs = append(outs, pptxOut{raw: p.lineBreak(current()), translated: true})
	}
	text := func(s string) {
		for i, line := range strings.Split(s, "\n") {
			if i > 0 {
				lineBreak()
			}
			if line != "" {
				outs = append(outs, pptxOut{text: line, rPr: current(), translated: true})
			}
		}
	}
	chipPiece := func(id int) (pptxPiece, bool) {
		if id < 1 || id > len(chips) {
			return pptxPiece{}, false
		}
		return p.pieces[chips[id-1]], true
	}

	for i := 0; i < len(tr); {
		if tr[i] != '<' {
			j := strings.IndexByte(tr[i:], '<')
			if j < 0 {
				j = len(tr) - i
			}
			text(tr[i : i+j])
			i += j
			continue
		}
		tag, next, ok := common.ScanChipTag(tr, i)
		if !ok {
			text("<")
			i++
			continue
		}
		switch {
		case tag.Closing:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case tag.Name == "x":
			// ctype="lb" is a line break whatever its id: the editor numbers
			// a new break after the translation's own chips, which can reuse
			// the id of a source chip the translation dropped.
			piece, ok := chipPiece(tag.Id)
			switch {
			case tag.Ctype == "lb" && !(ok && piece.kind == pptxBreak):
				lineBreak()
			case ok && piece.kind != pptxText:
				outs = append(outs, pptxOut{raw: p.pieceMarkup(chips[tag.Id-1])})
			}
		case tag.SelfClosing:
		default:
			if piece, ok := chipPiece(tag.Id); ok && piece.kind == pptxText {
				stack = append(stack, piece.rPr)
			} else {
				stack = append(stack, current())
			}
		}
		i = next
	}
	return outs
}

// pieceMarkup returns the markup of a break, field or opaque piece: its
// source element, or an a:br for a newline that sat inside a run's text.
func (p *pptxParagraph) pieceMarkup(i int) string {
	piece := p.pieces[i]
	if piece.inRun {
		return p.lineBreak(piece.rPr)
	}
	return p.src[piece.node.OuterStart:piece.node.OuterEnd]
}

// lineBreak renders an a:br carrying rPr's look.
func (p *pptxParagraph) lineBreak(rPr *common.XMLNode) string {
	if rPr == nil {
		return "<" + p.prefix + "br/>"
	}
	return "<" + p.prefix + "br>" + p.runProps(rPr, true, common.ExportLangs{}) + "</" + p.prefix + "br>"
}

// writeOuts writes the runs, joining neighbouring text that looks the same.
func (p *pptxParagraph) writeOuts(b *strings.Builder, outs []pptxOut, langs common.ExportLangs) {
	for i := 0; i < len(outs); i++ {
		o := outs[i]
		if o.raw != "" {
			b.WriteString(o.raw)
			continue
		}
		text := o.text
		for i+1 < len(outs) && outs[i+1].raw == "" && outs[i+1].rPr == o.rPr && outs[i+1].translated == o.translated {
			i++
			text += outs[i].text
		}
		text = pptxEscapeText(text)
		if text == "" {
			continue
		}
		b.WriteString("<" + p.prefix + "r>")
		b.WriteString(p.runProps(o.rPr, o.translated, langs))
		b.WriteString("<" + p.prefix + "t>" + text + "</" + p.prefix + "t>")
		b.WriteString("</" + p.prefix + "r>")
	}
}

// runProps renders a run properties element for rebuilt text. Spell-check
// state (err, dirty) is dropped so PowerPoint checks the new text again. For
// translated text, a lang in the source language becomes the target
// language; a missing lang or another language stays as it is.
func (p *pptxParagraph) runProps(rPr *common.XMLNode, translated bool, langs common.ExportLangs) string {
	if rPr == nil {
		return ""
	}
	tag := common.RemoveRawAttrs(rPr.StartTag(p.src), "err", "dirty")
	if translated && langs.Source != "" && langs.Target != "" {
		if v, ok := common.RawAttrValue(tag, "lang"); ok &&
			langcode.SameBase(langcode.Normalize(common.XMLUnescapeAttr(v)), langcode.Normalize(langs.Source)) {
			tag = common.SetRawAttrValue(tag, "lang", common.XMLEscapeAttrValue(langcode.BCP47(langs.Target)))
		}
	}
	return tag + p.src[rPr.InnerStart:rPr.OuterEnd]
}

// pptxEscapeText escapes text for an a:t element and drops characters XML
// cannot hold.
func pptxEscapeText(s string) string {
	clean := strings.Map(func(r rune) rune {
		if r == '\t' || (r >= 0x20 && r != 0xFFFE && r != 0xFFFF) {
			return r
		}
		return -1
	}, s)
	return common.XMLEscapeText(clean)
}

// pptxPlainOf returns a translation without chips, for places that hold
// plain text only, such as alt text.
func pptxPlainOf(tr string) string {
	var b strings.Builder
	for i := 0; i < len(tr); {
		if tr[i] == '<' {
			if _, next, ok := common.ScanChipTag(tr, i); ok {
				i = next
				continue
			}
		}
		b.WriteByte(tr[i])
		i++
	}
	return b.String()
}
