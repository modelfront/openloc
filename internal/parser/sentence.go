package parser

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Sentence segmentation.
//
// Matches the segmentation of an external CAT tool, pinned by
// testdata/st4_reference.xlf (that tool's segmentation of st4_source.xml): 20 of
// its 129 trans-units come from splitting a single XML element into several
// sentences. Matching that behaviour is what lets a sentence-level TM built with
// that tool leverage against our segments.
//
// The rule is deliberately conservative — over-splitting costs TM matches and
// produces fragments a translator can't work with, so every break needs three
// signals to agree: terminator, whitespace, and a capital/digit start.

// TextRange is a half-open byte range [Start, End) within a string.
type TextRange struct {
	Start int
	End   int
}

// sentenceTerminators end a sentence when followed by whitespace + a
// capital/digit. The colon is in here because the reference splits on it —
// "Revised Information: F7m Flow Sensor Specifications" and
// "Ethanol: 0-8.5 l/min" are each two units. It's also why the
// whitespace requirement matters: "I:O", "1:2", "ISO 4064-2-55:2018" and
// "D0:UI_PUMP_MODE_SEL_ITM0" must stay intact.
const sentenceTerminators = ".!?:"

// sentenceAbbreviations never end a sentence, even when the next word starts
// with a capital. Stored without the trailing period, lowercased.
//
// Deliberately short. Unit abbreviations (ms, min, sec, vol, no) are NOT here:
// in technical documentation a unit far more often ends a sentence than
// abbreviates one, and the reference proves it — it splits
// "…one sample per 32 ms. The actual data sample rate…" in two. Adding "ms"
// here to catch the honorific "Ms." would silently merge that pair, and every
// other sentence ending in a measurement.
var sentenceAbbreviations = map[string]bool{
	"e.g":    true,
	"i.e":    true,
	"etc":    true,
	"fig":    true,
	"figs":   true,
	"approx": true,
	"vs":     true,
	"cf":     true,
	"dr":     true,
	"mr":     true,
	"mrs":    true,
	"prof":   true,
	"inc":    true,
	"ltd":    true,
	"chap":   true,
}

// sentenceOpeners can legitimately start a sentence alongside capitals and
// digits — a quoted or parenthesized sentence.
const sentenceOpeners = `([{"'` + "«“‘"

// SplitSentences returns the byte ranges of the sentences in text. Ranges
// exclude the whitespace between sentences and any leading/trailing whitespace,
// so the caller can splice translations back in without disturbing the
// original spacing.
//
// canBreak, when non-nil, vetoes a candidate break at the given byte offset.
// The XML parser uses it to reject breaks that would land inside an inline
// element (a chip must not be cut in half).
//
// Returns a single range covering the trimmed text when no break is found, and
// nil when the text has no non-whitespace content.
func SplitSentences(text string, canBreak func(pos int) bool) []TextRange {
	var ranges []TextRange

	start := skipSpaceForward(text, 0)
	if start >= len(text) {
		return nil
	}

	for i := start; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !strings.ContainsRune(sentenceTerminators, r) {
			i += size
			continue
		}

		// The terminator belongs to the sentence it closes, so a break here
		// ends the current range just after it.
		breakPos := i + size

		next := skipSpaceForward(text, breakPos)
		if next == breakPos {
			// No whitespace after the terminator — not a sentence end.
			// Covers "I:E", "0.05", "T.xx", "ISO 80601-2-55:2018".
			i += size
			continue
		}
		if next >= len(text) {
			// Trailing whitespace only; the final range picks up the rest.
			break
		}
		if !startsSentence(text[next:]) {
			i += size
			continue
		}
		if r == '.' && endsWithAbbreviation(text[:i]) {
			i += size
			continue
		}
		if canBreak != nil && !canBreak(breakPos) {
			i += size
			continue
		}

		if end := trimSpaceBackward(text, start, breakPos); end > start {
			ranges = append(ranges, TextRange{Start: start, End: end})
		}
		start = next
		i = next
	}

	if end := trimSpaceBackward(text, start, len(text)); end > start {
		ranges = append(ranges, TextRange{Start: start, End: end})
	}
	return ranges
}

// breakVeto returns a canBreak callback for SplitSentences that allows a
// break only where the terminator it follows is breakable: breakable[i]
// reports whether text byte i may end a sentence.
func breakVeto(breakable []bool) func(int) bool {
	return func(pos int) bool {
		return pos > 0 && pos <= len(breakable) && breakable[pos-1]
	}
}

// startsSentence reports whether s begins with a rune that can open a sentence:
// an uppercase letter, a digit, or an opening bracket/quote. Lowercase is what
// keeps "corresponds to the setting: ambient temperature" in one piece.
func startsSentence(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r) || unicode.IsDigit(r) || strings.ContainsRune(sentenceOpeners, r)
}

// endsWithAbbreviation reports whether the text immediately before a period is
// a known abbreviation or the tail of a dotted one.
//
// A single LOWERCASE letter counts, because that's how multi-part
// abbreviations end ("e.g." ends in "g", "i.e." in "e"). A single UPPERCASE
// letter deliberately does not: in technical documentation "Type B." or
// "Revision T." ending a sentence is common, while personal initials
// ("T. Smith") essentially never appear. That trade-off costs us the initials
// case and buys the far more frequent one.
func endsWithAbbreviation(before string) bool {
	word := trailingWord(before)
	if word == "" {
		return false
	}
	if utf8.RuneCountInString(word) == 1 {
		r, _ := utf8.DecodeRuneInString(word)
		return unicode.IsLower(r)
	}
	if sentenceAbbreviations[strings.ToLower(word)] {
		return true
	}
	// "e.g" / "i.e" arrive here only when the caller passes the full dotted
	// form; check it explicitly so the map entries stay readable.
	return sentenceAbbreviations[strings.ToLower(dottedTrailingWord(before))]
}

// trailingWord returns the run of letters at the end of s.
func trailingWord(s string) string {
	end := len(s)
	i := end
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		if !unicode.IsLetter(r) {
			break
		}
		i -= size
	}
	return s[i:end]
}

// dottedTrailingWord returns the trailing run of letters and interior periods,
// so "for e.g" yields "e.g".
func dottedTrailingWord(s string) string {
	end := len(s)
	i := end
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		if !unicode.IsLetter(r) && r != '.' {
			break
		}
		i -= size
	}
	return strings.Trim(s[i:end], ".")
}

// skipSpaceForward returns the index of the first non-space byte at or after i.
func skipSpaceForward(s string, i int) int {
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return i
}

// trimSpaceBackward returns the index just past the last non-space byte in
// s[start:end], or start when the slice is all whitespace.
func trimSpaceBackward(s string, start, end int) int {
	for end > start {
		r, size := utf8.DecodeLastRuneInString(s[start:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	return end
}
