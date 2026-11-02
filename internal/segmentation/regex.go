package segmentation

import (
	"regexp"
	"regexp/syntax"
	"sort"
	"unicode"
)

// A before pattern must match text that ends at a position. RE2 has no
// look-behind, so the pattern is reversed and matched, anchored, against the
// reversed text from that position backwards. An after pattern is matched,
// anchored, forwards. Both are prefixed with one character of the other side,
// so \b at the position sees both of its neighbours.

// sideMatcher matches one side of a rule. full is used when the context window
// reaches the block edge; cut when it does not, with the anchors that would
// fire at the cut-off end replaced by "never".
type sideMatcher struct {
	full, cut *regexp.Regexp
	// first is the set of characters that can sit next to the position on
	// this side; nil means any.
	first *runeSet
	// weight is what one byte read costs: the compiled program's size in
	// units of instUnit instructions, at least 1. The regexp engine's work
	// per byte grows with the program, so a pattern with large counted
	// repeats pays for it.
	weight int
}

// instUnit is the program size that costs 1 per byte read. The built-ins'
// patterns are all smaller.
const instUnit = 64

// compileSide builds the matcher for pattern; reverse is true for a before
// pattern. An empty pattern returns nil (matches everywhere).
func compileSide(pattern string, reverse bool) (*sideMatcher, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	if reverse {
		re = reverseRegexp(re)
	}
	set, nullable := firstRunes(re)
	m := &sideMatcher{}
	if !nullable {
		m.first = set.normalized()
	}
	if m.full, err = regexp.Compile(anchored(re).String()); err != nil {
		return nil, err
	}
	prog, err := syntax.Compile(anchored(re).Simplify())
	if err != nil {
		return nil, err
	}
	m.weight = max(1, (len(prog.Inst)+instUnit-1)/instUnit)
	if m.cut, err = regexp.Compile(anchored(withoutEndAnchors(re)).String()); err != nil {
		return nil, err
	}
	return m, nil
}

// programSize estimates how many instructions re compiles to, counting each
// counted repeat as its copies, without compiling it. It stops counting past
// limit.
func programSize(re *syntax.Regexp, limit int) int {
	n := 0
	switch re.Op {
	case syntax.OpLiteral:
		return len(re.Rune)
	case syntax.OpCapture:
		return programSize(re.Sub[0], limit) + 2
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest:
		// A star over a part that can match nothing compiles to two
		// branches; count two for all of them.
		return programSize(re.Sub[0], limit) + 2
	case syntax.OpRepeat:
		copies, star := re.Max, 0
		if copies < 0 {
			// x{n,} is n copies and a star.
			copies, star = re.Min+1, 1
		}
		// Each copy compiles to at least one instruction, even of an empty
		// part.
		sub := max(programSize(re.Sub[0], limit), 1)
		if copies > limit/sub {
			return limit + 1
		}
		return max(sub*copies+copies, 1) + star
	case syntax.OpConcat, syntax.OpAlternate:
		for _, sub := range re.Sub {
			if n += programSize(sub, limit) + 1; n > limit {
				return n
			}
		}
		return n
	}
	return 1
}

// anchored is ^(?s:.)re: the one character of context, then the pattern.
func anchored(re *syntax.Regexp) *syntax.Regexp {
	return &syntax.Regexp{Op: syntax.OpConcat, Sub: []*syntax.Regexp{
		{Op: syntax.OpBeginText},
		{Op: syntax.OpAnyChar},
		re,
	}}
}

// reverseRegexp returns a pattern that matches the reverse of every string re
// matches.
func reverseRegexp(re *syntax.Regexp) *syntax.Regexp {
	out := *re
	out.Sub = make([]*syntax.Regexp, len(re.Sub))
	for i, sub := range re.Sub {
		out.Sub[i] = reverseRegexp(sub)
	}
	out.Sub0 = [1]*syntax.Regexp{}
	switch re.Op {
	case syntax.OpConcat:
		for i, j := 0, len(out.Sub)-1; i < j; i, j = i+1, j-1 {
			out.Sub[i], out.Sub[j] = out.Sub[j], out.Sub[i]
		}
	case syntax.OpLiteral:
		out.Rune = make([]rune, len(re.Rune))
		for i, r := range re.Rune {
			out.Rune[len(re.Rune)-1-i] = r
		}
	case syntax.OpBeginText:
		out.Op = syntax.OpEndText
	case syntax.OpEndText:
		out.Op = syntax.OpBeginText
	case syntax.OpBeginLine:
		out.Op = syntax.OpEndLine
	case syntax.OpEndLine:
		out.Op = syntax.OpBeginLine
	}
	return &out
}

// withoutEndAnchors replaces $ (end of text or line) with a pattern that
// never matches, for a window that stops before the block edge.
func withoutEndAnchors(re *syntax.Regexp) *syntax.Regexp {
	if re.Op == syntax.OpEndText || re.Op == syntax.OpEndLine {
		return &syntax.Regexp{Op: syntax.OpNoMatch}
	}
	out := *re
	out.Sub = make([]*syntax.Regexp, len(re.Sub))
	for i, sub := range re.Sub {
		out.Sub[i] = withoutEndAnchors(sub)
	}
	out.Sub0 = [1]*syntax.Regexp{}
	return &out
}

// firstRunes returns the characters a match of re can start with, and whether
// re can match the empty string (then any character may follow).
func firstRunes(re *syntax.Regexp) (*runeSet, bool) {
	switch re.Op {
	case syntax.OpNoMatch:
		return &runeSet{}, false
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText,
		syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return &runeSet{}, true
	case syntax.OpLiteral:
		if len(re.Rune) == 0 {
			return &runeSet{}, true
		}
		set := &runeSet{}
		r := re.Rune[0]
		set.add(r, r)
		if re.Flags&syntax.FoldCase != 0 {
			for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
				set.add(f, f)
			}
		}
		return set, false
	case syntax.OpCharClass:
		set := &runeSet{}
		for i := 0; i+1 < len(re.Rune); i += 2 {
			set.add(re.Rune[i], re.Rune[i+1])
		}
		return set, false
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return &runeSet{any: true}, false
	case syntax.OpCapture, syntax.OpPlus:
		return firstRunes(re.Sub[0])
	case syntax.OpStar, syntax.OpQuest:
		set, _ := firstRunes(re.Sub[0])
		return set, true
	case syntax.OpRepeat:
		set, nullable := firstRunes(re.Sub[0])
		return set, nullable || re.Min == 0
	case syntax.OpConcat:
		set := &runeSet{}
		for _, sub := range re.Sub {
			s, nullable := firstRunes(sub)
			set.union(s)
			if !nullable {
				return set, false
			}
		}
		return set, true
	case syntax.OpAlternate:
		set := &runeSet{}
		nullable := false
		for _, sub := range re.Sub {
			s, n := firstRunes(sub)
			set.union(s)
			nullable = nullable || n
		}
		return set, nullable
	}
	return &runeSet{any: true}, true
}

// runeSet is a set of characters as sorted, non-overlapping [lo, hi] pairs,
// or every character when any is set.
type runeSet struct {
	any    bool
	ranges []rune
}

func (s *runeSet) add(lo, hi rune) { s.ranges = append(s.ranges, lo, hi) }

func (s *runeSet) union(o *runeSet) {
	s.any = s.any || o.any
	s.ranges = append(s.ranges, o.ranges...)
}

// normalized sorts and merges the ranges; nil when the set holds every
// character.
func (s *runeSet) normalized() *runeSet {
	if s.any {
		return nil
	}
	type pair struct{ lo, hi rune }
	pairs := make([]pair, 0, len(s.ranges)/2)
	for i := 0; i+1 < len(s.ranges); i += 2 {
		pairs = append(pairs, pair{s.ranges[i], s.ranges[i+1]})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].lo < pairs[j].lo })
	out := &runeSet{}
	for _, p := range pairs {
		n := len(out.ranges)
		if n > 0 && p.lo <= out.ranges[n-1]+1 {
			if p.hi > out.ranges[n-1] {
				out.ranges[n-1] = p.hi
			}
			continue
		}
		out.ranges = append(out.ranges, p.lo, p.hi)
	}
	return out
}

// contains reports whether r is in a normalized set; a nil set holds every
// character.
func (s *runeSet) contains(r rune) bool {
	if s == nil {
		return true
	}
	n := len(s.ranges) / 2
	i := sort.Search(n, func(i int) bool { return s.ranges[2*i+1] >= r })
	return i < n && s.ranges[2*i] <= r
}

// hasNonASCII reports whether a normalized set holds a character above 0x7F.
func (s *runeSet) hasNonASCII() bool {
	return s == nil || (len(s.ranges) > 0 && s.ranges[len(s.ranges)-1] > unicode.MaxASCII)
}
