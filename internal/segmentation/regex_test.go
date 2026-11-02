package segmentation

import (
	"regexp"
	"regexp/syntax"
	"testing"
	"unicode/utf8"
)

// TestSideMatchers_MatchLikeAWindow checks the reversed before matcher and the
// forward after matcher against plain regexp matching on the text before and
// after each position. The patterns avoid \b at the break and ^ inside an
// after pattern, where the matchers see more context than a plain slice.
func TestSideMatchers_MatchLikeAWindow(t *testing.T) {
	patterns := []string{
		`\.`, `[.!?]+`, `(?i)dr\.`, `\bDr\.`, `^Note:`, `(?:^|\P{L})\p{Ll}\.`,
		`[a-z]{2,4}\.`, `(?:etc|e\.g)\.`, `\d+\.\s*`, `x*`, `.`, `(ab|cd)+e`,
		`[\p{Lu}][^.]*\.`, `(?m)^-`, `\s+`, `é\.`, `(?s).\n`, `\x{3002}`,
	}
	texts := []string{
		"Ask Dr. Who. etc. And e.g. this!",
		"Note: one. 12. ab cdabcde. éé. x.",
		"line one\n- item\n- item two.",
		"今日は晴れ。明日。",
		"", "a", ".", "...!!",
	}
	for _, pat := range patterns {
		before, err := compileSide(pat, true)
		if err != nil {
			t.Fatalf("%s: %v", pat, err)
		}
		after, err := compileSide(pat, false)
		if err != nil {
			t.Fatalf("%s: %v", pat, err)
		}
		endsAt := regexp.MustCompile(`(?:` + pat + `)$`)
		startsAt := regexp.MustCompile(`^(?:` + pat + `)`)
		for _, text := range texts {
			tv := newTextView(text)
			for p := 1; p < len(text); p++ {
				if !utf8.RuneStart(text[p]) {
					continue
				}
				_, nextSize := utf8.DecodeRuneInString(text[p:])
				_, prevSize := utf8.DecodeLastRuneInString(text[:p])
				next, _ := utf8.DecodeRuneInString(text[p:])
				prev, _ := utf8.DecodeLastRuneInString(text[:p])

				want := endsAt.MatchString(text[:p])
				pos := &position{p: p, prev: prev, next: next, prevSize: prevSize, nextSize: nextSize, spaceAfter: -1}
				got := before.first.contains(prev) && tv.beforeMatches(before, pos)
				if got != want {
					t.Errorf("before %s at %d of %q: got %v, want %v", pat, p, text, got, want)
				}
				if pat == `^Note:` || pat == `(?m)^-` || pat == `(?:^|\P{L})\p{Ll}\.` || pat == `\bDr\.` {
					continue
				}
				want = startsAt.MatchString(text[p:])
				got = after.first.contains(next) && tv.afterMatches(after, pos)
				if got != want {
					t.Errorf("after %s at %d of %q: got %v, want %v", pat, p, text, got, want)
				}
			}
		}
	}
}

func TestRuneSet(t *testing.T) {
	s := (&runeSet{ranges: []rune{'m', 'p', 'a', 'c', 'b', 'e', 0x3000, 0x3002}}).normalized()
	if want := []rune{'a', 'e', 'm', 'p', 0x3000, 0x3002}; !equalRunes(s.ranges, want) {
		t.Fatalf("normalized = %q, want %q", s.ranges, want)
	}
	for r, want := range map[rune]bool{'a': true, 'e': true, 'f': false, 'n': true, 'q': false, 0x3001: true, 0x3003: false} {
		if s.contains(r) != want {
			t.Errorf("contains(%q) = %v", r, !want)
		}
	}
	if !s.hasNonASCII() || (&runeSet{any: true}).normalized() != nil {
		t.Error("hasNonASCII or any is wrong")
	}
}

func equalRunes(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// programSize never counts less than the pattern compiles to, so
// MaxPatternSize bounds the compiled program.
func TestProgramSize_NeverBelowTheCompiledSize(t *testing.T) {
	patterns := []string{
		`.{600,}`, `(?:.{0,20}){0,20}`, `(a|b{0,300}){2}`, `(x{0,200})+`, `(?:a{0}){0,1000}`,
		`(?:(?:a{0}){0,30}){0,30}`, `(?:(?:a{3}){4}){5}`, `(a*)*`, `()*`, `(?:(a*)*){0,170}`, `(?:a?)*`, `(?:a*){2,}`, `(?:(a*)*){3,}`, `\b\d+\.`, `[.!?…][\p{Pe}\p{Pf}"']*`, `(?i)abc|d(e|f)*g?`,
	}
	for _, b := range Builtins() {
		for _, r := range b.RuleSet.Rules {
			patterns = append(patterns, r.Before, r.After)
		}
	}
	for _, p := range patterns {
		if p == "" {
			continue
		}
		re, err := syntax.Parse(p, syntax.Perl)
		if err != nil {
			t.Fatal(err)
		}
		prog, err := syntax.Compile(re.Simplify())
		if err != nil {
			t.Fatal(err)
		}
		// A compiled program also holds one fail and one match
		// instruction of its own.
		if got, body := programSize(re, 1<<20), len(prog.Inst)-2; got < body {
			t.Errorf("programSize(%q) = %d, below the compiled %d", p, got, body)
		}
	}

	// The limit sits between these two.
	for p, want := range map[string]bool{`(?s).{0,250}.{0,240}z`: true, `(?s).{0,270}.{0,260}z`: false} {
		re, err := syntax.Parse(p, syntax.Perl)
		if err != nil {
			t.Fatal(err)
		}
		if got := programSize(re, MaxPatternSize) <= MaxPatternSize; got != want {
			t.Errorf("%q within MaxPatternSize = %v, want %v", p, got, want)
		}
	}
}
