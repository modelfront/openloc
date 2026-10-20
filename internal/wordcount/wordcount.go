// Package wordcount implements word counting for translation analysis.
//
// Rules:
//   - Whitespace-delimited tokens, Unicode-aware (strings.Fields)
//   - Inline tags (<...>) stripped before counting
//   - Standalone numbers excluded (they are "placeables", not words)
//   - Mixed alphanumeric tokens counted as words ("B2B" = 1 word)
//   - Hyphenated words = 1 word ("well-known" = 1)
//   - Apostrophe words = 1 word ("it's" = 1, "l'homme" = 1)
//   - Punctuation-only tokens excluded
//   - CJK characters: each character = 1 word
//   - Korean: each character = 1 word (same as CJK)
//   - URLs and emails = 1 word each
//
// This is the single source of truth for word counts in OpenLoc. Used at:
//   - File upload (per-segment word_count stored in DB)
//   - Analysis queries (SUM(s.word_count) — no SQL-side recalculation)
//   - Job-level word_count (sum of segment word counts)
package wordcount

import (
	"regexp"
	"strings"
	"unicode"
)

// tagRe matches inline HTML/XML tags to strip before counting.
var tagRe = regexp.MustCompile(`<[^>]+>`)

// CountWords returns the word count for a source text segment.
func CountWords(text string) int {
	// Step 1: Strip inline tags (XML/HTML markup is not translatable content).
	text = tagRe.ReplaceAllString(text, " ")

	// Step 2: Normalize invisible word boundaries that strings.Fields doesn't handle.
	// Zero-width space (U+200B) is used in Arabic/Thai and copy-pasted web content.
	// Soft hyphen (U+00AD) is an invisible break hint inserted by word processors.
	text = strings.ReplaceAll(text, "\u200B", " ")
	text = strings.ReplaceAll(text, "\u00AD", " ")

	// Step 3: Split on Unicode whitespace.
	tokens := strings.Fields(text)

	count := 0
	for _, tok := range tokens {
		count += countToken(tok)
	}
	return count
}

// countToken returns the word count contribution of a single whitespace-delimited token.
// Most tokens contribute 0 or 1, but tokens containing CJK characters may contribute more
// (each CJK character = 1 word).
func countToken(tok string) int {
	runes := []rune(tok)

	// Check if token contains any CJK characters.
	// If so, count CJK chars individually + count non-CJK letter runs as words.
	hasCJK := false
	for _, r := range runes {
		if isCJK(r) {
			hasCJK = true
			break
		}
	}

	if hasCJK {
		return countMixedCJK(runes)
	}

	// Non-CJK token: classify as word, number, or punctuation.
	if isPureNumeric(tok) {
		// Standalone numbers are "placeables", not counted as words.
		// This includes integers, decimals, comma-separated ("1,000.50"), signed ("-3.14").
		return 0
	}

	if isPurePunctuation(runes) {
		// Punctuation-only tokens ("...", "—", "***") are not words.
		return 0
	}

	// Everything else is 1 word. This naturally handles:
	// - Regular words ("hello" = 1)
	// - Hyphenated words ("well-known" = 1)
	// - Apostrophe words ("it's" = 1, "l'homme" = 1)
	// - Mixed alphanumeric ("B2B" = 1, "COVID19" = 1)
	// - URLs ("https://example.com" = 1)
	// - Emails ("user@example.com" = 1)
	return 1
}

// countMixedCJK counts words in a token containing CJK characters.
// Each CJK character = 1 word. Non-CJK letter runs between CJK chars = 1 word each.
// Example: "Hello世界Test" = 1 ("Hello") + 2 (世, 界) + 1 ("Test") = 4
func countMixedCJK(runes []rune) int {
	count := 0
	inLetterRun := false

	for _, r := range runes {
		if isCJK(r) {
			// Each CJK character = 1 word.
			if inLetterRun {
				count++ // close the preceding letter run
				inLetterRun = false
			}
			count++
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			inLetterRun = true
		} else {
			// Punctuation/symbol between runs.
			if inLetterRun {
				count++
				inLetterRun = false
			}
		}
	}
	if inLetterRun {
		count++
	}
	return count
}

// isPureNumeric checks if a token is a standalone number (possibly with decimals,
// commas, signs, percent, currency symbols). These are "placeables" — not words.
//
// Matches: "123", "1,000", "3.14", "-5", "+3.5", "$100", "€50", "100%", "1,234.56"
// Does NOT match: "B2B", "COVID19", "3rd" (these contain letters → counted as words).
func isPureNumeric(tok string) bool {
	hasDigit := false
	for _, r := range tok {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '.' || r == ',' || r == '-' || r == '+' || r == '%':
			// Numeric separators and signs.
		case unicode.Is(unicode.Sc, r):
			// Currency symbols ($, €, £, ¥, etc.).
		default:
			return false
		}
	}
	return hasDigit
}

// isPurePunctuation checks if every rune in the token is punctuation or a symbol
// (not a letter or digit). Catches "...", "—", "***", "//", etc.
func isPurePunctuation(runes []rune) bool {
	if len(runes) == 0 {
		return true
	}
	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// isCJK returns true if r is a CJK ideograph, Hiragana, or Katakana character.
// These scripts don't use whitespace word boundaries — each character is counted
// as one word.
//
// Covers:
//   - CJK Unified Ideographs (U+4E00–U+9FFF) — Chinese/Japanese kanji
//   - CJK Extension A (U+3400–U+4DBF)
//   - CJK Extension B+ (U+20000–U+2A6DF)
//   - CJK Compatibility Ideographs (U+F900–U+FAFF)
//   - Hiragana (U+3040–U+309F) — Japanese
//   - Katakana (U+30A0–U+30FF) — Japanese
//   - Hangul Syllables (U+AC00–U+D7AF) — Korean
//   - Hangul Jamo (U+1100–U+11FF)
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}
