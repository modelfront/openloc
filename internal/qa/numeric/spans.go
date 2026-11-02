package numeric

import "sort"

// Span kinds returned by Spans.
const (
	SpanNumber      = "number"
	SpanDate        = "date"
	SpanTime        = "time"
	SpanMeasurement = "measurement"
)

// Span is a number, date, time or measurement found in a text: its kind and
// its byte offsets in the text as given.
type Span struct {
	Kind       string
	Start, End int
}

// Spans returns the numbers, dates, times and measurements of s in order,
// read the way the number checks read the text of language lang. Weak dates
// ("3/4") count as dates. Digits inside web addresses, email addresses and
// software placeholders are skipped.
func Spans(s, lang string, seps Separators) []Span {
	sd := scan(s, localeFor(lang).with(seps))
	byteAt := make([]int, len(sd.t.orig)+1) // rune index → byte offset in s
	for i, r := range sd.t.orig {
		byteAt[i+1] = byteAt[i] + len(string(r))
	}
	span := func(kind string, start, end int) Span {
		return Span{Kind: kind, Start: byteAt[sd.t.runeAt[start]], End: byteAt[sd.t.runeAt[end]]}
	}
	var out []Span
	for _, c := range sd.dates {
		out = append(out, span(SpanDate, c.start, c.end))
	}
	for _, c := range sd.times {
		out = append(out, span(SpanTime, c.start, c.end))
	}
	for _, c := range sd.qties {
		out = append(out, span(SpanMeasurement, c.start, c.end))
	}
	for _, n := range sd.numbers {
		out = append(out, span(SpanNumber, n.start, n.end))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}
