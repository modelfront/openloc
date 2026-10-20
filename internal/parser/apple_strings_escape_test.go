package parser

import (
	"strconv"
	"strings"
	"testing"
)

// appleUnquote reads a .strings quoted value the way CoreFoundation's
// old-style plist parser does: `\U` takes up to 4 hex digits, and any escape
// letter it does not know (including a lowercase `\u`) yields the letter.
// Octal escapes are left out; the test inputs do not produce them.
func appleUnquote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'U':
			j := i + 1
			for j < len(s) && j < i+5 && strings.IndexByte("0123456789abcdefABCDEF", s[j]) >= 0 {
				j++
			}
			r, _ := strconv.ParseUint(s[i+1:j], 16, 32)
			b.WriteRune(rune(r))
			i = j - 1
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// A French translation with a no-break space before the colon must reach
// the exported .strings file in a form Apple's parser reads back unchanged.
func TestAppleStringsReconstruct_NoBreakSpaceReadableByApple(t *testing.T) {
	want := "Nom\u00a0:"
	out, err := AppleStrings{}.Reconstruct([]byte(`"name" = "Name:";`), map[string]string{"name": want})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	line := string(out)
	start := strings.Index(line, `= "`)
	end := strings.LastIndex(line, `";`)
	if start < 0 || end <= start {
		t.Fatalf("unexpected line: %s", line)
	}
	if got := appleUnquote(line[start+3 : end]); got != want {
		t.Errorf("Apple reads the value as %q, want %q (line: %s)", got, want, line)
	}
}

// appleSplitLine returns the raw (still escaped) key and value of a
// `"key" = "value";` line, ending each quoted string at the first quote that
// is not escaped, as CoreFoundation does.
func appleSplitLine(t *testing.T, line string) (string, string) {
	t.Helper()
	quoted := func(s string) (string, string) {
		if !strings.HasPrefix(s, `"`) {
			t.Fatalf("expected a quoted string at %q (line: %s)", s, line)
		}
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '"':
				return s[1:i], s[i+1:]
			}
		}
		t.Fatalf("unterminated quoted string (line: %s)", line)
		return "", ""
	}
	key, rest := quoted(line)
	rest, ok := strings.CutPrefix(rest, " = ")
	if !ok {
		t.Fatalf("expected ` = ` after the key (line: %s)", line)
	}
	val, rest := quoted(rest)
	if rest != ";" {
		t.Fatalf("expected `;` after the value, got %q (line: %s)", rest, line)
	}
	return key, val
}

// Every key and value the exporter writes must read back unchanged by both
// Apple's parser and our own: parse, reconstruct, parse gives the same text.
func TestAppleStringsReconstruct_EscapesRoundTrip(t *testing.T) {
	nbsp := string(rune(0x00A0))
	original := strings.Join([]string{
		`/* comment */`,
		`"plain" = "Plain";`,
		`"say \"hi\"" = "Say hi";`,
		`"C:\\new" = "Path";`,
		`"tab\there" = "Tab";`,
		`"ret\rurn" = "Return";`,
		`"it\'s" = "It's";`,
	}, "\n")
	translations := map[string]string{
		"plain":     "Nom" + nbsp + ":",
		`say "hi"`:  "dis \"salut\"\tà\ttous",
		`C:\new`:    `C:\nouveau\\dossier` + "\n" + "ligne 2",
		"tab\there": "café\r\nthé",
		"ret\rurn":  `fin\`,
		"it's":      "c'est",
	}

	src, err := AppleStrings{}.Parse([]byte(original))
	if err != nil {
		t.Fatalf("Parse original: %v", err)
	}
	if len(src) != len(translations) {
		t.Fatalf("parsed %d segments from the original, want %d", len(src), len(translations))
	}
	for _, s := range src {
		if _, ok := translations[s.Key]; !ok {
			t.Fatalf("original key parsed as %q, not in the translation map", s.Key)
		}
	}

	out, err := AppleStrings{}.Reconstruct([]byte(original), translations)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	got, err := AppleStrings{}.Parse(out)
	if err != nil {
		t.Fatalf("Parse reconstructed: %v", err)
	}
	if len(got) != len(src) {
		t.Fatalf("reconstructed file parses to %d segments, want %d:\n%s", len(got), len(src), out)
	}
	for i, s := range got {
		if s.Key != src[i].Key {
			t.Errorf("segment %d: key %q, want %q", i, s.Key, src[i].Key)
		}
		if want := translations[src[i].Key]; s.Text != want {
			t.Errorf("segment %d: our parser reads %q, want %q", i, s.Text, want)
		}
	}

	lines := strings.Split(string(out), "\n")[1:]
	for i, line := range lines {
		rawKey, rawVal := appleSplitLine(t, line)
		if k := appleUnquote(rawKey); k != src[i].Key {
			t.Errorf("line %d: Apple reads the key as %q, want %q", i, k, src[i].Key)
		}
		if v, want := appleUnquote(rawVal), translations[src[i].Key]; v != want {
			t.Errorf("line %d: Apple reads the value as %q, want %q", i, v, want)
		}
	}
}
