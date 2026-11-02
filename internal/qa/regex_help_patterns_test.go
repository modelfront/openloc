package qa

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// helpRow matches one row of web/src/lib/regex-help-patterns.ts.
var helpRow = regexp.MustCompile(`pattern: '((?:[^'\\]|\\.)*)'.*sample: '((?:[^'\\]|\\.)*)'(.*)$`)

// The regex rule dialog shows the shared cheat sheet without the rows marked
// segmentsOnly or notInQA. Every row it shows must pass the save-time check and
// match its sample in Go; every notInQA row must really be refused, or the
// mark is stale and hides a working example.
func TestRegexHelpPatterns_QARows(t *testing.T) {
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
		if strings.Contains(m[3], "segmentsOnly: true") {
			continue
		}
		pattern, sample := unquote.ReplaceAllString(m[1], "$1"), unquote.ReplaceAllString(m[2], "$1")
		raw, _ := json.Marshal(RegexRuleConfig{Name: "n", Mode: RegexModeTargetMustNotMatch, Pattern: pattern, Message: "m"})
		_, err := ValidateRegexRuleConfig(context.Background(), raw)
		if strings.Contains(m[3], "notInQA: true") {
			if err == nil {
				t.Errorf("%s is marked notInQA but a QA rule accepts it", pattern)
			}
			continue
		}
		rows++
		if err != nil {
			t.Errorf("%s is shown for QA rules but refused: %v", pattern, err)
			continue
		}
		if !regexp.MustCompile(pattern).MatchString(sample) {
			t.Errorf("%s does not match its sample %q in Go", pattern, sample)
		}
	}
	if rows == 0 {
		t.Fatal("found no cheat-sheet rows for QA rules")
	}
}
