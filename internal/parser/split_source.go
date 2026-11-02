package parser

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"openloc/internal/segmentation"
)

// Piece is one piece of a segment that a split cut, or that a join or an
// export puts back together.
type Piece struct {
	// Text is the plain text.
	Text string
	// Rich is the text with its chips, ids renumbered from 1; "" when it holds
	// no chip.
	Rich string
	// ChipBase is added to Rich's chip ids to give the ids of the text the
	// piece came from.
	ChipBase int
	// Gap is the source between the piece before and this one, in rich form
	// when the source has chips; "" for the first piece.
	Gap string
}

var (
	// ErrSplitPosition is a split SplitSourceAt refuses at this offset.
	ErrSplitPosition = errors.New("parser: the segment cannot be split at this position")
	// ErrSplitTags is a source whose inline codes SplitSourceAt cannot cut
	// (XLIFF <bpt>/<ept>/<ph> carry escaped markup, not chips).
	ErrSplitTags = errors.New("parser: the segment's inline codes cannot be split")
)

// SplitSourceAt cuts a segment's source in two at byte offset off of its
// plain text, with the rules sentence splitting uses: the cut never falls
// inside a paired tag or a "{…}", and each piece's chip ids are renumbered
// from 1. Whitespace around off becomes the second piece's Gap. rich is the
// source with its chips, or "" when it has none.
//
// The error is ErrSplitTags when rich does not line up with text, and
// ErrSplitPosition when off is at an edge or inside a character, a piece
// would hold only whitespace, or the cut is not allowed there.
func SplitSourceAt(text, rich string, off int) ([2]Piece, error) {
	var none [2]Piece
	if rich != "" && RichPlainText(rich) != text {
		return none, ErrSplitTags
	}
	if off <= 0 || off >= len(text) || !utf8.RuneStart(text[off]) {
		return none, ErrSplitPosition
	}
	end := off
	for end > 0 {
		r, n := utf8.DecodeLastRuneInString(text[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= n
	}
	start := off
	for start < len(text) {
		r, n := utf8.DecodeRuneInString(text[start:])
		if !unicode.IsSpace(r) {
			break
		}
		start += n
	}
	if strings.TrimSpace(text[:end]) == "" || strings.TrimSpace(text[start:]) == "" {
		return none, ErrSplitPosition
	}
	cut := func(t string, canBreak func(int) bool) ([]segmentation.TextRange, error) {
		if canBreak != nil && !canBreak(off) {
			return nil, nil
		}
		return []segmentation.TextRange{{Start: 0, End: end}, {Start: start, End: len(t)}}, nil
	}
	plan, ok, err := planSentences(cut, text, rich)
	if err != nil || !ok || len(plan.parts) != 2 {
		return none, ErrSplitPosition
	}
	var out [2]Piece
	for i, part := range plan.parts {
		out[i] = Piece{Text: part.text, Rich: part.rich, ChipBase: part.idBase}
	}
	out[1].Gap = plan.plainGaps[1]
	if rich != "" {
		out[1].Gap = plan.richGaps[1]
	}
	return out, nil
}

// JoinPieces puts pieces back together in order, each after its Gap: the
// plain text, and the rich text with every chip id shifted back by its
// piece's ChipBase. rich is "" when no piece or gap holds a chip. The first
// piece's Gap is not written.
func JoinPieces(pieces []Piece) (text, rich string) {
	var plain, tagged strings.Builder
	chips := false
	for i, p := range pieces {
		if i > 0 {
			plain.WriteString(RichPlainText(p.Gap))
			tagged.WriteString(p.Gap)
			chips = chips || richToken.MatchString(p.Gap)
		}
		plain.WriteString(p.Text)
		if p.Rich != "" {
			tagged.WriteString(shiftChipIDs(p.Rich, p.ChipBase))
			chips = true
		} else {
			tagged.WriteString(p.Text)
		}
	}
	if !chips {
		return plain.String(), ""
	}
	return plain.String(), tagged.String()
}

// RenumberChips renumbers rich's chip ids from 1 and returns what was
// subtracted, as a split does for each piece.
func RenumberChips(rich string) (string, int) {
	return renumberChipIDs(rich)
}

// RichPlainText is rich without its chips, a line-break chip read as "\n",
// as the parsers write the plain text.
func RichPlainText(rich string) string {
	return richToken.ReplaceAllStringFunc(rich, func(tag string) string {
		if lineBreakChip.MatchString(tag) {
			return "\n"
		}
		return ""
	})
}
