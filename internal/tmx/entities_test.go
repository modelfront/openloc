package tmx

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The plain text of a <seg> is character data: entities and character
// references are decoded, so "Tom &amp; Jerry" is stored as "Tom & Jerry". The
// rich form keeps the seg's XML as it is. Export escapes the plain text once.
func TestParseEntriesDecodesEntitiesAndExportsThemOnce(t *testing.T) {
	content, err := os.ReadFile("testdata/entities.tmx")
	if err != nil {
		t.Fatal(err)
	}
	wantSource := []string{"Tom & Jerry", `Use a < b and say "yes"`, "Press OK & go"}
	wantTarget := []string{"Tom & Jerry", "Verwenden Sie a < b und sagen Sie „ja“", "Drücken SieOK & los"}

	check := func(name string, entries []Entry) {
		t.Helper()
		if len(entries) != 3 {
			t.Fatalf("%s: %d entries, want 3", name, len(entries))
		}
		for i, e := range entries {
			if e.SourceText != wantSource[i] || e.TargetText != wantTarget[i] {
				t.Errorf("%s entry %d = %q → %q, want %q → %q", name, i, e.SourceText, e.TargetText, wantSource[i], wantTarget[i])
			}
		}
		if !strings.Contains(entries[2].SourceRichText, "OK &amp; go") {
			t.Errorf("%s: rich text = %q, want the seg's XML", name, entries[2].SourceRichText)
		}
	}

	entries, err := ParseEntries(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	check("ParseEntries", entries)

	f, err := Parse(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	check("Parse", FlattenEntries(f))

	// Plain entries export with each special character escaped exactly once.
	plain := make([]Entry, len(entries))
	for i, e := range entries {
		plain[i] = Entry{SourceLang: e.SourceLang, TargetLang: e.TargetLang, SourceText: e.SourceText, TargetText: e.TargetText}
	}
	var buf bytes.Buffer
	if err := Export(&buf, "en-us", plain); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "<seg>Tom &amp; Jerry</seg>") || strings.Contains(out, "&amp;amp;") {
		t.Errorf("export escaped wrongly:\n%s", out)
	}
	again, err := ParseEntries(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range again {
		if e.SourceText != wantSource[i] {
			t.Errorf("re-import entry %d = %q, want %q", i, e.SourceText, wantSource[i])
		}
	}
}
