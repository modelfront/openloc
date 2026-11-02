package editor

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var helpRow = regexp.MustCompile(`pattern: '((?:[^'\\]|\\.)*)'.*sample: '((?:[^'\\]|\\.)*)'(.*)$`)

// The segment search shows the shared cheat sheet without the tmOnly rows.
// Every row it shows must compile in the search's regex mode and find its
// sample; every tmOnly row must really fail here, or the mark hides a working
// example.
func TestRegexHelpPatterns_SegmentSearchRows(t *testing.T) {
	src, err := os.ReadFile("../../web/src/lib/regex-help-patterns.ts")
	if err != nil {
		t.Fatalf("read cheat sheet: %v", err)
	}
	unquote := regexp.MustCompile(`\\(.)`)
	rows := 0
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "{ pattern: ") {
			continue
		}
		m := helpRow.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("cannot parse cheat-sheet row (keep one row per line, single-quoted): %s", strings.TrimSpace(line))
		}
		pattern, sample := unquote.ReplaceAllString(m[1], "$1"), unquote.ReplaceAllString(m[2], "$1")
		match, err := SearchOptions{Text: pattern, Target: true, CaseSensitive: true, Regex: true}.Matcher()
		if strings.Contains(m[3], "tmOnly: true") {
			if err == nil {
				t.Errorf("%s is marked tmOnly but the segment search accepts it", pattern)
			}
			continue
		}
		rows++
		if err != nil {
			t.Errorf("%s is shown for the segment search but refused: %v", pattern, err)
			continue
		}
		if !match("", sample) {
			t.Errorf("%s does not find its sample %q", pattern, sample)
		}
	}
	if rows == 0 {
		t.Fatal("found no cheat-sheet rows for the segment search")
	}
}
