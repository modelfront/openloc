package numeric

import (
	"strings"
)

// formatPair is a target number and the source number it stands for.
type formatPair struct{ src, tgt number }

// roleReading is the value whose separators the format check grades: a value
// both numbers share; for a number copied unchanged, the source's reading;
// else the target's own.
func roleReading(p formatPair) string {
	copied := false
	for _, a := range p.src.values {
		for _, b := range p.tgt.values {
			if a != b {
				continue
			}
			if !strings.HasPrefix(a, "=") {
				return a
			}
			copied = true
		}
	}
	if copied {
		return p.src.values[0]
	}
	return p.tgt.values[0]
}

// separators splits a number into its decimal separator (0 when it has none)
// and its grouping separators, read as value v. ok is false for numbers whose
// groups are not three digits (versions, Indian grouping): those are not
// graded.
func separators(n number, v string) (dec rune, groups []rune, ok bool) {
	tok := strings.TrimLeft(strings.TrimRight(n.norm, ".,"), "-")
	rs := []rune(tok)
	last := -1
	if strings.Contains(v, ".") {
		for i := len(rs) - 1; i >= 0; i-- {
			if rs[i] == '.' || rs[i] == ',' {
				last = i
				break
			}
		}
	}
	intEnd := len(rs)
	if last >= 0 {
		dec, intEnd = rs[last], last
	}
	run := 0
	first := true
	for i := 0; i < intEnd; i++ {
		if rs[i] >= '0' && rs[i] <= '9' {
			run++
			continue
		}
		if (first && (run < 1 || run > 3)) || (!first && run != 3) {
			return 0, nil, false
		}
		groups = append(groups, rs[i])
		first, run = false, 0
	}
	if len(groups) > 0 && run != 3 {
		return 0, nil, false
	}
	return dec, groups, true
}

// sameGroup reports two grouping separators of one kind: any space, or any
// apostrophe.
func sameGroup(a, b rune) bool {
	return a == b || (a == ' ' || a == nbsp) && (b == ' ' || b == nbsp)
}

// badFormat reports a target number written with a separator the target
// language does not use — nor, unless require is set, the source number.
func badFormat(p formatPair, srcLoc, tgtLoc locale, require bool) bool {
	if !tgtLoc.known || tgtLoc.decimal == 0 {
		return false
	}
	v := roleReading(p)
	dec, groups, ok := separators(p.tgt, v)
	if !ok || dec == 0 && len(groups) == 0 {
		return false
	}
	okDec := []rune{tgtLoc.decimal}
	okGroup := []rune{normRune(tgtLoc.group)}
	if !require {
		if sdec, sgroups, ok := separators(p.src, roleReading(formatPair{src: p.src, tgt: p.src})); ok {
			if sdec != 0 {
				okDec = append(okDec, sdec)
			}
			okGroup = append(okGroup, sgroups...)
		}
	}
	if dec != 0 && !containsRune(okDec, dec, func(a, b rune) bool { return a == b }) {
		return true
	}
	for _, g := range groups {
		if !containsRune(okGroup, g, sameGroup) {
			return true
		}
	}
	return false
}

func containsRune(list []rune, r rune, eq func(a, b rune) bool) bool {
	for _, x := range list {
		if eq(x, r) {
			return true
		}
	}
	return false
}

// localized writes value v the target language's way.
func localized(v string, loc locale) string {
	sign := ""
	if strings.HasPrefix(v, "-") {
		sign, v = "-", v[1:]
	}
	intPart, frac, _ := strings.Cut(v, ".")
	if loc.group != 0 && len(intPart) > 3 && !(loc.minGrouping == 2 && len(intPart) == 4) {
		var b strings.Builder
		for i, d := range intPart {
			if i > 0 && (len(intPart)-i)%3 == 0 {
				b.WriteRune(loc.group)
			}
			b.WriteRune(d)
		}
		intPart = b.String()
	}
	if frac != "" {
		return sign + intPart + string(loc.decimal) + frac
	}
	return sign + intPart
}
