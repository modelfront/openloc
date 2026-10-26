package common

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SentenceSegmenter splits each block an inner parser extracts (a paragraph, a
// line, a JSON value) into one segment per sentence, keyed `<block key>#1`,
// `#2`, …; a one-sentence block keeps its key. A block stays whole when a split
// could corrupt it (see SentencePlans). Reconstruct joins the sentences back
// with the source's spacing; a sentence with no translation keeps its source,
// and a translation stored under the whole block's key is written as it is.
type SentenceSegmenter struct {
	Inner Parser
	// RichExport is true for formats whose export passes a sentence's rich
	// text (chips) when it has one, rather than plain text; see
	// exportTranslations in internal/pipeline.
	RichExport bool
}

func (s SentenceSegmenter) Parse(content []byte) ([]Segment, error) {
	blocks, err := s.Inner.Parse(content)
	if err != nil {
		return nil, err
	}
	plans := SentencePlans(blocks)
	out := make([]Segment, 0, len(blocks))
	for _, b := range blocks {
		plan, ok := plans[b.Key]
		if !ok {
			b.Position = len(out)
			out = append(out, b)
			continue
		}
		for i, part := range plan.Parts {
			seg := b
			seg.Key = SentenceKey(b.Key, i)
			seg.Text = part.Text
			seg.RichText = part.rich
			seg.Position = len(out)
			out = append(out, seg)
		}
	}
	return out, nil
}

func (s SentenceSegmenter) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	// Nothing to join when the inner parser cannot write a file (PDF) or no
	// translation is keyed by sentence; skip the parse, which for DOCX and
	// PDF is a round trip to the sidecar.
	if _, ok := s.Inner.(interface{ ExportOnlyError() }); ok || !hasSentenceKey(translations) {
		return s.Inner.Reconstruct(original, translations)
	}
	blocks, err := s.Inner.Parse(original)
	if err != nil {
		return nil, err
	}
	plans := SentencePlans(blocks)
	merged := make(map[string]string, len(translations))
	for k, v := range translations {
		merged[k] = v
	}
	joined := map[string][]SentencePiece{}
	for _, b := range blocks {
		plan, ok := plans[b.Key]
		if !ok {
			continue
		}
		if _, whole := translations[b.Key]; whole {
			continue
		}
		rich := s.RichExport && b.RichText != ""
		gaps := plan.plainGaps
		if rich {
			gaps = plan.richGaps
		}
		pieces := []SentencePiece{{Text: gaps[0]}}
		translated := false
		for i, part := range plan.Parts {
			key := SentenceKey(b.Key, i)
			piece := SentencePiece{}
			if t, ok := translations[key]; ok {
				translated = true
				delete(merged, key)
				if rich {
					t = shiftChipIDs(t, part.idBase)
				}
				piece = SentencePiece{Text: t, Translated: true}
			} else if rich && part.sourceRich != "" {
				piece.Text = part.sourceRich
			} else {
				piece.Text = part.Text
			}
			pieces = append(pieces, piece, SentencePiece{Text: gaps[i+1]})
		}
		if translated {
			joined[b.Key] = pieces
		}
	}
	if j, ok := s.Inner.(PieceJoiner); ok {
		return j.ReconstructJoined(original, merged, joined)
	}
	for key, pieces := range joined {
		merged[key] = JoinPieces(pieces)
	}
	return s.Inner.Reconstruct(original, merged)
}

// SentencePiece is one part of a split block on export: a sentence, as
// translated or as in the source, or the text between two sentences.
type SentencePiece struct {
	Text       string
	Translated bool
}

func JoinPieces(pieces []SentencePiece) string {
	var b strings.Builder
	for _, p := range pieces {
		b.WriteString(p.Text)
	}
	return b.String()
}

// PieceJoiner is an inner parser that writes a split block from its pieces
// itself, because how a piece is written depends on whether it is a
// translation (HTML: only a translation can be editor HTML).
type PieceJoiner interface {
	ReconstructJoined(original []byte, translations map[string]string, joined map[string][]SentencePiece) ([]byte, error)
}

func hasSentenceKey(translations map[string]string) bool {
	for k := range translations {
		if strings.Contains(k, "#") {
			return true
		}
	}
	return false
}

func SentenceKey(blockKey string, i int) string {
	return fmt.Sprintf("%s#%d", blockKey, i+1)
}

// sentencePlan is how one block splits: its sentences, and the text around
// and between them (gaps[0] before the first sentence, gaps[i+1] after
// sentence i).
type sentencePlan struct {
	Parts     []sentencePart
	plainGaps []string
	richGaps  []string
}

type sentencePart struct {
	Text string
	// rich is the sentence's rich text with chip ids renumbered from 1, or ""
	// when it holds no chip; sourceRich is the same slice with the block's ids.
	rich, sourceRich string
	// idBase is what was subtracted from the block's chip ids.
	idBase int
}

// SentencePlans returns the plan of every block that splits into two or more
// sentences, by block key. Parse and Reconstruct both call it on a parse of
// the same file, so they agree on every key.
//
// A block stays whole when it already carries a target, its plain text does
// not line up with its chips, a sentence or the space between two would hold
// half of an inline element, or a sentence key would clash with a key the
// file already has. No sentence ends inside "{…}".
func SentencePlans(blocks []Segment) map[string]sentencePlan {
	keys := make(map[string]bool, len(blocks))
	for _, b := range blocks {
		keys[b.Key] = true
	}
	plans := map[string]sentencePlan{}
	for _, b := range blocks {
		if b.Key == "" || b.Target != "" || b.TargetRichText != "" {
			continue
		}
		plan, ok := planSentences(b.Text, b.RichText)
		if !ok || len(plan.Parts) < 2 {
			continue
		}
		clash := false
		for i := range plan.Parts {
			if keys[SentenceKey(b.Key, i)] {
				clash = true
				break
			}
		}
		if !clash {
			plans[b.Key] = plan
		}
	}
	return plans
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

// planSentences splits text (and rich, its chip form, when set) into
// sentences. ok is false when rich does not line up with text, or a split
// would leave a chip open across sentences.
func planSentences(text, rich string) (sentencePlan, bool) {
	outsideBraces := braceFree(text)
	if rich == "" {
		ranges := SplitSentences(text, outsideBraces)
		plan := sentencePlan{}
		prev := 0
		for _, r := range ranges {
			plan.plainGaps = append(plan.plainGaps, text[prev:r.Start])
			plan.Parts = append(plan.Parts, sentencePart{Text: text[r.Start:r.End]})
			prev = r.End
		}
		plan.plainGaps = append(plan.plainGaps, text[prev:])
		return plan, true
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
				return sentencePlan{}, false
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
		return sentencePlan{}, false
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
	ranges := SplitSentences(text, func(p int) bool { return depthAt(p) == 0 && outsideBraces(p) })

	plan := sentencePlan{}
	prevPlain, prevRich := 0, 0
	for _, r := range ranges {
		rs, re := lo(r.Start), hi(r.End)
		gap := rich[prevRich:rs]
		if strings.Contains(gap, "<g") || strings.Contains(gap, "</g") {
			return sentencePlan{}, false
		}
		plan.plainGaps = append(plan.plainGaps, text[prevPlain:r.Start])
		plan.richGaps = append(plan.richGaps, gap)
		part := sentencePart{Text: text[r.Start:r.End]}
		if src := rich[rs:re]; richToken.MatchString(src) {
			if !balancedChips(src) {
				return sentencePlan{}, false
			}
			part.sourceRich = src
			part.rich, part.idBase = renumberChipIDs(src)
		}
		plan.Parts = append(plan.Parts, part)
		prevPlain, prevRich = r.End, re
	}
	tail := rich[prevRich:]
	if strings.Contains(tail, "<g") || strings.Contains(tail, "</g") {
		return sentencePlan{}, false
	}
	plan.plainGaps = append(plan.plainGaps, text[prevPlain:])
	plan.richGaps = append(plan.richGaps, tail)
	return plan, true
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
