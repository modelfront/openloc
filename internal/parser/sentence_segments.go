package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"openloc/internal/segmentation"
)

// sentenceSegmenter splits each block an inner parser extracts (a paragraph, a
// line, a JSON value) into one segment per sentence, keyed `<block key>#1`,
// `#2`, …; a one-sentence block keeps its key. A block stays whole when a split
// could corrupt it (see sentencePlans). Reconstruct joins the sentences back
// with the source's spacing; a sentence with no translation keeps its source,
// and a translation stored under the whole block's key is written as it is.
type sentenceSegmenter struct {
	inner Parser
	// richExport is true for formats whose export passes a sentence's rich
	// text (chips) when it has one, rather than plain text; see
	// exportTranslations in internal/pipeline.
	richExport bool
	// seg splits each block; nil is the conservative built-in set.
	seg *segmentation.Segmenter
}

func (s sentenceSegmenter) Parse(content []byte) ([]Segment, error) {
	blocks, err := s.inner.Parse(content)
	if err != nil {
		return nil, err
	}
	plans, err := sentencePlans(segmenterOr(s.seg), blocks)
	if err != nil {
		return nil, err
	}
	out := make([]Segment, 0, len(blocks))
	for _, b := range blocks {
		plan, ok := plans[b.Key]
		if !ok {
			b.Position = len(out)
			out = append(out, b)
			continue
		}
		for i, part := range plan.parts {
			seg := b
			seg.Key = sentenceKey(b.Key, i)
			seg.Text = part.text
			seg.RichText = part.rich
			seg.Position = len(out)
			out = append(out, seg)
		}
	}
	return out, nil
}

func (s sentenceSegmenter) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	// Nothing to join when the inner parser cannot write a file (PDF) or no
	// translation is keyed by sentence; skip the parse, which for DOCX and
	// PDF is a round trip to the sidecar.
	if _, ok := s.inner.(interface{ exportOnlyError() }); ok || !hasSentenceKey(translations) {
		return s.inner.Reconstruct(original, translations)
	}
	blocks, err := s.inner.Parse(original)
	if err != nil {
		return nil, err
	}
	plans, err := sentencePlans(segmenterOr(s.seg), blocks)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]string, len(translations))
	for k, v := range translations {
		merged[k] = v
	}
	joined := map[string][]sentencePiece{}
	ranges := numberedRanges(translations)
	for _, b := range blocks {
		plan, ok := plans[b.Key]
		if !ok {
			continue
		}
		if _, whole := translations[b.Key]; whole {
			continue
		}
		rich := s.richExport && b.RichText != ""
		gaps := plan.plainGaps
		if rich {
			gaps = plan.richGaps
		}
		pieces := []sentencePiece{{text: gaps[0]}}
		translated := false
		// A joined run of sentences is written over them and the gaps
		// between them; its chips are numbered as plan.run numbers them.
		runs := runsByStart(ranges[b.Key], len(plan.parts))
		for i := 0; i < len(plan.parts); i++ {
			if r, ok := runs[i]; ok {
				t := translations[r.key]
				delete(merged, r.key)
				if rich {
					_, runRich := plan.joinParts(r.i, r.j)
					_, base := renumberChipIDs(runRich)
					t = shiftChipIDs(t, base)
				}
				translated = true
				pieces = append(pieces, sentencePiece{text: t, translated: true}, sentencePiece{text: gaps[r.j+1]})
				i = r.j
				continue
			}
			part := plan.parts[i]
			key := sentenceKey(b.Key, i)
			piece := sentencePiece{}
			if t, ok := translations[key]; ok {
				translated = true
				delete(merged, key)
				if rich {
					t = shiftChipIDs(t, part.idBase)
				}
				piece = sentencePiece{text: t, translated: true}
			} else if rich && part.sourceRich != "" {
				piece.text = part.sourceRich
			} else {
				piece.text = part.text
			}
			pieces = append(pieces, piece, sentencePiece{text: gaps[i+1]})
		}
		if translated {
			joined[b.Key] = pieces
		}
	}
	if j, ok := s.inner.(pieceJoiner); ok {
		return j.reconstructJoined(original, merged, joined)
	}
	for key, pieces := range joined {
		merged[key] = joinPieces(pieces)
	}
	return s.inner.Reconstruct(original, merged)
}

// sentencePiece is one part of a split block on export: a sentence, as
// translated or as in the source, or the text between two sentences.
type sentencePiece struct {
	text       string
	translated bool
}

func joinPieces(pieces []sentencePiece) string {
	var b strings.Builder
	for _, p := range pieces {
		b.WriteString(p.text)
	}
	return b.String()
}

// pieceJoiner is an inner parser that writes a split block from its pieces
// itself, because how a piece is written depends on whether it is a
// translation (HTML: only a translation can be editor HTML).
type pieceJoiner interface {
	reconstructJoined(original []byte, translations map[string]string, joined map[string][]sentencePiece) ([]byte, error)
}

func hasSentenceKey(translations map[string]string) bool {
	for k := range translations {
		if strings.Contains(k, "#") {
			return true
		}
	}
	return false
}

func sentenceKey(blockKey string, i int) string {
	return fmt.Sprintf("%s#%d", blockKey, i+1)
}

// sentencePlan is how one block splits: its sentences, and the text around
// and between them (gaps[0] before the first sentence, gaps[i+1] after
// sentence i).
type sentencePlan struct {
	parts     []sentencePart
	plainGaps []string
	richGaps  []string
}

type sentencePart struct {
	text string
	// rich is the sentence's rich text with chip ids renumbered from 1, or ""
	// when it holds no chip; sourceRich is the same slice with the block's ids.
	rich, sourceRich string
	// idBase is what was subtracted from the block's chip ids.
	idBase int
}

// sentencePlans returns the plan of every block that splits into two or more
// sentences, by block key. Parse and Reconstruct both call it on a parse of
// the same file, so they agree on every key.
//
// A block stays whole when it already carries a target, its plain text does
// not line up with its chips, a sentence or the space between two would hold
// half of an inline element, or a sentence key would clash with a key the
// file already has. No sentence ends inside "{…}".
func sentencePlans(seg *segmentation.Segmenter, blocks []Segment) (map[string]sentencePlan, error) {
	keys := make(map[string]bool, len(blocks))
	for _, b := range blocks {
		keys[b.Key] = true
	}
	plans := map[string]sentencePlan{}
	for _, b := range blocks {
		if b.Key == "" || b.Target != "" || b.TargetRichText != "" {
			continue
		}
		plan, ok, err := planSentences(seg.Split, b.Text, b.RichText)
		if err != nil {
			return nil, err
		}
		if !ok || len(plan.parts) < 2 {
			continue
		}
		clash := false
		for i := range plan.parts {
			if keys[sentenceKey(b.Key, i)] {
				clash = true
				break
			}
		}
		if !clash {
			plans[b.Key] = plan
		}
	}
	return plans, nil
}

// richToken matches the chips a rich text holds: a <g …> opener, its </g>
// closer, and a self-closing <x …/>. Attribute values are XML-escaped, so a
// tag never contains a raw '>'.
var richToken = regexp.MustCompile(`<g(?:\s[^>]*)?>|</g>|<x(?:\s[^>]*)?/>`)

var lineBreakChip = regexp.MustCompile(`\sctype="lb"`)

// braceFree reports, for a break at byte p of text, whether no "{" is open
// there: an ICU plural or select, or a placeholder, is never cut in half.
func braceFree(text string) func(p int) bool {
	open := make([]int, len(text)+1)
	n := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '{':
			n++
		case '}':
			if n > 0 {
				n--
			}
		}
		open[i+1] = n
	}
	return func(p int) bool { return open[p] == 0 }
}

// splitFunc cuts text into ranges, breaking only where canBreak allows:
// Segmenter.Split, or a cut at one position (SplitSourceAt).
type splitFunc func(text string, canBreak func(pos int) bool) ([]segmentation.TextRange, error)

// planSentences splits text (and rich, its chip form, when set) into
// sentences with split. ok is false when rich does not line up with text, or a
// split would leave a chip open across sentences.
func planSentences(split splitFunc, text, rich string) (sentencePlan, bool, error) {
	outsideBraces := braceFree(text)
	if rich == "" {
		ranges, err := split(text, outsideBraces)
		if err != nil {
			return sentencePlan{}, false, err
		}
		plan := sentencePlan{}
		prev := 0
		for _, r := range ranges {
			plan.plainGaps = append(plan.plainGaps, text[prev:r.Start])
			plan.parts = append(plan.parts, sentencePart{text: text[r.Start:r.End]})
			prev = r.End
		}
		plan.plainGaps = append(plan.plainGaps, text[prev:])
		return plan, true, nil
	}

	// The plain text of rich, byte by byte, with where each byte sits in rich
	// and how many <g> are open there. A line-break chip reads as "\n", as the
	// parsers write it in the plain text.
	var view strings.Builder
	var start, end, depth []int
	open := 0
	add := func(b byte, s, e int) {
		view.WriteByte(b)
		start = append(start, s)
		end = append(end, e)
		depth = append(depth, open)
	}
	pos := 0
	for _, m := range richToken.FindAllStringIndex(rich, -1) {
		for i := pos; i < m[0]; i++ {
			add(rich[i], i, i+1)
		}
		tag := rich[m[0]:m[1]]
		switch {
		case strings.HasPrefix(tag, "</g"):
			open--
			if open < 0 {
				return sentencePlan{}, false, nil
			}
		case strings.HasPrefix(tag, "<g"):
			open++
		case lineBreakChip.MatchString(tag):
			add('\n', m[0], m[1])
		}
		pos = m[1]
	}
	for i := pos; i < len(rich); i++ {
		add(rich[i], i, i+1)
	}
	if open != 0 || view.String() != text {
		return sentencePlan{}, false, nil
	}

	// lo is where rich resumes after the byte before p; hi is where the byte
	// at p starts. Chips between them belong to whichever side they open or
	// close, so a sentence takes its own wrappers.
	lo := func(p int) int {
		if p == 0 {
			return 0
		}
		return end[p-1]
	}
	hi := func(p int) int {
		if p == len(text) {
			return len(rich)
		}
		return start[p]
	}
	depthAt := func(p int) int {
		if p == len(text) {
			return 0
		}
		return depth[p]
	}
	// touch is where rich splits between two sentences that meet with no
	// text between them (a full-width terminator breaks without a space):
	// closing tags stay with the sentence before, the rest go to the one after.
	touch := func(p int) int {
		at := lo(p)
		for {
			m := richToken.FindStringIndex(rich[at:hi(p)])
			if m == nil || m[0] != 0 || !strings.HasPrefix(rich[at:], "</g") {
				return at
			}
			at += m[1]
		}
	}

	// A break after whitespace needs no tag open at the next sentence's first
	// character. A break with no gap (the next sentence starts right at it)
	// needs none open at the touch point, after the closing tags.
	canBreak := func(p int) bool {
		if !outsideBraces(p) {
			return false
		}
		if depthAt(p) == 0 {
			return true
		}
		if p == 0 || p == len(text) {
			return false
		}
		if r, _ := utf8.DecodeRuneInString(text[p:]); unicode.IsSpace(r) {
			return false
		}
		closed := len(richToken.FindAllString(rich[lo(p):touch(p)], -1))
		return depth[p-1]-closed == 0
	}
	ranges, err := split(text, canBreak)
	if err != nil {
		return sentencePlan{}, false, err
	}

	plan := sentencePlan{}
	prevPlain, prevRich := 0, 0
	for i, r := range ranges {
		rs, re := lo(r.Start), hi(r.End)
		if i > 0 && r.Start == ranges[i-1].End {
			rs = touch(r.Start)
		}
		if i+1 < len(ranges) && ranges[i+1].Start == r.End {
			re = touch(r.End)
		}
		gap := rich[prevRich:rs]
		if strings.Contains(gap, "<g") || strings.Contains(gap, "</g") {
			return sentencePlan{}, false, nil
		}
		plan.plainGaps = append(plan.plainGaps, text[prevPlain:r.Start])
		plan.richGaps = append(plan.richGaps, gap)
		part := sentencePart{text: text[r.Start:r.End]}
		if src := rich[rs:re]; richToken.MatchString(src) {
			if !balancedChips(src) {
				return sentencePlan{}, false, nil
			}
			part.sourceRich = src
			part.rich, part.idBase = renumberChipIDs(src)
		}
		plan.parts = append(plan.parts, part)
		prevPlain, prevRich = r.End, re
	}
	tail := rich[prevRich:]
	if strings.Contains(tail, "<g") || strings.Contains(tail, "</g") {
		return sentencePlan{}, false, nil
	}
	plan.plainGaps = append(plan.plainGaps, text[prevPlain:])
	plan.richGaps = append(plan.richGaps, tail)
	return plan, true, nil
}

func balancedChips(s string) bool {
	open := 0
	for _, tag := range richToken.FindAllString(s, -1) {
		switch {
		case strings.HasPrefix(tag, "</g"):
			open--
			if open < 0 {
				return false
			}
		case strings.HasPrefix(tag, "<g"):
			open++
		}
	}
	return open == 0
}

var chipID = regexp.MustCompile(`\sid="(\d+)"`)

// renumberChipIDs makes a sentence's chip ids start at 1, as in a segment of
// its own, and returns what it subtracted. Ids that are not all numbers are
// left as they are (base 0).
func renumberChipIDs(rich string) (string, int) {
	minID := -1
	for _, tag := range richToken.FindAllString(rich, -1) {
		if strings.HasPrefix(tag, "</g") {
			continue
		}
		m := chipID.FindStringSubmatch(tag)
		if m == nil {
			return rich, 0
		}
		n, _ := strconv.Atoi(m[1])
		if minID < 0 || n < minID {
			minID = n
		}
	}
	if minID <= 1 {
		return rich, 0
	}
	base := minID - 1
	return shiftChipIDs(rich, -base), base
}

// shiftChipIDs adds delta to every numeric chip id in rich. Text outside the
// chips is left alone, so a literal ` id="3"` in a sentence is not touched.
func shiftChipIDs(rich string, delta int) string {
	if delta == 0 {
		return rich
	}
	return richToken.ReplaceAllStringFunc(rich, func(tag string) string {
		return chipID.ReplaceAllStringFunc(tag, func(attr string) string {
			n, err := strconv.Atoi(chipID.FindStringSubmatch(attr)[1])
			if err != nil {
				return attr
			}
			return strings.Replace(attr, strconv.Itoa(n), strconv.Itoa(n+delta), 1)
		})
	})
}
