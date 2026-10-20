package translationmemory

import "testing"

func TestSearchPattern(t *testing.T) {
	cases := []struct {
		name string
		f    EntryFilter
		want string
	}{
		{"plain text is escaped", EntryFilter{Query: `50% (a.b) [x]{2} ^$|?*+\`}, `50% \(a\.b\) \[x\]\{2\} \^\$\|\?\*\+\\`},
		{"exact anchors plain text", EntryFilter{Query: "a.b", Exact: true}, `^a\.b$`},
		{"regex passes through, grouped", EntryFilter{Query: "cancel|abort", Regex: true}, `(?:cancel|abort)`},
		{"exact regex anchors the whole alternation", EntryFilter{Query: "cancel|abort", Regex: true, Exact: true}, `^(?:cancel|abort)$`},
		{"non-ASCII letters are not escaped", EntryFilter{Query: "réduction"}, "réduction"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := searchPattern(c.f); got != c.want {
				t.Errorf("searchPattern(%+v) = %q, want %q", c.f, got, c.want)
			}
		})
	}
}

func TestJSRegexToPostgres(t *testing.T) {
	cases := map[string]string{
		`\bserver\b`: `\yserver\y`,
		`\Bserv`:     `\Yserv`,
		`[\b]`:       `[\b]`,     // backspace inside brackets in both dialects
		`[a\]]\b`:    `[a\]]\y`,  // an escaped ] does not close the bracket
		`\\b`:        `\\b`,      // escaped backslash, then a literal b
		`\d+\.\d+`:   `\d+\.\d+`, // shared escapes are untouched
		`(?=.*a)`:    `(?=.*a)`,
		`trailing\`:  `trailing\`,
	}
	for in, want := range cases {
		if got := jsRegexToPostgres(in); got != want {
			t.Errorf("jsRegexToPostgres(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchScope(t *testing.T) {
	cases := []struct {
		scope              string
		inSource, inTarget bool
	}{
		{"", true, true},
		{ScopeBoth, true, true},
		{ScopeSource, true, false},
		{ScopeTarget, false, true},
	}
	for _, c := range cases {
		s, tg, err := searchScope(c.scope)
		if err != nil || s != c.inSource || tg != c.inTarget {
			t.Errorf("searchScope(%q) = %v, %v, %v; want %v, %v, nil", c.scope, s, tg, err, c.inSource, c.inTarget)
		}
	}
	if _, _, err := searchScope("everywhere"); err == nil {
		t.Error("an unknown scope must be rejected")
	}
}
