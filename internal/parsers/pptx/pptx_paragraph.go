package pptx

import (
	"fmt"
	"openloc/internal/parsers/common"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// A DrawingML paragraph (a:p) holds paragraph properties, a list of pieces —
// text runs, line breaks, fields — and end-of-paragraph properties. The
// formatting of a run (a:rPr) sits next to its text, not around it, so the
// generic XML chip model, where every element becomes a chip, does not fit:
// PowerPoint splits text into many runs for reasons a translator cannot see
// (spell-check marks, language tags), and every one would become a tag.
//
// Instead, runs whose properties differ only by such noise are merged, the
// style covering the most characters becomes the paragraph's base, and only
// text in another style becomes a <g> chip. Line breaks and fields become <x>
// placeholders. Export rebuilds the runs: text outside chips gets the base
// properties, and a chip gets back the properties of the run it came from,
// wherever the translator moved it.

// pptxNoiseAttrs are run properties that change nothing a reader sees.
var pptxNoiseAttrs = map[string]bool{
	"lang":     true,
	"altLang":  true,
	"err":      true,
	"dirty":    true,
	"noProof":  true,
	"smtClean": true,
	"smtId":    true,
	"bmk":      true,
}

type pptxPieceKind int

const (
	pptxText   pptxPieceKind = iota // a:r
	pptxBreak                       // a:br
	pptxField                       // a:fld, generated text such as the slide number
	pptxOpaque                      // anything else, such as an equation
)

// pptxPiece is one piece of a paragraph after merging.
type pptxPiece struct {
	kind pptxPieceKind
	// node is the first source element of the piece.
	node *common.XMLNode
	// rPr is the run properties element, nil when the run has none.
	rPr *common.XMLNode
	// style identifies what the run looks like: rPr without noise
	// attributes. Two text pieces with the same style look the same.
	style string
	text  string
	// inRun marks a line break written as a newline inside a run's text; its
	// node is that run, and export writes it as an a:br.
	inRun bool
}

// pptxSpan places a piece in the paragraph's plain text: bytes [start, end).
// Fields and opaque pieces have no text and start == end.
type pptxSpan struct {
	piece      int
	start, end int
}

// pptxParagraph is the translator's view of one a:p.
type pptxParagraph struct {
	node   *common.XMLNode
	src    string
	prefix string // element prefix of the paragraph, such as "a:"
	pPr    *common.XMLNode
	endRPr *common.XMLNode
	pieces []pptxPiece
	spans  []pptxSpan
	plain  string
	// base is the style of the paragraph's base text; basePiece is the
	// first piece in that style, whose run properties untagged text gets.
	base      string
	basePiece int
	sentences []pptxSentence
}

// pptxSentence is one segment of a paragraph.
type pptxSentence struct {
	rng  common.TextRange
	text string
	rich string
	// chips[id-1] is the piece chip id stands for.
	chips []int
}

// newPPTXParagraph builds the model of p. It returns nil when the paragraph
// has no text to translate.
func newPPTXParagraph(src string, p *common.XMLNode) *pptxParagraph {
	para := &pptxParagraph{node: p, src: src, prefix: elementPrefix(p.StartTag(src)), basePiece: -1}
	var raw []pptxPiece
	for _, c := range p.Children {
		switch c.Name {
		case "pPr":
			para.pPr = c
		case "endParaRPr":
			para.endRPr = c
		case "r":
			rPr := xmlChild(c, "rPr")
			style := pptxStyle(src, rPr)
			for i, line := range pptxLineBreaks.Split(pptxRunText(src, xmlChild(c, "t")), -1) {
				if i > 0 {
					raw = append(raw, pptxPiece{kind: pptxBreak, node: c, rPr: rPr, style: style, inRun: true})
				}
				if line != "" {
					raw = append(raw, pptxPiece{kind: pptxText, node: c, rPr: rPr, style: style, text: line})
				}
			}
		case "br":
			rPr := xmlChild(c, "rPr")
			raw = append(raw, pptxPiece{kind: pptxBreak, node: c, rPr: rPr, style: pptxStyle(src, rPr)})
		case "fld":
			raw = append(raw, pptxPiece{kind: pptxField, node: c})
		default:
			raw = append(raw, pptxPiece{kind: pptxOpaque, node: c})
		}
	}
	para.pieces = mergeRuns(raw)
	para.layout()
	if !hasWords(para.plain) {
		return nil
	}
	para.pickBase()
	para.split()
	if len(para.sentences) == 0 {
		return nil
	}
	return para
}

// hasWords reports whether text holds a letter or a digit. Text without
// one — a bullet drawn as its own text box, an arrow, a dash — has nothing to
// translate.
func hasWords(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) >= 0
}

// pptxLineBreaks are the newlines a run's text may hold. PowerPoint writes a
// line break as a:br; other tools write a newline or a vertical tab into the
// text, which PowerPoint shows as a break too.
var pptxLineBreaks = regexp.MustCompile("\r\n|[\n\r\v]")

// mergeRuns joins neighbouring text that looks the same, keeping the run
// properties of the longest run. A run of whitespace looks the same in any
// style, so it takes the style of the text before it (or, at the start,
// after it) instead of becoming a chip of its own.
func mergeRuns(raw []pptxPiece) []pptxPiece {
	for i := range raw {
		if raw[i].kind != pptxText || strings.TrimSpace(raw[i].text) != "" {
			continue
		}
		if j := nearestText(raw, i, -1); j >= 0 {
			raw[i].rPr, raw[i].style = raw[j].rPr, raw[j].style
		} else if j := nearestText(raw, i, 1); j >= 0 {
			raw[i].rPr, raw[i].style = raw[j].rPr, raw[j].style
		}
	}
	var pieces []pptxPiece
	// longest is the length of the run whose properties pieces[n-1] carries.
	longest := 0
	for _, piece := range raw {
		if n := len(pieces); n > 0 && piece.kind == pptxText && pieces[n-1].kind == pptxText && pieces[n-1].style == piece.style {
			pieces[n-1].text += piece.text
			// Merged runs may differ in noise such as lang; the longest run
			// speaks for the text, so a short run in another language
			// does not decide how the translation is marked.
			if len(piece.text) > longest {
				pieces[n-1].rPr, longest = piece.rPr, len(piece.text)
			}
			continue
		}
		pieces = append(pieces, piece)
		longest = len(piece.text)
	}
	return pieces
}

// nearestText returns the index of the closest text piece with visible text
// from i in direction step, or -1.
func nearestText(raw []pptxPiece, i, step int) int {
	for j := i + step; j >= 0 && j < len(raw); j += step {
		if raw[j].kind == pptxText && strings.TrimSpace(raw[j].text) != "" {
			return j
		}
	}
	return -1
}

// layout builds the plain text and places every piece in it. A line break
// reads as "\n"; fields and opaque pieces add no text.
func (p *pptxParagraph) layout() {
	var plain strings.Builder
	for i, piece := range p.pieces {
		start := plain.Len()
		switch piece.kind {
		case pptxText:
			plain.WriteString(piece.text)
		case pptxBreak:
			plain.WriteString("\n")
		}
		p.spans = append(p.spans, pptxSpan{piece: i, start: start, end: plain.Len()})
	}
	p.plain = plain.String()
}

// pickBase chooses the style covering the most characters; the earliest wins
// a tie.
func (p *pptxParagraph) pickBase() {
	count := map[string]int{}
	for _, piece := range p.pieces {
		if piece.kind == pptxText {
			count[piece.style] += len(piece.text)
		}
	}
	best := -1
	for i, piece := range p.pieces {
		if piece.kind != pptxText {
			continue
		}
		if c := count[piece.style]; c > best {
			best = c
			p.base = piece.style
			p.basePiece = i
		}
	}
}

// isChipText reports whether a piece is text that becomes a <g> chip.
func (p *pptxParagraph) isChipText(i int) bool {
	return p.pieces[i].kind == pptxText && p.pieces[i].style != p.base
}

// split cuts the paragraph into sentences. A break may only follow base
// text, so a sentence never ends inside a chip.
func (p *pptxParagraph) split() {
	breakable := make([]bool, len(p.plain))
	for _, s := range p.spans {
		if p.pieces[s.piece].kind == pptxText && !p.isChipText(s.piece) {
			for i := s.start; i < s.end; i++ {
				breakable[i] = true
			}
		}
	}
	for _, rng := range common.SplitSentences(p.plain, common.BreakVeto(breakable)) {
		p.sentences = append(p.sentences, p.sentence(rng))
	}
}

// inSentence reports whether span s belongs to the sentence covering rng. A
// text span belongs when it overlaps the range; a zero-width piece only when
// it sits strictly inside, so pieces at a sentence's edges stay in the
// paragraph around it.
func inSentence(s pptxSpan, rng common.TextRange) bool {
	if s.start == s.end {
		return s.start > rng.Start && s.start < rng.End
	}
	return s.start < rng.End && s.end > rng.Start
}

// sentence renders the plain and rich text of one sentence.
func (p *pptxParagraph) sentence(rng common.TextRange) pptxSentence {
	sent := pptxSentence{rng: rng, text: p.plain[rng.Start:rng.End]}
	var rich strings.Builder
	for _, s := range p.spans {
		if !inSentence(s, rng) {
			continue
		}
		piece := p.pieces[s.piece]
		switch piece.kind {
		case pptxText:
			text := p.plain[max(s.start, rng.Start):min(s.end, rng.End)]
			if !p.isChipText(s.piece) {
				rich.WriteString(text)
				continue
			}
			sent.chips = append(sent.chips, s.piece)
			fmt.Fprintf(&rich, `<g id="%d" ctype="%s">%s</g>`, len(sent.chips), p.chipCtype(piece), text)
		case pptxBreak:
			sent.chips = append(sent.chips, s.piece)
			fmt.Fprintf(&rich, `<x id="%d" ctype="lb"/>`, len(sent.chips))
		case pptxField:
			sent.chips = append(sent.chips, s.piece)
			fmt.Fprintf(&rich, `<x id="%d" ctype="x-xml-fld"/>`, len(sent.chips))
		default:
			sent.chips = append(sent.chips, s.piece)
			fmt.Fprintf(&rich, `<x id="%d" ctype="x-xml-%s"/>`, len(sent.chips), piece.node.Name)
		}
	}
	if len(sent.chips) > 0 {
		sent.rich = rich.String()
	}
	return sent
}

// chipCtype names what sets a chip's text apart from the base text, so the
// editor can label the chip: bold, italic, underlined, a link, or a generic
// span for anything else (colour, size, font).
func (p *pptxParagraph) chipCtype(piece pptxPiece) string {
	base := p.pieces[p.basePiece].rPr
	if xmlChild(piece.rPr, "hlinkClick") != nil && xmlChild(base, "hlinkClick") == nil {
		return "x-html-a"
	}
	if pptxInner(p.src, piece.rPr) != pptxInner(p.src, base) {
		return "x-html-span"
	}
	chip, baseAttrs := pptxStyleAttrs(piece.rPr), pptxStyleAttrs(base)
	var diff []string
	for name, v := range chip {
		if baseAttrs[name] != v {
			diff = append(diff, name)
		}
	}
	for name := range baseAttrs {
		if _, ok := chip[name]; !ok {
			diff = append(diff, name)
		}
	}
	if len(diff) != 1 {
		return "x-html-span"
	}
	on := chip[diff[0]]
	switch {
	case diff[0] == "b" && pptxTrue(on):
		return "bold"
	case diff[0] == "i" && pptxTrue(on):
		return "italic"
	case diff[0] == "u" && on != "" && on != "none":
		return "underlined"
	}
	return "x-html-span"
}

func pptxTrue(v string) bool { return v == "1" || v == "true" }

// pptxStyleAttrs returns the run properties that affect how text looks.
func pptxStyleAttrs(rPr *common.XMLNode) map[string]string {
	attrs := map[string]string{}
	if rPr == nil {
		return attrs
	}
	for _, a := range rPr.Attrs {
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		name := a.Name.Local
		if a.Name.Space != "" {
			name = a.Name.Space + ":" + name
		} else if pptxNoiseAttrs[name] {
			continue
		}
		attrs[name] = a.Value
	}
	return attrs
}

// pptxInner returns the raw content of rPr: fills, fonts, links.
func pptxInner(src string, rPr *common.XMLNode) string {
	if rPr == nil {
		return ""
	}
	return src[rPr.InnerStart:rPr.InnerEnd]
}

// pptxStyle is the comparable form of a run's look: its sorted non-noise
// attributes and its raw content.
func pptxStyle(src string, rPr *common.XMLNode) string {
	attrs := pptxStyleAttrs(rPr)
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(attrs[name])
		b.WriteByte(0)
	}
	b.WriteByte(1)
	b.WriteString(pptxInner(src, rPr))
	return b.String()
}

// pptxRunText decodes the text of an a:t element, CDATA sections included.
func pptxRunText(src string, t *common.XMLNode) string {
	if t == nil {
		return ""
	}
	raw := src[t.InnerStart:t.InnerEnd]
	var b strings.Builder
	for {
		i := strings.Index(raw, "<![CDATA[")
		if i < 0 {
			text, _ := common.XMLUnescape(raw)
			b.WriteString(text)
			return b.String()
		}
		text, _ := common.XMLUnescape(raw[:i])
		b.WriteString(text)
		raw = raw[i+len("<![CDATA["):]
		j := strings.Index(raw, "]]>")
		if j < 0 {
			b.WriteString(raw)
			return b.String()
		}
		b.WriteString(raw[:j])
		raw = raw[j+len("]]>"):]
	}
}

// elementPrefix returns the namespace prefix of a start tag, with its colon:
// "a:" for `<a:p>`, "" for `<p>`.
func elementPrefix(startTag string) string {
	name := common.StartTagElementName(startTag)
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[:i+1]
	}
	return ""
}
