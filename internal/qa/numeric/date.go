package numeric

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// ymd is a calendar date; a zero field is not written ("March 14").
type ymd struct{ y, m, d int }

// valid reports a plausible date: a month, and a day that month can have.
func (v ymd) valid() bool {
	if v.m < 1 || v.m > 12 || v.d < 0 || v.d > 31 || (v.d == 0 && v.y == 0) {
		return false
	}
	return v.d <= [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[v.m]
}

// satisfiedBy reports a target date t that writes every field of the source
// date s, with the same value. Fields only the target writes are fine.
func (s ymd) satisfiedBy(t ymd) bool {
	return (s.y == 0 || s.y == t.y) && (s.m == 0 || s.m == t.m) && (s.d == 0 || s.d == t.d)
}

// fullYear reads a two-digit year the way people write them: 00–69 is 20xx.
func fullYear(y int, digits int) int {
	if digits == 2 {
		if y < 70 {
			return 2000 + y
		}
		return 1900 + y
	}
	return y
}

var (
	// numericDate: three numbers joined by the same '/', '.' or '-'.
	numericDate = regexp.MustCompile(`(\d{1,4})([./-])(\d{1,2})([./-])(\d{1,4})\.?`)
	// dayMonthDot: the German-style "14.3." with no year. Weak.
	dayMonthDot = regexp.MustCompile(`(\d{1,2})\.(\d{1,2})\.`)
	// monthDaySlash: "3/14" or "14/3". Weak: a fraction looks the same.
	monthDaySlash = regexp.MustCompile(`(\d{1,2})/(\d{1,2})`)
	cjkDate       = regexp.MustCompile(`(?:(\d{4})\s*[年년]\s*)?(\d{1,2})\s*[月월]\s*(?:(\d{1,2})\s*[日일])?`)
)

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// readNumericDate returns the dates three numbers can be, in the language's
// orders, or in any order when none of those gives a valid date.
func readNumericDate(a, b, c string, orders []string) []ymd {
	read := func(order string) (ymd, bool) {
		parts := map[byte]string{order[0]: a, order[1]: b, order[2]: c}
		y := parts['y']
		if len(y) != 2 && len(y) != 4 || len(parts['m']) > 2 || len(parts['d']) > 2 {
			return ymd{}, false
		}
		v := ymd{y: fullYear(atoi(y), len(y)), m: atoi(parts['m']), d: atoi(parts['d'])}
		return v, v.d > 0 && v.valid()
	}
	var out []ymd
	for _, list := range [][]string{orders, allOrders} {
		for _, o := range list {
			if v, ok := read(o); ok {
				out = appendDate(out, v)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func appendDate(list []ymd, v ymd) []ymd {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// dateCandidates finds the dates of the text.
func (t *text) dateCandidates(loc locale) []candidate {
	var out []candidate
	s := t.norm
	for _, m := range t.findAll(numericDate) {
		a, sep, b, sep2, c := s[m[2]:m[3]], s[m[4]:m[5]], s[m[6]:m[7]], s[m[8]:m[9]], s[m[10]:m[11]]
		end := trimDot(s, m[0], m[1])
		if sep != sep2 || !t.boundaryOK(m[0], m[11]) || followedByNumber(s, m[11]) {
			continue
		}
		// A dotted date with a two-digit year needs two-digit day and month
		// ("14.03.26"): "1.2.23" is a version. A hyphen needs a four-digit year.
		if sep == "-" && len(a) != 4 && len(c) != 4 {
			continue
		}
		if sep == "." && len(a) != 4 && len(c) == 2 && (len(a) != 2 || len(b) != 2) {
			continue
		}
		var readings []ymd
		if len(a) == 4 {
			readings = readNumericDate(a, b, c, []string{"ymd"})
		} else {
			readings = readNumericDate(a, b, c, loc.dateOrders)
		}
		if len(readings) > 0 {
			out = append(out, candidate{kind: kindDate, start: m[0], end: end, dates: readings})
		}
	}
	for _, m := range t.findAll(dayMonthDot) {
		if !t.boundaryOK(m[0], m[1]-1) || followedByNumber(s, m[1]-1) || (m[1] < len(s) && isDigit(s[m[1]])) {
			continue
		}
		if v := (ymd{m: atoi(s[m[4]:m[5]]), d: atoi(s[m[2]:m[3]])}); v.d > 0 && v.valid() {
			out = append(out, candidate{kind: kindDate, start: m[0], end: m[1], weak: true, dates: []ymd{v}})
		}
	}
	for _, m := range t.findAll(monthDaySlash) {
		if !t.boundaryOK(m[0], m[1]) || followedByNumber(s, m[1]) {
			continue
		}
		a, b := atoi(s[m[2]:m[3]]), atoi(s[m[4]:m[5]])
		var readings []ymd
		for _, v := range []ymd{{m: a, d: b}, {m: b, d: a}} {
			if v.d > 0 && v.valid() {
				readings = appendDate(readings, v)
			}
		}
		if len(readings) > 0 {
			out = append(out, candidate{kind: kindDate, start: m[0], end: m[1], weak: true, dates: readings})
		}
	}
	for _, m := range t.findAll(cjkDate) {
		v := ymd{m: atoi(s[m[4]:m[5]])}
		if m[2] >= 0 {
			v.y = atoi(s[m[2]:m[3]])
		}
		if m[6] >= 0 {
			v.d = atoi(s[m[6]:m[7]])
		}
		if (v.y != 0 || v.d != 0) && v.valid() {
			out = append(out, candidate{kind: kindDate, start: m[0], end: m[1], dates: []ymd{v}})
		}
	}
	return append(out, t.monthNameDates(loc)...)
}

// followedByNumber reports a separator and a digit at i: the match is part
// of a longer run such as an IP address.
func followedByNumber(s string, i int) bool {
	return i+1 < len(s) && strings.ContainsRune("./-:", rune(s[i])) && isDigit(s[i+1])
}

// monthPatterns are the month-name date patterns of one language, its month
// names and the English ones together (an English month in any text is a
// date).
type monthPatterns struct {
	byName map[string]int
	// short holds the abbreviations: names shorter than another name the
	// same language has for that month ("set" next to "settembre").
	short      map[string]bool
	dayFirst   *regexp.Regexp // 14 March 2026, 14. März, 14 de marzo de 2026
	monthFirst *regexp.Regexp // March 14, 2026
	yearFirst  *regexp.Regexp // 2026. március 14.
	monthYear  *regexp.Regexp // March 2026
}

var monthCache sync.Map // the month name lists → *monthPatterns

func monthPatternsFor(loc locale) *monthPatterns {
	var key strings.Builder
	for _, names := range loc.months {
		key.WriteString(strings.Join(names, "|") + "/")
	}
	key.WriteString(strings.Join(loc.monthWords, "|"))
	if p, ok := monthCache.Load(key.String()); ok {
		return p.(*monthPatterns)
	}
	en := localeFor("en")
	p := &monthPatterns{byName: map[string]int{}, short: map[string]bool{}}
	// English first: the language's own list then decides for every name it
	// has, so Malay "Jun" is a full name even though English abbreviates June
	// that way.
	for _, months := range [][12][]string{en.months, loc.months} {
		for i, names := range months {
			longest := 0
			for _, n := range names {
				longest = max(longest, utf8.RuneCountInString(n))
			}
			for _, n := range names {
				r, size := utf8.DecodeRuneInString(n)
				if unicode.IsDigit(r) {
					continue // "2月", "2월": cjkDate reads these
				}
				forms := []string{n}
				if unicode.IsLower(r) {
					forms = append(forms, string(unicode.ToTitle(r))+n[size:])
				}
				for _, f := range forms {
					p.byName[f] = i + 1
					p.short[f] = utf8.RuneCountInString(n) < longest
				}
			}
		}
	}
	for _, w := range loc.monthWords {
		p.short[w] = true
	}
	names := make([]string, 0, len(p.byName))
	for n := range p.byName {
		names = append(names, regexp.QuoteMeta(n))
	}
	// Longest first, so "Sept." wins over "Sep".
	sort.Slice(names, func(i, j int) bool {
		return len(names[i]) > len(names[j]) || len(names[i]) == len(names[j]) && names[i] < names[j]
	})
	month := `(` + strings.Join(names, "|") + `)\.?`
	const (
		day     = `(\d{1,2})`
		ordinal = `(?:st|nd|rd|th|er|º|°|\.)?`
		of      = `(?:\s*(?:de|del|of)\s+|\s*)`
		year    = `(\d{4})`
	)
	p.dayFirst = regexp.MustCompile(day + ordinal + of + month + `(?:,?` + of + year + `)?`)
	p.monthFirst = regexp.MustCompile(month + `\s+` + day + ordinal + `(?:,?\s+` + year + `)?`)
	p.yearFirst = regexp.MustCompile(year + `\.?\s+` + month + `\s+` + day + `\.?`)
	p.monthYear = regexp.MustCompile(month + `\s+` + year)
	monthCache.Store(key.String(), p)
	return p
}

func (t *text) monthNameDates(loc locale) []candidate {
	p := monthPatternsFor(loc)
	s := t.norm
	var out []candidate
	add := func(start, end int, v ymd, month string) {
		if end > start && t.boundaryOK(start, end) && v.valid() {
			// "2 set" is two sets in Italian before it is September 2: an
			// abbreviated month with no year counts only when it matches the
			// other side.
			weak := v.y == 0 && p.short[month]
			out = append(out, candidate{kind: kindDate, start: start, end: end, dates: []ymd{v}, weak: weak})
		}
	}
	group := func(m []int, i int) string {
		if m[2*i] < 0 {
			return ""
		}
		return s[m[2*i]:m[2*i+1]]
	}
	for _, m := range t.findAll(p.dayFirst) {
		add(m[0], trimDot(s, m[0], m[1]), ymd{d: atoi(group(m, 1)), m: p.byName[group(m, 2)], y: atoi(group(m, 3))}, group(m, 2))
	}
	for _, m := range t.findAll(p.monthFirst) {
		add(m[0], trimDot(s, m[0], m[1]), ymd{m: p.byName[group(m, 1)], d: atoi(group(m, 2)), y: atoi(group(m, 3))}, group(m, 1))
	}
	for _, m := range t.findAll(p.yearFirst) {
		add(m[0], trimDot(s, m[0], m[1]), ymd{y: atoi(group(m, 1)), m: p.byName[group(m, 2)], d: atoi(group(m, 3))}, group(m, 2))
	}
	for _, m := range t.findAll(p.monthYear) {
		add(m[0], m[1], ymd{m: p.byName[group(m, 1)], y: atoi(group(m, 2))}, group(m, 1))
	}
	return out
}

// trimDot drops a final '.' that ends the sentence rather than the date.
func trimDot(s string, start, end int) int {
	if end-1 > start && s[end-1] == '.' && (end == len(s) || s[end] == ' ' || s[end] == '\n') {
		return end - 1
	}
	return end
}
