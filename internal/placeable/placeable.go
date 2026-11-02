// Package placeable finds the parts of a source text a translator carries
// into the target as they are: numbers, dates, times, measurements, currency
// amounts, web and email addresses, software variables, and product codes.
package placeable

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"openloc/internal/qa/numeric"
	"openloc/internal/textspan"
)

// priority decides which of two overlapping placeables is kept: the lower.
var priority = map[Kind]int{
	KindURL: 0, KindEmail: 0, KindVariable: 1,
	KindDate: 2, KindTime: 2, KindCurrency: 3, KindMeasurement: 3,
	KindCode: 3, KindNumber: 4,
}

// Find returns the placeables of s, without overlaps, in text order. lang is
// the text's language; it decides how numbers and dates read.
func Find(s, lang string) []Placeable {
	var all []Placeable
	add := func(k Kind, start, end int) {
		if start < end {
			all = append(all, Placeable{Kind: k, Text: s[start:end], Start: start, End: end})
		}
	}
	for _, m := range textspan.URL.FindAllStringIndex(s, -1) {
		add(KindURL, m[0], m[0]+len(textspan.TrimURL(s[m[0]:m[1]])))
	}
	for _, m := range textspan.Email.FindAllStringIndex(s, -1) {
		add(KindEmail, m[0], m[1])
	}
	for _, m := range textspan.Variable.FindAllStringIndex(s, -1) {
		if s[m[0]] == '%' && afterDigit(s, m[0]) {
			continue // "20%-ige", "100%ige": a percentage, not a printf variable
		}
		add(KindVariable, m[0], m[1])
	}
	for _, sp := range numeric.Spans(s, lang, numeric.Separators{}) {
		start, end := sp.Start, sp.End
		k := Kind(sp.Kind)
		if k == KindNumber {
			end = trimNumberEnd(s, start, end)
			if gluedToLetter(s, start, end) {
				continue // the "3" of "mp3" is part of a word
			}
			if cs, ce, ok := currencyAround(s, start, end); ok {
				k, start, end = KindCurrency, cs, ce
			}
		}
		add(k, start, end)
	}
	for _, m := range codeToken.FindAllStringIndex(s, -1) {
		if isCode(s[m[0]:m[1]]) {
			add(KindCode, m[0], m[1])
		}
	}
	return resolve(all)
}

// resolve keeps, of overlapping placeables, the one of the lowest priority,
// then the earlier, then the longer, and returns them in text order.
func resolve(all []Placeable) []Placeable {
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if priority[a.Kind] != priority[b.Kind] {
			return priority[a.Kind] < priority[b.Kind]
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.End > b.End
	})
	kept := []Placeable{}
	for _, p := range all {
		overlaps := false
		for _, k := range kept {
			if p.Start < k.End && k.Start < p.End {
				overlaps = true
				break
			}
		}
		if !overlaps {
			kept = append(kept, p)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Start < kept[j].Start })
	return kept
}

// trimNumberEnd drops a separator that ends the number: in "costs 5." the
// dot ends the sentence.
func trimNumberEnd(s string, start, end int) int {
	for end > start+1 && (s[end-1] == '.' || s[end-1] == ',') {
		end--
	}
	return end
}

// currencySymbols are the symbols and codes written next to an amount. A
// prefixed dollar comes before "$", so "US$ 5" keeps its "US".
var currencySymbols = []string{"US$", "CA$", "AU$", "NZ$", "HK$", "C$", "A$", "R$", "$", "€", "£", "¥", "₹", "₽", "₩", "₺", "₪", "CHF", "USD", "EUR", "GBP", "JPY", "CNY", "CAD", "AUD", "SEK", "NOK", "DKK", "PLN", "CZK", "BRL", "INR"}

// currencyAround widens the number [start, end) to the currency symbol or
// code right before or after it, with at most one space between: "$5",
// "US$ 5", "5,00 €", "EUR 12".
func currencyAround(s string, start, end int) (int, int, bool) {
	before := strings.TrimSuffix(strings.TrimSuffix(s[:start], " "), "\u00a0")
	for _, c := range currencySymbols {
		if strings.HasSuffix(before, c) && !letterBefore(before, len(before)-len(c), c) {
			return len(before) - len(c), end, true
		}
	}
	after := s[end:]
	gap := 0
	if strings.HasPrefix(after, " ") {
		gap = 1
	} else if strings.HasPrefix(after, "\u00a0") {
		gap = len("\u00a0")
	}
	for _, c := range currencySymbols {
		if strings.HasPrefix(after[gap:], c) && !letterAfter(after, gap+len(c), c) {
			return start, end + gap + len(c), true
		}
	}
	return 0, 0, false
}

// letterBefore reports a letter glued to the front of a currency symbol or
// code at i that starts with a letter: "XUSD" is no code, and "PLUS$5" has
// "$", not "US$". A bare "$" may follow letters.
func letterBefore(s string, i int, code string) bool {
	if first, _ := utf8.DecodeRuneInString(code); !unicode.IsLetter(first) || i == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return unicode.IsLetter(r)
}

func letterAfter(s string, i int, code string) bool {
	if i >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	if isLetters(code) {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	return false
}

func isLetters(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// codeToken is a run of letters and digits, maybe joined by - _ . or /. Its
// letters are of the scripts that separate words with spaces, so a code ends
// where Chinese or Japanese text begins.
var codeToken = regexp.MustCompile(`[\p{Latin}\p{Greek}\p{Cyrillic}\p{N}]+(?:[-_./][\p{Latin}\p{Greek}\p{Cyrillic}\p{N}]+)*`)

// isCode reports a product or part number: a token that starts with a letter
// and has a digit and an upper-case letter ("XJ-500B", "A4", "SKU_1234-B"). A
// token that starts with a digit ("5GB", "4K", "2FA") is a number glued to
// letters, and no placeable.
func isCode(tok string) bool {
	if first, _ := utf8.DecodeRuneInString(tok); !unicode.IsLetter(first) {
		return false
	}
	var digit, upper bool
	for _, r := range tok {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case unicode.IsUpper(r):
			upper = true
		}
	}
	return digit && upper
}

// afterDigit reports a decimal digit right before i.
func afterDigit(s string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return i > 0 && unicode.IsDigit(r)
}

// gluedToLetter reports a word letter right before or after [start, end).
func gluedToLetter(s string, start, end int) bool {
	if r, _ := utf8.DecodeLastRuneInString(s[:start]); start > 0 && IsWordLetter(r) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[end:])
	return end < len(s) && IsWordLetter(r)
}

// IsWordLetter reports a letter that makes a number or code next to it part of
// a word. Chinese, Japanese, Korean, Thai, Lao, Khmer and Burmese write a
// number right against its word ("500円", "5개", "500บาท"), so their letters
// never do.
func IsWordLetter(r rune) bool {
	return unicode.IsLetter(r) && !unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul,
		unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar)
}

// Count counts the occurrences of p's text in s that are that placeable. A
// variable, web or email address counts wherever its text is ("%d" is in
// "%dx%d"). A code counts where it does not run on into a longer code: "MP3"
// is in "MP3-Player", but "XJ-500B" is not in "XJ-500B-2" and "SKU_1234" is
// not in "SKU_1234_B". A number, date, time or amount counts where no word
// letter or digit is right against its own letter or digit edge: "5" is not
// in "15", but "$5" is in "PLUS$5". web/src/lib/
// placeables.ts `countOccurrences` draws the same lines.
func Count(s string, p Placeable) int {
	switch {
	case p.Text == "":
		return 0
	case p.Kind == KindVariable || p.Kind == KindURL || p.Kind == KindEmail:
		return strings.Count(s, p.Text)
	}
	runsOn := codeRunsOn
	if p.Kind != KindCode {
		runsOn = wordRunsOn
	}
	n := 0
	for i := 0; i <= len(s)-len(p.Text); {
		j := strings.Index(s[i:], p.Text)
		if j < 0 {
			break
		}
		start, end := i+j, i+j+len(p.Text)
		if !(edgeIsWord(p.Text, true) && runsOn(s[:start], true)) && !(edgeIsWord(p.Text, false) && runsOn(s[end:], false)) {
			n++
			i = end
			continue
		}
		i = start + 1
	}
	return n
}

// edgeIsWord reports a word letter or digit at the first (or last) character
// of a placeable: only such an edge can run on into the text beside it, so
// "$5" is in "PLUS$5".
func edgeIsWord(text string, first bool) bool {
	r, _ := utf8.DecodeRuneInString(text)
	if !first {
		r, _ = utf8.DecodeLastRuneInString(text)
	}
	return IsWordLetter(r) || unicode.IsDigit(r)
}

// wordRunsOn reports a word letter or digit right against a placeable on one
// side; before is the text before it, else the text after.
func wordRunsOn(side string, before bool) bool {
	r, _ := utf8.DecodeRuneInString(side)
	if before {
		r, _ = utf8.DecodeLastRuneInString(side)
	}
	return side != "" && (IsWordLetter(r) || unicode.IsDigit(r))
}

// codeRunsOn reports that the text on one side of a code continues it: a
// Latin, Greek or Cyrillic letter or a digit right against it, or "_", "." or "/" and then one, or "-"
// and then a digit ("XJ-500B-2"). "-" and then a word is a compound
// ("MP3-Player"). before is the text before the code, else the text after.
func codeRunsOn(side string, before bool) bool {
	next := func(s string) (rune, string) {
		if before {
			r, n := utf8.DecodeLastRuneInString(s)
			return r, s[:len(s)-n]
		}
		r, n := utf8.DecodeRuneInString(s)
		return r, s[n:]
	}
	// The letters codeToken takes: a code runs on only into what it could have
	// been read as.
	word := func(r rune) bool {
		return unicode.IsDigit(r) || unicode.In(r, unicode.Latin, unicode.Greek, unicode.Cyrillic)
	}
	if side == "" {
		return false
	}
	r, rest := next(side)
	if word(r) {
		return true
	}
	if rest == "" {
		return false
	}
	r2, _ := next(rest)
	switch r {
	case '_', '.', '/':
		return word(r2)
	case '-':
		return unicode.IsDigit(r2)
	}
	return false
}
