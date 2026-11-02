package numeric

import (
	"strings"
	"unicode/utf8"
)

// number is one number as written: its span in the normalized text, the
// original text, and the values it can stand for (see numberValues).
type number struct {
	start, end int
	text       string
	norm       string
	values     []string
	readings   []reading
	// spaced is set when a space or apostrophe groups its digits: if it
	// matches nothing, its parts are compared one by one.
	spaced bool
}

// scanNumbers finds the plain numbers of the norm byte span [from, to). A
// number is digits joined by '.' or ',' — and, as a grouping separator between
// one to three digits and exactly three, by a no-break space always, by a
// space where the language groups with spaces and by an apostrophe where it
// groups with one.
// A minus sign belongs to the number only when no letter or digit comes right
// before it: "10-20" is a range.
func (t *text) scanNumbers(from, to int, loc locale) []number {
	var out []number
	s := t.norm
	for i := from; i < to; {
		if !isDigit(s[i]) {
			i++
			continue
		}
		start := i
		if start > from && s[start-1] == '-' && (start-1 == 0 || !t.wordBefore(start-1)) {
			start--
		}
		i = t.numberEnd(i, to, loc)
		out = append(out, t.newNumber(start, i, loc))
	}
	return out
}

// numberEnd returns where the number starting at the digit at i ends.
func (t *text) numberEnd(i, to int, loc locale) int {
	s := t.norm
	run := 0 // digits since the last separator
	for i < to {
		switch {
		case isDigit(s[i]):
			i++
			run++
		case s[i] == '.' || s[i] == ',':
			i++
			run = 0
		case run >= 1 && run <= 3 && i+1 < to && t.groupsAt(i, to, loc):
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size + 3
			run = 3
		default:
			return i
		}
	}
	return i
}

// groupsAt reports a grouping space or apostrophe at i followed by exactly
// three digits.
func (t *text) groupsAt(i, to int, loc locale) bool {
	s := t.norm
	r, size := utf8.DecodeRuneInString(s[i:])
	switch {
	case loc.noSpaceGroups:
		return false
	case r == nbsp:
	case r == ' ' && loc.spaceGroup:
	case r == '\'' && loc.apostropheGroup:
	default:
		return false
	}
	j := i + size
	if j+3 > to || !isDigit(s[j]) || !isDigit(s[j+1]) || !isDigit(s[j+2]) {
		return false
	}
	return j+3 == to || !isDigit(s[j+3])
}

// wordBefore reports a letter or digit right before the norm byte offset i.
func (t *text) wordBefore(i int) bool {
	r, _ := utf8.DecodeLastRuneInString(t.norm[:i])
	return isWordRune(r)
}

func (t *text) newNumber(start, end int, loc locale) number {
	norm := t.norm[start:end]
	values := numberValues(norm, loc.decimal)
	return number{
		start: start, end: end, text: t.original(start, end), norm: norm,
		values: values, readings: readingsOf(values),
		spaced: strings.ContainsAny(norm, " '"+string(nbsp)),
	}
}

// parts splits a number grouped with spaces or apostrophes into the numbers
// either side of them.
func (t *text) parts(n number, loc locale) []number {
	loc.noSpaceGroups = true
	return t.scanNumbers(n.start, n.end, loc)
}

// numberValues returns the values a number can stand for, as "1234" or
// "1234.5" (no trailing zeros after the point, so "1.50" is "1.5"), followed
// by the token itself, so a number copied unchanged always matches. dec is
// the language's decimal separator, or 0 when it is not known.
//
// Spaces and apostrophes only group. Of '.' and ',', the last one is the
// decimal separator when the other kind comes before it ("1,234.56"); one
// that repeats groups digits ("1,000,000"), as do lists and versions
// ("1.2.3"). One separator followed by exactly three digits ("1,000") is
// grouping or a decimal, so dec decides, and with no dec either reading
// counts. Any other single separator is decimal ("25.16", "2,5").
func numberValues(token string, dec rune) []string {
	tok := strings.TrimRight(token, ".,") // sentence or list punctuation
	sign := ""
	if strings.HasPrefix(tok, "-") {
		sign, tok = "-", tok[1:]
	}
	literal := "=" + sign + tok
	tok = strings.NewReplacer(" ", "", string(nbsp), "", "'", "").Replace(tok)
	last := strings.LastIndexAny(tok, ".,")
	if last < 0 {
		return []string{sign + tok, literal}
	}
	sep, other := tok[last], byte('.')
	if sep == '.' {
		other = ','
	}
	intPart, frac := tok[:last], tok[last+1:]
	digitsOnly := strings.NewReplacer(",", "", ".", "")
	grouped := sign + digitsOnly.Replace(tok)
	decimal := sign + digitsOnly.Replace(intPart)
	if frac := strings.TrimRight(frac, "0"); frac != "" {
		decimal += "." + frac
	}

	var values []string
	switch {
	case strings.IndexByte(intPart, sep) >= 0:
		values = []string{grouped}
	case strings.IndexByte(intPart, other) >= 0:
		values = []string{decimal}
	case len(frac) == 3 && len(intPart) <= 3 && intPart[0] != '0':
		switch dec {
		case rune(sep):
			values = []string{decimal}
		case 0:
			values = []string{grouped, decimal}
		default:
			values = []string{grouped}
		}
	default:
		values = []string{decimal}
	}
	return append(values, literal)
}

// shares reports a value the two numbers have in common.
func (n number) shares(o number) bool {
	for _, a := range n.values {
		for _, b := range o.values {
			if a == b {
				return true
			}
		}
	}
	return false
}
