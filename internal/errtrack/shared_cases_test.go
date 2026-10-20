package errtrack_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"openloc/internal/errtrack"
)

// TestSharedRedactionCases runs testdata/redaction_cases.json, which
// web/src/lib/errtrack.test.ts runs against the browser twins too, so the Go
// and TS scrubbers can't drift apart.
func TestSharedRedactionCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/redaction_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var groups map[string][]struct{ Name, In, Want string }
	if err := json.Unmarshal(raw, &groups); err != nil {
		t.Fatal(err)
	}
	fns := map[string]func(string) string{
		"redactTokenPaths": errtrack.RedactTokenPaths,
		"scrubURL":         errtrack.ScrubURL,
		"scrubMessage":     errtrack.ScrubMessage,
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	slices.Sort(names)
	if want := []string{"redactTokenPaths", "scrubMessage", "scrubURL"}; !slices.Equal(names, want) {
		t.Fatalf("case groups = %v, want %v", names, want)
	}
	for group, cases := range groups {
		if len(cases) == 0 {
			t.Errorf("group %s has no cases", group)
		}
		for _, c := range cases {
			t.Run(group+"/"+c.Name, func(t *testing.T) {
				if got := fns[group](c.In); got != c.Want {
					t.Errorf("%s(%q) = %q, want %q", group, c.In, got, c.Want)
				}
			})
		}
	}
}
