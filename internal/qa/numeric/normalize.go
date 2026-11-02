// Package numeric compares the numbers, dates, times and measurements of a
// segment's source and target for the QA checks of the Numbers group.
package numeric

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// text is a segment prepared for scanning. norm has one rune for each rune of
// the original: every decimal digit script is folded to ASCII, the minus sign
// to '-', full-width punctuation to ASCII, every no-break or thin space to
// U+00A0 and the typographic apostrophe to an ASCII apostrophe. Byte offsets
// in norm map to the original through runeAt.
type text struct {
	orig    []rune
	norm    string
	runeAt  []int    // byte offset in norm → rune index
	windows [][2]int // the parts of norm near a digit (see findAll)
}

const nbsp = '\u00a0'

func newText(s string) *text {
	orig := []rune(s)
	var b strings.Builder
	runeAt := make([]int, 0, len(s)+1)
	for i, r := range orig {
		n := normRune(r)
		for range len(string(n)) {
			runeAt = append(runeAt, i)
		}
		b.WriteRune(n)
	}
	runeAt = append(runeAt, len(orig))
	return &text{orig: orig, norm: b.String(), runeAt: runeAt}
}

// original returns the original text of the norm byte span [start, end).
func (t *text) original(start, end int) string {
	return string(t.orig[t.runeAt[start]:t.runeAt[end]])
}

// window is how far a date or time reaches from its digits: the longest
// month name, day period or connecting word fits in it.
const window = 48

// findAll runs re over the parts of the text within window bytes of a digit.
// Every date and time has a digit in it, and a pattern costs time in
// proportion to the text it scans, so long text with few numbers stays cheap.
// Match indexes are offsets in norm.
func (t *text) findAll(re *regexp.Regexp) [][]int {
	if t.windows == nil {
		t.windows = digitWindows(t.norm, window)
	}
	var out [][]int
	for _, w := range t.windows {
		for _, m := range re.FindAllStringSubmatchIndex(t.norm[w[0]:w[1]], -1) {
			for i := range m {
				if m[i] >= 0 {
					m[i] += w[0]
				}
			}
			out = append(out, m)
		}
	}
	return out
}

// digitWindows returns the spans of s within margin bytes of a digit,
// merged, on rune boundaries.
func digitWindows(s string, margin int) [][2]int {
	out := [][2]int{}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			continue
		}
		j := i
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		start, end := max(i-margin, 0), min(j+margin, len(s))
		for start > 0 && !utf8.RuneStart(s[start]) {
			start--
		}
		for end < len(s) && !utf8.RuneStart(s[end]) {
			end++
		}
		if n := len(out); n > 0 && start <= out[n-1][1] {
			out[n-1][1] = end
		} else {
			out = append(out, [2]int{start, end})
		}
		i = j
	}
	return out
}

func normRune(r rune) rune {
	if r < 0x80 {
		return r
	}
	if unicode.IsDigit(r) {
		return '0' + digitValue(r)
	}
	switch r {
	case '\u2212', '\ufe63', '\uff0d': // minus signs
		return '-'
	case '\u00a0', '\u202f', '\u2009', '\u2007', '\u2008': // no-break, narrow, thin, figure, punctuation space
		return nbsp
	case '\u2019', '\u02bc', '\u2032': // typographic apostrophe, modifier apostrophe, prime
		return '\''
	case '\u2033': // double prime
		return '"'
	case '\uff0c', '\u066c': // full-width comma, Arabic thousands separator
		return ','
	case '\uff0e', '\u066b': // full-width full stop, Arabic decimal separator
		return '.'
	case '\uff1a':
		return ':'
	case '\uff0f':
		return '/'
	}
	return r
}

// digitValue is the value of a decimal digit of any script: Unicode lays out
// every Nd script as runs of ten, zero first.
func digitValue(r rune) rune {
	for _, rg := range unicode.Nd.R16 {
		if r >= rune(rg.Lo) && r <= rune(rg.Hi) {
			return (r - rune(rg.Lo)) % 10
		}
	}
	for _, rg := range unicode.Nd.R32 {
		if r >= rune(rg.Lo) && r <= rune(rg.Hi) {
			return (r - rune(rg.Lo)) % 10
		}
	}
	return 0
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// isWordRune reports a letter or digit: a token that touches one on either
// side is part of a longer word.
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// boundaryOK reports that the norm byte span [start, end) is not glued to a
// letter or digit on either side.
func (t *text) boundaryOK(start, end int) bool {
	if start > 0 {
		if r := t.orig[t.runeAt[start]-1]; isWordRune(r) {
			return false
		}
	}
	if i := t.runeAt[end]; i < len(t.orig) && isWordRune(t.orig[i]) {
		return false
	}
	return true
}
