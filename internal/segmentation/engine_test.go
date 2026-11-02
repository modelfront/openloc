package segmentation

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const ws = `[\t\n\v\f\r\x{85}\p{Z}]`

// breakAfterPeriod is the minimal break rule most tests build on.
var breakAfterPeriod = Rule{Before: `[.!?]`, After: ws + `+\S`, Break: true}

func segments(t *testing.T, s *Segmenter, text string, canBreak func(int) bool) []string {
	t.Helper()
	var out []string
	ranges, err := s.Split(text, canBreak)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ranges {
		out = append(out, text[r.Start:r.End])
	}
	return out
}

func mustCompile(t *testing.T, rs RuleSet) *Segmenter {
	t.Helper()
	s, err := Compile(rs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return s
}

func assertSegments(t *testing.T, s *Segmenter, text string, want ...string) {
	t.Helper()
	if got := segments(t, s, text, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("split %q\n got %q\nwant %q", text, got, want)
	}
}

func TestSplit_FirstMatchingRuleDecides(t *testing.T) {
	noBreakDr := Rule{Before: `\bDr\.`, After: ws}
	text := "Ask Dr. Who. Then go."

	s := mustCompile(t, RuleSet{Rules: []Rule{noBreakDr, breakAfterPeriod}})
	assertSegments(t, s, text, "Ask Dr. Who.", "Then go.")

	// The same rules the other way round: the break rule matches first.
	s = mustCompile(t, RuleSet{Rules: []Rule{breakAfterPeriod, noBreakDr}})
	assertSegments(t, s, text, "Ask Dr.", "Who.", "Then go.")
}

func TestSplit_NoRuleMeansNoBreak(t *testing.T) {
	s := mustCompile(t, RuleSet{Rules: []Rule{{Before: `!`, After: ws, Break: true}}})
	assertSegments(t, s, "One. Two! Three.", "One. Two!", "Three.")
	assertSegments(t, mustCompile(t, RuleSet{}), "One. Two.", "One. Two.")
}

func TestSplit_AbbreviationList(t *testing.T) {
	s := mustCompile(t, RuleSet{Rules: []Rule{breakAfterPeriod}, Abbreviations: []string{"Dr", "e.g"}})
	tests := []struct {
		name, text string
		want       []string
	}{
		{"listed word", "Ask Dr. Who now.", []string{"Ask Dr. Who now."}},
		{"case-sensitive", "Ask dr. Who now.", []string{"Ask dr.", "Who now."}},
		{"whole word only", "Ask XDr. Who now.", []string{"Ask XDr.", "Who now."}},
		{"after a non-letter", "(Dr. Who) came.", []string{"(Dr. Who) came."}},
		{"at block start", "Dr. Who came.", []string{"Dr. Who came."}},
		{"dotted word", "Use e.g. Go here.", []string{"Use e.g. Go here."}},
		{"needs whitespace after", "Dr.Who. Next.", []string{"Dr.Who.", "Next."}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := segments(t, s, tc.text, nil); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplit_NumberAbbreviationList(t *testing.T) {
	s := mustCompile(t, RuleSet{Rules: []Rule{breakAfterPeriod}, NumberAbbreviations: []string{"p", "no"}})
	assertSegments(t, s, "See p. 12 for more.", "See p. 12 for more.")
	assertSegments(t, s, "Room no. 5 is free.", "Room no. 5 is free.")
	// Not before a number: an ordinary sentence end.
	assertSegments(t, s, "The answer was a big no. Then we left.", "The answer was a big no.", "Then we left.")
}

func TestSplit_ParagraphMode(t *testing.T) {
	s := mustCompile(t, RuleSet{Mode: ModeParagraph, Rules: []Rule{breakAfterPeriod}})
	assertSegments(t, s, "  One. Two. Three.  ", "One. Two. Three.")
	if d, err := s.Explain("One. Two."); err != nil || len(d) != 0 {
		t.Errorf("paragraph mode explains %v, want nothing", d)
	}
}

func TestSplit_FullWidthWithoutSpaces(t *testing.T) {
	b, _ := BuiltinByID(DefaultID)
	s := MustCompile(b.RuleSet)
	assertSegments(t, s, "今日は晴れ。明日は雨？「本当！」と言った。", "今日は晴れ。", "明日は雨？", "「本当！」と言った。")
}

func TestSplit_CanBreakVeto(t *testing.T) {
	s := mustCompile(t, RuleSet{Rules: []Rule{breakAfterPeriod}})
	text := "One. Two. Three."
	got := segments(t, s, text, func(p int) bool { return p != 4 })
	if want := []string{"One. Two.", "Three."}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSplit_RangesAreTight(t *testing.T) {
	s := mustCompile(t, RuleSet{Rules: []Rule{breakAfterPeriod, {Before: `\n`, After: ``, Break: true}}})
	text := " \t One.   Two.\n\n Three. \n"
	ranges, err := s.Split(text, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ranges {
		seg := text[r.Start:r.End]
		if seg != strings.TrimSpace(seg) || seg == "" {
			t.Errorf("range %v = %q is not trimmed", r, seg)
		}
	}
	assertSegments(t, s, text, "One.", "Two.", "Three.")
	if got, err := s.Split(" \n\t ", nil); err != nil || got != nil {
		t.Errorf("blank text split into %v, want nil", got)
	}
}

func TestSplit_WordBoundaryAtTheBreakSeesBothSides(t *testing.T) {
	// \b at the end of before: the break sits between a word character and
	// a space, so it is a boundary; between two word characters it is not.
	s := mustCompile(t, RuleSet{Rules: []Rule{{Before: `x\b`, After: ``, Break: true}}})
	assertSegments(t, s, "ax b", "ax", "b")
	assertSegments(t, s, "axb", "axb")
	// The same for \b at the start of after.
	s = mustCompile(t, RuleSet{Rules: []Rule{{Before: ``, After: `\bq`, Break: true}}})
	assertSegments(t, s, "a qz", "a", "qz")
	assertSegments(t, s, "aqz", "aqz")
}

func TestSplit_AnchorsMeanTheBlockEdges(t *testing.T) {
	s := mustCompile(t, RuleSet{Rules: []Rule{{Before: `^Note:`, After: ws, Break: true}}})
	assertSegments(t, s, "Note: read this. Note: and this.", "Note:", "read this. Note: and this.")

	// Beyond the context window ^ cannot be seen, so it does not match.
	long := "Note:" + strings.Repeat("x", ContextWindow) + " tail"
	s = mustCompile(t, RuleSet{Rules: []Rule{{Before: `^Note:x+`, After: ws, Break: true}}})
	assertSegments(t, s, long, long)
	short := "Note:xxx tail"
	assertSegments(t, s, short, "Note:xxx", "tail")
}

func TestSplit_InvalidUTF8(t *testing.T) {
	s := mustCompile(t, RuleSet{Rules: []Rule{breakAfterPeriod}, Abbreviations: []string{"Dr"}})
	text := "Bad \xff. Next \xc3. Dr. More\x80. End"
	got := segments(t, s, text, nil)
	if want := []string{"Bad \xff.", "Next \xc3.", "Dr. More\x80.", "End"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExplain_NamesTheRuleOrWord(t *testing.T) {
	s := mustCompile(t, RuleSet{
		Rules:               []Rule{{Before: `\?`, After: ws, Break: false}, breakAfterPeriod},
		Abbreviations:       []string{"Dr"},
		NumberAbbreviations: []string{"p"},
	})
	got, err := s.Explain("Dr. Who? See p. 4. End.")
	if err != nil {
		t.Fatal(err)
	}
	want := []Decision{
		{Pos: 3, Source: SourceAbbreviation, Rule: -1, Word: "Dr"},
		{Pos: 8, Source: SourceRule, Rule: 0},
		{Pos: 15, Source: SourceNumberAbbreviation, Rule: -1, Word: "p"},
		{Pos: 18, Break: true, Source: SourceRule, Rule: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestValidate_Limits(t *testing.T) {
	many := func(n int, v string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s%d", v, i)
		}
		return out
	}
	tests := []struct {
		name string
		rs   RuleSet
		want string
	}{
		{"mode", RuleSet{Mode: "words"}, "Mode must be"},
		{"too many rules", RuleSet{Rules: make([]Rule, MaxRules+1)}, "at most 500 rules"},
		{"empty rule", RuleSet{Rules: []Rule{{}}}, "Rule 1 needs a before or an after pattern"},
		{"long pattern", RuleSet{Rules: []Rule{{Before: strings.Repeat("a", MaxPatternLength+1)}}}, "Rule 1: the before pattern is longer than 1000"},
		{"too many words", RuleSet{Abbreviations: many(MaxWords+1, "w")}, "at most 2000 words"},
		{"long word", RuleSet{NumberAbbreviations: []string{strings.Repeat("a", MaxWordLength+1)}}, "is longer than 64"},
		{"tab in word", RuleSet{Abbreviations: []string{"a\tb"}}, "has a line break, a tab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(tc.rs)
			var ie *InvalidError
			if !errors.As(err, &ie) || !strings.Contains(ie.Message, tc.want) {
				t.Fatalf("err = %v, want an InvalidError containing %q", err, tc.want)
			}
		})
	}
	ok := RuleSet{Rules: make([]Rule, MaxRules), Abbreviations: many(MaxWords, "w")}
	for i := range ok.Rules {
		ok.Rules[i] = breakAfterPeriod
	}
	if _, err := Compile(ok); err != nil {
		t.Errorf("a set at every limit: %v", err)
	}
}

func TestValidate_RE2Rejections(t *testing.T) {
	tests := map[string]string{
		`(?=A)`:   "look-ahead is not supported",
		`(?!A)`:   "look-ahead is not supported",
		`(?<=A)`:  "look-behind is not supported",
		`(?<!A)`:  "look-behind is not supported",
		`(a)\1`:   "back-references are not supported",
		`a++`:     "possessive quantifiers are not supported",
		`(?>a)`:   "atomic groups are not supported",
		`[a-`:     "missing closing ]",
		`\p{Foo}`: "invalid character class range",
	}
	for pattern, want := range tests {
		_, err := Compile(RuleSet{Rules: []Rule{{Before: "x", After: pattern, Break: true}}})
		var ie *InvalidError
		if !errors.As(err, &ie) || !strings.Contains(ie.Message, "Rule 1: the after pattern is not valid: "+want) {
			t.Errorf("%s: err = %v, want %q", pattern, err, want)
		}
	}
}

func TestNormalize_Words(t *testing.T) {
	got := Normalize(RuleSet{Abbreviations: []string{" Dr. ", "Dr", "", " . ", "e.g."}})
	if want := []string{"Dr", "e.g"}; !reflect.DeepEqual(got.Abbreviations, want) {
		t.Errorf("abbreviations = %q, want %q", got.Abbreviations, want)
	}
	if got.Mode != ModeSentence || got.Rules == nil || got.NumberAbbreviations == nil {
		t.Errorf("normalize left %+v", got)
	}
}

func TestSplit_WhitespaceAtThePositionIsNotPartOfTheWindow(t *testing.T) {
	s := mustCompile(t, RuleSet{Rules: []Rule{breakAfterPeriod}})
	gap := strings.Repeat(" ", 3*ContextWindow)
	assertSegments(t, s, "One."+gap+"Two.", "One.", "Two.")
	// The same on the before side: the rule needs the full stop.
	s = mustCompile(t, RuleSet{Rules: []Rule{{Before: `\.\s+`, After: `T`, Break: true}}})
	assertSegments(t, s, "One."+gap+"Two.", "One.", "Two.")
}

func TestSplit_WorkBudget(t *testing.T) {
	// 500 rules that are tried at every position and read their whole window.
	costly := RuleSet{Rules: make([]Rule, MaxRules)}
	for i := range costly.Rules {
		costly.Rules[i] = Rule{Before: `z(?s).*`, After: ``, Break: true}
	}
	s := mustCompile(t, costly)
	text := strings.Repeat("A long sentence without the letter. ", 30)
	if _, err := s.Split(text, nil); !errors.Is(err, ErrTooCostly) {
		t.Errorf("costly rules: err = %v, want ErrTooCostly", err)
	}
	if _, err := s.Explain(text); !errors.Is(err, ErrTooCostly) {
		t.Errorf("costly rules, Explain: err = %v, want ErrTooCostly", err)
	}

	// Counted repeats make the regexp engine work hard on every byte it
	// reads: past MaxPatternSize a pattern is refused, and under it the
	// rule pays for its size.
	if _, err := Compile(RuleSet{Rules: []Rule{{After: `(?s).{0,500}.{0,490}z`, Break: true}}}); err == nil || !strings.Contains(err.Error(), "too complex") {
		t.Errorf("pattern over the size limit: err = %v, want it refused", err)
	}
	heavy := mustCompile(t, RuleSet{Rules: []Rule{{After: `(?s).{0,250}.{0,240}z`, Break: true}}})
	if _, err := heavy.Split(text, nil); !errors.Is(err, ErrTooCostly) {
		t.Errorf("large counted repeats: err = %v, want ErrTooCostly", err)
	}

	// A long list of ordinary rules fits on any text: here MaxRules
	// abbreviation-style no-break rules, each tried at every full stop of a
	// dot leader.
	abbreviations := RuleSet{Rules: make([]Rule, MaxRules)}
	for i := range abbreviations.Rules {
		abbreviations.Rules[i] = Rule{Before: fmt.Sprintf(`\b%c%cx\.`, 'A'+rune(i%26), 'a'+rune(i/26)), After: ``}
	}
	leader := "Chapter 1 " + strings.Repeat(".", 5000) + " 5"
	if _, err := mustCompile(t, abbreviations).Split(leader, nil); err != nil {
		t.Errorf("MaxRules abbreviation rules on a dot leader: %v", err)
	}

	// The built-ins stay far inside the budget on the hardest ordinary text:
	// with every rule tried 50 times over, and as a copy with typical added
	// no-break rules.
	hard := []string{
		strings.Repeat(".", 20000),
		"Chapter 1 " + strings.Repeat(".", 5000) + " 5",
		strings.Repeat(". ", 10000),
		strings.Repeat("A. ", 7000),
		strings.Repeat("?!\"' ", 5000),
		strings.Repeat("。", 10000),
		"End." + strings.Repeat(" ", 50000) + "Next.",
	}
	typical := []Rule{
		{Before: `\b\d+\.`, After: `\s`},
		{Before: `(?:\b\p{Lu}\.)+`, After: `\s`},
		{Before: `\b\p{Lu}\p{Ll}{0,3}\.`, After: `\s+\d`},
		{Before: `\.{2,}`, After: ``},
		{Before: `\b(?:approx|incl|excl|dept|vol|ch)\.`, After: `\s`},
		{Before: `\.\s*\.`, After: ``},
		{Before: `[\p{L}\d]+\.[\p{L}\d]+\.`, After: `\s`},
		{Before: `\(\w+\.`, After: `\s`},
		{Before: `\b\w\.`, After: `\s+\p{Ll}`},
		{Before: `:`, After: `\s+\p{Lu}`, Break: true},
	}
	for _, b := range Builtins() {
		repeated := b.RuleSet
		repeated.Rules = nil
		for range 50 {
			repeated.Rules = append(repeated.Rules, b.RuleSet.Rules...)
		}
		copied := b.RuleSet
		copied.Rules = append(append([]Rule{}, typical...), b.RuleSet.Rules...)
		for name, rs := range map[string]RuleSet{"as is": b.RuleSet, "every rule 50 times": repeated, "with typical rules": copied} {
			seg := MustCompile(rs)
			for _, text := range hard {
				if _, err := seg.Split(text, nil); err != nil {
					t.Errorf("%s %s on %q…: %v", b.ID, name, text[:12], err)
				}
			}
		}
	}
}

// A position stops trying rules once an attempt has gone over the budget, so
// one position cannot run every rule's match first.
func TestDecide_StopsAtTheBudget(t *testing.T) {
	rules := make([]Rule, MaxRules)
	for i := range rules {
		rules[i] = Rule{After: `\p{L}\d`, Break: true}
	}
	text := "a bc"
	pos := position{p: 2, prev: 'b', next: 'c', prevSize: 1, nextSize: 1, spaceAfter: -1}
	one := &textView{text: text, budget: 1 << 30}
	mustCompile(t, RuleSet{Rules: rules[:1]}).decide(one, &pos)
	all := &textView{text: text, budget: 0}
	mustCompile(t, RuleSet{Rules: rules}).decide(all, &pos)
	if all.cost != one.cost {
		t.Errorf("cost over a zero budget = %d, want one attempt's %d", all.cost, one.cost)
	}
}
