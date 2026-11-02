package segmentation

import (
	"errors"
	"io"
	"unicode"
	"unicode/utf8"
)

// TextRange is a half-open byte range [Start, End) within a string.
type TextRange struct {
	Start int
	End   int
}

// Decision is how a position was decided: Pos is a byte offset into the text,
// Rule the index of the deciding rule (-1 for a word list), Word the
// abbreviation that matched.
type Decision struct {
	Pos    int            `json:"pos"`
	Break  bool           `json:"break"`
	Source DecisionSource `json:"source"`
	Rule   int            `json:"rule"`
	Word   string         `json:"word,omitempty"`
}

// Segmenter is a compiled rule set. It is safe for concurrent use.
type Segmenter struct {
	mode    Mode
	rules   []compiledRule
	abbr    *wordList
	numAbbr *wordList
	// byASCII lists, per ASCII character before a position, the rules (in
	// order) whose before pattern can end with it; nonASCII lists the rules
	// that can end with any other character.
	byASCII  [utf8.RuneSelf][]int
	nonASCII []int
}

type compiledRule struct {
	before, after *sideMatcher
	brk           bool
}

// Compile validates rs and compiles it.
func Compile(rs RuleSet) (*Segmenter, error) {
	rs = Normalize(rs)
	if err := Validate(rs); err != nil {
		return nil, err
	}
	s := &Segmenter{
		mode:    rs.Mode,
		abbr:    newWordList(rs.Abbreviations),
		numAbbr: newWordList(rs.NumberAbbreviations),
	}
	for i, r := range rs.Rules {
		before, err := compileSide(r.Before, true)
		if err != nil {
			return nil, invalid("Rule %d: the before pattern is not valid: %s.", i+1, PatternProblem(r.Before, err))
		}
		after, err := compileSide(r.After, false)
		if err != nil {
			return nil, invalid("Rule %d: the after pattern is not valid: %s.", i+1, PatternProblem(r.After, err))
		}
		s.rules = append(s.rules, compiledRule{before: before, after: after, brk: r.Break})
		var first *runeSet
		if before != nil {
			first = before.first
		}
		for c := rune(0); c < utf8.RuneSelf; c++ {
			if first.contains(c) {
				s.byASCII[c] = append(s.byASCII[c], i)
			}
		}
		if first.hasNonASCII() {
			s.nonASCII = append(s.nonASCII, i)
		}
	}
	return s, nil
}

// MustCompile is Compile for rule sets known to be valid (the built-ins).
func MustCompile(rs RuleSet) *Segmenter {
	s, err := Compile(rs)
	if err != nil {
		panic(err)
	}
	return s
}

// Split returns the byte ranges of the segments of text. Ranges exclude the
// whitespace between segments and at either end, so a caller can splice
// translations back without disturbing the source's spacing.
//
// canBreak, when non-nil, vetoes a break at a byte offset; parsers use it to
// keep inline elements and placeholders whole.
//
// Returns one range for the trimmed text when nothing breaks, and nil when the
// text has no non-whitespace content. The error is ErrTooCostly.
func (s *Segmenter) Split(text string, canBreak func(pos int) bool) ([]TextRange, error) {
	if skipSpaceForward(text, 0) >= len(text) {
		return nil, nil
	}
	var breaks []Decision
	if s.mode == ModeSentence {
		err := s.scan(text, func(d Decision) {
			if d.Break && (canBreak == nil || canBreak(d.Pos)) {
				breaks = append(breaks, d)
			}
		})
		if err != nil {
			return nil, err
		}
	}
	return Ranges(text, breaks), nil
}

// Ranges cuts text at every break decision of ds (in order) and returns the
// segments, trimmed of whitespace as Split's are.
func Ranges(text string, ds []Decision) []TextRange {
	start := skipSpaceForward(text, 0)
	if start >= len(text) {
		return nil
	}
	var ranges []TextRange
	for _, d := range ds {
		if !d.Break {
			continue
		}
		if end := trimSpaceBackward(text, start, d.Pos); end > start {
			ranges = append(ranges, TextRange{Start: start, End: end})
		}
		start = skipSpaceForward(text, d.Pos)
	}
	if end := trimSpaceBackward(text, start, len(text)); end > start {
		ranges = append(ranges, TextRange{Start: start, End: end})
	}
	return ranges
}

// Explain returns every position of text that a word list or a rule decides,
// in order, for the preview. A paragraph-mode set decides none. The error is
// ErrTooCostly.
func (s *Segmenter) Explain(text string) ([]Decision, error) {
	var out []Decision
	if s.mode == ModeSentence {
		if err := s.scan(text, func(d Decision) { out = append(out, d) }); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ErrTooCostly is a split whose patterns cost more than the work budget
// allows (CostPerByte and CostPerRule per byte of the text, plus
// CostAllowance): a rule set whose patterns read most of their window at
// nearly every position.
var ErrTooCostly = errors.New("segmentation: the rules take too long to run on this text")

// position is a position between two characters, with its neighbours.
type position struct {
	p                  int
	prev, next         rune
	prevSize, nextSize int
	// spaceBefore is the length of the whitespace run that ends at p when the
	// character at p is not whitespace, else 0. spaceAfter is the length of
	// the run that starts at p when the character before p is not
	// whitespace, else 0; -1 until measured. The context window starts
	// beyond them, and each run is walked at its two edges only, so a long
	// run costs linear time.
	spaceBefore, spaceAfter int
}

// scan calls emit for every decided position strictly inside text, in order.
// It stops with ErrTooCostly once the patterns have read more than the budget
// allows; the count depends on the rules, the text and the regexp engine,
// never on timing, so a re-parse of the same file meets the same outcome.
func (s *Segmenter) scan(raw string, emit func(Decision)) error {
	if len(raw) < 2 || (s.abbr == nil && s.numAbbr == nil && len(s.rules) == 0) {
		return nil
	}
	t := newTextView(raw)
	t.budget = (CostPerByte+CostPerRule*len(s.rules))*len(raw) + CostAllowance
	prev, prevSize := utf8.DecodeRuneInString(t.text)
	runStart := 0 // where the whitespace run that ends at p starts
	if !unicode.IsSpace(prev) {
		runStart = prevSize
	}
	for p := prevSize; p < len(t.text); {
		next, nextSize := utf8.DecodeRuneInString(t.text[p:])
		pos := position{p: p, prev: prev, next: next, prevSize: prevSize, nextSize: nextSize, spaceAfter: -1}
		nextSpace := unicode.IsSpace(next)
		if !nextSpace {
			pos.spaceBefore = p - runStart
		}
		if d, ok := s.decide(t, &pos); ok {
			emit(d)
		}
		if t.cost > t.budget {
			return ErrTooCostly
		}
		prev, prevSize = next, nextSize
		p += nextSize
		if !nextSpace {
			runStart = p
		}
	}
	return nil
}

func (s *Segmenter) decide(t *textView, pos *position) (Decision, bool) {
	p, prev, next := pos.p, pos.prev, pos.next
	if prev == '.' && unicode.IsSpace(next) {
		if w := s.abbr.endsAt(t.text, p-1); w != "" {
			return Decision{Pos: p, Source: SourceAbbreviation, Rule: -1, Word: w}, true
		}
		if s.numAbbr != nil && startsWithNumber(t.text[p:]) {
			if w := s.numAbbr.endsAt(t.text, p-1); w != "" {
				return Decision{Pos: p, Source: SourceNumberAbbreviation, Rule: -1, Word: w}, true
			}
		}
	}
	candidates := s.nonASCII
	if prev < utf8.RuneSelf {
		candidates = s.byASCII[prev]
	}
	for _, i := range candidates {
		r := &s.rules[i]
		if r.before != nil && !r.before.first.contains(prev) {
			continue
		}
		if r.after != nil && !r.after.first.contains(next) {
			continue
		}
		if !t.beforeMatches(r.before, pos) || !t.afterMatches(r.after, pos) {
			if t.cost > t.budget {
				return Decision{}, false
			}
			continue
		}
		return Decision{Pos: p, Break: r.brk, Source: SourceRule, Rule: i}, true
	}
	return Decision{}, false
}

// startsWithNumber reports whether s is whitespace followed by a digit.
func startsWithNumber(s string) bool {
	i := skipSpaceForward(s, 0)
	if i == 0 || i >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return unicode.IsDigit(r)
}

// textView is a block's text with invalid UTF-8 bytes replaced by 0xFF (one
// byte each, so offsets do not move and no two bytes can join into a valid
// character when reversed), and its reverse, built on first use.
type textView struct {
	text string
	rev  string
	// cost is what the patterns have read so far, and budget what they may
	// read before the split fails.
	cost, budget int
	rd           countingReader
}

// attemptCost is what one match attempt costs besides the bytes it reads.
const attemptCost = 8

// countingReader reads a string rune by rune and counts the bytes read, so a
// match is billed for what it looks at: an anchored attempt that fails on its
// first character costs that character, not its window.
type countingReader struct {
	s string
	n int
}

func (r *countingReader) ReadRune() (rune, int, error) {
	if r.n >= len(r.s) {
		return 0, 0, io.EOF
	}
	c, size := utf8.DecodeRuneInString(r.s[r.n:])
	r.n += size
	return c, size, nil
}

// match runs m's full or cut pattern on s and adds what it read, plus
// attemptCost for the call itself, times the pattern's weight, to the cost.
func (t *textView) match(m *sideMatcher, full bool, s string) bool {
	re := m.cut
	if full {
		re = m.full
	}
	t.rd = countingReader{s: s}
	ok := re.MatchReader(&t.rd)
	t.cost += (t.rd.n + attemptCost) * m.weight
	return ok
}

func newTextView(raw string) *textView {
	if utf8.ValidString(raw) {
		return &textView{text: raw}
	}
	b := []byte(raw)
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			b[i] = 0xFF
		}
		i += size
	}
	return &textView{text: string(b)}
}

func (t *textView) reversed() string {
	if t.rev == "" {
		b := make([]byte, len(t.text))
		n := len(t.text)
		for i := 0; i < n; {
			_, size := utf8.DecodeRuneInString(t.text[i:])
			copy(b[n-i-size:], t.text[i:i+size])
			i += size
		}
		t.rev = string(b)
	}
	return t.rev
}

// beforeMatches matches m against the text ending at pos, seen backwards from
// the character at pos.
func (t *textView) beforeMatches(m *sideMatcher, pos *position) bool {
	if m == nil {
		return true
	}
	rev := t.reversed()
	n := len(rev)
	lo, hi := n-pos.p-pos.nextSize, n-pos.p+pos.spaceBefore+ContextWindow
	if hi >= n {
		return t.match(m, true, rev[lo:])
	}
	return t.match(m, false, rev[lo:hi])
}

// afterMatches matches m against the text starting at pos, seen from the
// character before pos.
func (t *textView) afterMatches(m *sideMatcher, pos *position) bool {
	if m == nil {
		return true
	}
	if pos.spaceAfter < 0 {
		pos.spaceAfter = 0
		if !unicode.IsSpace(pos.prev) {
			pos.spaceAfter = skipSpaceForward(t.text, pos.p) - pos.p
		}
	}
	lo, hi := pos.p-pos.prevSize, pos.p+pos.spaceAfter+ContextWindow
	if hi >= len(t.text) {
		return t.match(m, true, t.text[lo:])
	}
	return t.match(m, false, t.text[lo:hi])
}

// skipSpaceForward returns the index of the first non-space byte at or after i.
func skipSpaceForward(s string, i int) int {
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return i
}

// trimSpaceBackward returns the index just past the last non-space byte in
// s[start:end], or start when the slice is all whitespace.
func trimSpaceBackward(s string, start, end int) int {
	for end > start {
		r, size := utf8.DecodeLastRuneInString(s[start:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	return end
}
