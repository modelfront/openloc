package numeric

import "sort"

type kind int

const (
	kindDate kind = iota
	kindTime
	kindQuantity
)

// candidate is a date, time or quantity found in the text. A weak one ("3/4",
// "14.3.", "15 h") is only taken as such when it matches the other side;
// otherwise its numbers are plain numbers.
type candidate struct {
	kind       kind
	start, end int
	weak       bool
	dates      []ymd // every date it can be
	minutes    []int // every time it can be, in minutes since midnight
	qty        *quantity
}

// side is one side of the segment, scanned: its dates, times and
// quantities, and the plain numbers outside them.
type side struct {
	t       *text
	loc     locale
	dates   []candidate
	times   []candidate
	qties   []candidate
	numbers []number
}

func scan(s string, loc locale) *side {
	t := newText(s)
	t.excludeSpans()
	var all []candidate
	all = append(all, t.dateCandidates(loc)...)
	all = append(all, t.timeCandidates(loc)...)
	all = append(all, t.quantityCandidates(loc)...)

	// Leftmost-longest: a candidate is kept unless it overlaps one that starts
	// earlier, or starts at the same place and is longer.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].start != all[j].start {
			return all[i].start < all[j].start
		}
		if all[i].end != all[j].end {
			return all[i].end > all[j].end
		}
		return all[i].kind < all[j].kind
	})
	sd := &side{t: t, loc: loc}
	covered := 0
	gapStart := 0
	for _, c := range all {
		if c.start < covered {
			continue
		}
		sd.numbers = append(sd.numbers, t.scanNumbers(gapStart, c.start, loc)...)
		switch c.kind {
		case kindDate:
			sd.dates = append(sd.dates, c)
		case kindTime:
			sd.times = append(sd.times, c)
		case kindQuantity:
			sd.qties = append(sd.qties, c)
		}
		covered, gapStart = c.end, c.end
	}
	sd.numbers = append(sd.numbers, t.scanNumbers(gapStart, len(t.norm), loc)...)
	return sd
}

// numbersIn returns the plain numbers of a candidate's span: what the
// number checks see when it is not compared as a date, time or quantity.
func (sd *side) numbersIn(c candidate) []number {
	return sd.t.scanNumbers(c.start, c.end, sd.loc)
}

func (sd *side) text(c candidate) string { return sd.t.original(c.start, c.end) }
