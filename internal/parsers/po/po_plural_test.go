package po

import (
	"os"
	"strings"
	"testing"
)

// A plural entry gives one segment per msgstr[N] line in the file, keyed
// "<entry key>#N": form 0 shows msgid as its source, the others msgid_plural.
func TestPOParsePluralFormsAreSegments(t *testing.T) {
	content, err := os.ReadFile("testdata/plurals_pl.po")
	if err != nil {
		t.Fatal(err)
	}
	segs, err := PO{PlainText: true}.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	type seg struct{ key, text, context string }
	var got []seg
	for _, s := range segs {
		got = append(got, seg{s.Key, s.Text, s.Context})
	}
	want := []seg{
		{"Inbox", "Inbox", ""},
		{"%d new message#0", "%d new message", "plural form 0"},
		{"%d new message#1", "%d new messages", "plural form 1"},
		{"%d new message#2", "%d new messages", "plural form 2"},
		{"files|Deleted %d file from the folder#0", "Deleted %d file from the folder", "files; plural form 0"},
		{"files|Deleted %d file from the folder#1", "Deleted %d files from the folder", "files; plural form 1"},
		{"files|Deleted %d file from the folder#2", "Deleted %d files from the folder", "files; plural form 2"},
		{"Settings", "Settings", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("segments = %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Export writes each plural form into its own msgstr[N] line and leaves every
// other line, the header included, as uploaded.
func TestPOReconstructPluralFormsRoundTrip(t *testing.T) {
	content, err := os.ReadFile("testdata/plurals_pl.po")
	if err != nil {
		t.Fatal(err)
	}
	p := PO{PlainText: true}
	out, err := p.Reconstruct(content, map[string]string{
		"Inbox":            "Skrzynka",
		"%d new message#0": "%d nowa wiadomość",
		"%d new message#1": "%d nowe wiadomości",
		"%d new message#2": "%d nowych wiadomości",
		"files|Deleted %d file from the folder#1": "Usunięto %d pliki z folderu",
	})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	want := string(content)
	for _, r := range [][2]string{
		{"msgid \"Inbox\"\nmsgstr \"\"", "msgid \"Inbox\"\nmsgstr \"Skrzynka\""},
		{"msgid_plural \"%d new messages\"\nmsgstr[0] \"\"\nmsgstr[1] \"\"\nmsgstr[2] \"\"",
			"msgid_plural \"%d new messages\"\nmsgstr[0] \"%d nowa wiadomość\"\nmsgstr[1] \"%d nowe wiadomości\"\nmsgstr[2] \"%d nowych wiadomości\""},
		{"\"from the folder\"\nmsgstr[0] \"\"\nmsgstr[1] \"\"", "\"from the folder\"\nmsgstr[0] \"\"\nmsgstr[1] \"Usunięto %d pliki z folderu\""},
	} {
		if !strings.Contains(want, r[0]) {
			t.Fatalf("fixture lacks %q", r[0])
		}
		want = strings.Replace(want, r[0], r[1], 1)
	}
	if string(out) != want {
		t.Fatalf("export:\n%s\nwant:\n%s", out, want)
	}
	// The export parses back to the same segments with the translations in place.
	segs, err := p.Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	targets := map[string]string{}
	for _, s := range segs {
		targets[s.Key] = s.Target
	}
	if targets["%d new message#2"] != "%d nowych wiadomości" || targets["Settings"] != "" {
		t.Errorf("re-parsed targets = %v", targets)
	}
}

// An entry with no msgstr line is left alone on export rather than written over
// the first line of the file.
func TestPOReconstructSkipsAnEntryWithoutMsgstr(t *testing.T) {
	content := "# header comment\nmsgid \"Orphan\"\n\nmsgid \"Kept\"\nmsgstr \"\"\n"
	out, err := PO{PlainText: true}.Reconstruct([]byte(content), map[string]string{"Orphan": "X", "Kept": "Y"})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if want := "# header comment\nmsgid \"Orphan\"\n\nmsgid \"Kept\"\nmsgstr \"Y\"\n"; string(out) != want {
		t.Fatalf("export = %q, want %q", out, want)
	}
}
