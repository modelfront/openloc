package numeric

import (
	"fmt"
	"sort"
	"strings"
)

// Rules are the enabled checks of the Numbers group, each with its severity;
// an empty severity turns a check off.
type Rules struct {
	Numbers, Format, Dates, Times, Units string
	// FormatRequire makes Format accept only the target language's
	// separators; otherwise the source number's are fine too.
	FormatRequire bool
	// SourceSeparators and TargetSeparators replace the language's own
	// separators when set: they decide how its numbers are read and which
	// separators the format check expects.
	SourceSeparators, TargetSeparators Separators
}

// Separators is how a language writes numbers: its decimal separator ('.' or
// ',') and its grouping separator (',', '.', a space, an apostrophe, or 0 for
// none). The zero value means the language's CLDR data.
type Separators struct {
	Decimal, Group rune
}

func (r Rules) on() bool {
	return r.Numbers != "" || r.Format != "" || r.Dates != "" || r.Times != "" || r.Units != ""
}

// Finding is one problem a check found in a segment.
type Finding struct {
	CheckType, Severity, Message string
}

// Run compares the numbers of a segment's source and target. Dates, times
// and quantities are compared as such when their check is on and both sides
// have one that can be told apart; everything else is compared as plain
// numbers, so a form the scanners do not know never raises more than the
// plain number check would.
func Run(source, target, sourceLang, targetLang string, r Rules) []Finding {
	if !r.on() || !hasAnyDigit(source+target) {
		return nil
	}
	srcLoc, tgtLoc := localeFor(sourceLang).with(r.SourceSeparators), localeFor(targetLang).with(r.TargetSeparators)
	src, tgt := scan(source, srcLoc), scan(target, tgtLoc)
	c := &run{src: src, tgt: tgt, srcNums: src.numbers, tgtNums: tgt.numbers}

	c.compare(src.dates, tgt.dates, r.Dates, datesMatch(src, tgt), "date_consistency", "Date changed in target")
	c.compare(src.times, tgt.times, r.Times, timesMatch(src, tgt), "time_consistency", "Time changed in target")
	c.quantities(r.Units)
	c.numbers(r.Numbers)
	if r.Format != "" {
		c.format(r.Format, srcLoc, tgtLoc, r.FormatRequire)
	}
	sort.SliceStable(c.out, func(i, j int) bool { return order[c.out[i].CheckType] < order[c.out[j].CheckType] })
	return c.out
}

var order = map[string]int{"number_consistency": 0, "number_added": 1, "number_format": 2, "date_consistency": 3, "time_consistency": 4, "unit_conversion": 5}

func hasAnyDigit(s string) bool {
	for _, r := range s {
		if normRune(r) >= '0' && normRune(r) <= '9' {
			return true
		}
	}
	return false
}

type run struct {
	src, tgt         *side
	srcNums, tgtNums []number
	pairs            []formatPair
	out              []Finding
}

// maxEntities caps the dates, times or quantities of one kind compared per
// side: pairing is quadratic, and a segment with more than this is a table,
// whose numbers the number check compares in linear time.
const maxEntities = 200

// compare pairs the dates (or times) of the two sides. With the check off,
// or for those left unmatched, their numbers go to the number check.
func (c *run) compare(src, tgt []candidate, severity string, match func(s, t candidate) bool, checkType, label string) {
	if severity == "" || len(src) > maxEntities || len(tgt) > maxEntities {
		c.decompose(src, nil, tgt, nil)
		return
	}
	p := pair(src, tgt, match)
	if m := p.mismatch; m[0] >= 0 {
		p.srcOK[m[0]], p.tgtOK[m[1]] = true, true
		c.add(checkType, severity, fmt.Sprintf("%s: %s → %s", label, c.src.text(src[m[0]]), c.tgt.text(tgt[m[1]])))
	}
	c.decompose(src, p.srcOK, tgt, p.tgtOK)
}

// decompose hands the numbers of the unmatched entities to the number check.
func (c *run) decompose(src []candidate, srcOK []bool, tgt []candidate, tgtOK []bool) {
	for i, e := range src {
		if srcOK == nil || !srcOK[i] {
			c.srcNums = append(c.srcNums, c.src.numbersIn(e)...)
		}
	}
	for j, e := range tgt {
		if tgtOK == nil || !tgtOK[j] {
			c.tgtNums = append(c.tgtNums, c.tgt.numbersIn(e)...)
		}
	}
}

func (c *run) add(checkType, severity, msg string) {
	c.out = append(c.out, Finding{CheckType: checkType, Severity: severity, Message: msg})
}

// quantities pairs the measurements: the same value in the same unit, or a
// conversion. A plain number on the other side may stand for a quantity
// whose unit is written as a word the check does not know ("8 Kilometer").
func (c *run) quantities(severity string) {
	src, tgt := c.src.qties, c.tgt.qties
	if severity == "" || len(src) > maxEntities || len(tgt) > maxEntities {
		c.decompose(src, nil, tgt, nil)
		return
	}
	p := pair(src, tgt, quantitiesMatch)
	for _, t := range tgt {
		for _, s := range src {
			if quantitiesMatch(s, t) {
				c.pairs = append(c.pairs, formatPair{src: s.qty.num, tgt: t.qty.num})
				break
			}
		}
	}
	tgtPlain, srcPlain := newPlainPool(c.tgtNums), newPlainPool(c.srcNums)
	for i, s := range src {
		if !p.srcOK[i] {
			p.srcOK[i] = tgtPlain.take(*s.qty)
		}
	}
	for j, t := range tgt {
		if !p.tgtOK[j] {
			p.tgtOK[j] = srcPlain.take(*t.qty)
		}
	}
	c.tgtNums, c.srcNums = tgtPlain.left(), srcPlain.left()
	m := loneMismatch(src, tgt, p, func(s, t candidate) bool {
		return s.qty.unit.dim == t.qty.unit.dim && s.qty.unit != t.qty.unit
	})
	if m[0] >= 0 {
		s, t := src[m[0]], tgt[m[1]]
		p.srcOK[m[0]], p.tgtOK[m[1]] = true, true
		c.add("unit_conversion", severity, fmt.Sprintf("Converted value does not match: %s → %s (about %s)",
			c.src.text(s), c.tgt.text(t), expected(*s.qty, t.qty.num, t.qty.unit)))
	}
	c.decompose(src, p.srcOK, tgt, p.tgtOK)
}

// convertBudget is how many plain numbers a quantity is converted against,
// at most: a side with more is a table, and its quantities fall back to the
// number check.
const convertBudget = 1000

// plainPool is the plain numbers of one side that a quantity of the other
// side may stand for: "5 Meilen" is "5 miles" with its unit as a word, "8
// Kilometer" is "5 miles" converted.
type plainPool struct {
	nums    []number
	used    []bool
	byValue map[string][]int
}

func newPlainPool(nums []number) *plainPool {
	p := &plainPool{nums: nums, used: make([]bool, len(nums)), byValue: map[string][]int{}}
	for k, n := range nums {
		for _, v := range n.values {
			p.byValue[v] = append(p.byValue[v], k)
		}
	}
	return p
}

// take marks as used a plain number that is q without its unit, or q
// converted to a common unit of the other system, and reports whether it
// found one.
func (p *plainPool) take(q quantity) bool {
	for _, v := range q.num.values {
		for _, k := range p.byValue[v] {
			if !p.used[k] {
				p.used[k] = true
				return true
			}
		}
	}
	if len(p.nums) > convertBudget {
		return false
	}
	for k, n := range p.nums {
		if p.used[k] {
			continue
		}
		for i := range units {
			u := &units[i]
			if u.dim == q.unit.dim && u.metric != q.unit.metric && u.commonOut && converts(q, n, u) {
				p.used[k] = true
				return true
			}
		}
	}
	return false
}

// left returns the plain numbers no quantity took.
func (p *plainPool) left() []number {
	out := make([]number, 0, len(p.nums))
	for k, n := range p.nums {
		if !p.used[k] {
			out = append(out, n)
		}
	}
	return out
}

// numbers compares the plain numbers: one missing from the target is a
// finding at the rule's severity, one only the target has is a warning.
func (c *run) numbers(severity string) {
	c.srcNums = splitUnmatched(c.src, c.srcNums, c.tgtNums)
	c.tgtNums = splitUnmatched(c.tgt, c.tgtNums, c.srcNums)
	sortNumbers(c.srcNums)
	sortNumbers(c.tgtNums)
	first := map[string]int{} // value → the first source number with it
	for i, s := range c.srcNums {
		for _, v := range s.values {
			if _, ok := first[v]; !ok {
				first[v] = i
			}
		}
	}
	for _, t := range c.tgtNums {
		for _, v := range t.values {
			if i, ok := first[v]; ok {
				c.pairs = append(c.pairs, formatPair{src: c.srcNums[i], tgt: t})
				break
			}
		}
	}
	if severity == "" {
		return
	}
	if missing := unmatched(c.srcNums, valueSet(c.tgtNums)); len(missing) > 0 {
		c.add("number_consistency", severity, "Missing number(s) in target: "+strings.Join(missing, ", "))
	}
	if extra := unmatched(c.tgtNums, valueSet(c.srcNums)); len(extra) > 0 {
		c.add("number_added", "warning", "Number(s) in target but not source: "+strings.Join(extra, ", "))
	}
}

// splitUnmatched replaces each number grouped with spaces that matches
// nothing on the other side by its parts: "48 123 456 789" may be a phone
// number the other language writes as four numbers.
func splitUnmatched(sd *side, nums, other []number) []number {
	values := valueSet(other)
	out := make([]number, 0, len(nums))
	for _, n := range nums {
		if n.spaced && !n.in(values) {
			out = append(out, sd.t.parts(n, sd.loc)...)
			continue
		}
		out = append(out, n)
	}
	return out
}

func valueSet(nums []number) map[string]bool {
	set := make(map[string]bool, 2*len(nums))
	for _, n := range nums {
		for _, v := range n.values {
			set[v] = true
		}
	}
	return set
}

// in reports a value of n in the set.
func (n number) in(values map[string]bool) bool {
	for _, v := range n.values {
		if values[v] {
			return true
		}
	}
	return false
}

func sortNumbers(nums []number) {
	sort.SliceStable(nums, func(i, j int) bool { return nums[i].start < nums[j].start })
}

// unmatched lists, once each, the numbers in nums with no value in other.
func unmatched(nums []number, other map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range nums {
		if !n.in(other) && !seen[n.values[0]] {
			out = append(out, n.text)
			seen[n.values[0]] = true
		}
	}
	return out
}

func (c *run) format(severity string, srcLoc, tgtLoc locale, require bool) {
	var bad []string
	seen := map[string]bool{}
	sort.SliceStable(c.pairs, func(i, j int) bool { return c.pairs[i].tgt.start < c.pairs[j].tgt.start })
	for _, p := range c.pairs {
		if seen[p.tgt.text] || !badFormat(p, srcLoc, tgtLoc, require) {
			continue
		}
		seen[p.tgt.text] = true
		bad = append(bad, fmt.Sprintf("%s (expected %s)", strings.TrimRight(p.tgt.text, ".,"), localized(roleReading(p), tgtLoc)))
	}
	if len(bad) > 0 {
		c.add("number_format", severity, "Number(s) not in the target language's format: "+strings.Join(bad, ", "))
	}
}
