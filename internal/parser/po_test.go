package parser

import (
	"strings"
	"testing"
)

func TestPOParseBasic(t *testing.T) {
	input := `
# This is a comment
msgid "Hello"
msgstr "Hola"

msgid "World"
msgstr "Mundo"

msgid "Goodbye"
msgstr "Adiós"
`
	p := PO{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segments))
	}

	expected := []struct {
		key    string
		text   string
		target string
		pos    int
	}{
		{"Hello", "Hello", "Hola", 0},
		{"World", "World", "Mundo", 1},
		{"Goodbye", "Goodbye", "Adiós", 2},
	}

	for i, e := range expected {
		if segments[i].Key != e.key {
			t.Errorf("segment %d key = %q, want %q", i, segments[i].Key, e.key)
		}
		if segments[i].Text != e.text {
			t.Errorf("segment %d text = %q, want %q", i, segments[i].Text, e.text)
		}
		if segments[i].Target != e.target {
			t.Errorf("segment %d target = %q, want %q", i, segments[i].Target, e.target)
		}
		if segments[i].Position != e.pos {
			t.Errorf("segment %d position = %d, want %d", i, segments[i].Position, e.pos)
		}
	}
}

func TestPOParseMultiline(t *testing.T) {
	input := `msgid ""
"Hello "
"World"
msgstr ""
"Hola "
"Mundo"
`
	p := PO{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	if segments[0].Text != "Hello World" {
		t.Errorf("text = %q, want %q", segments[0].Text, "Hello World")
	}
	if segments[0].Target != "Hola Mundo" {
		t.Errorf("target = %q, want %q", segments[0].Target, "Hola Mundo")
	}
}

func TestPOParseEscaped(t *testing.T) {
	input := `msgid "Line one\nLine two"
msgstr "Línea uno\nLínea dos"

msgid "Say \"hello\""
msgstr "Di \"hola\""

msgid "Back\\slash"
msgstr "Barra\\invertida"
`
	p := PO{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segments))
	}
	if segments[0].Text != "Line one\nLine two" {
		t.Errorf("segment 0 text = %q, want %q", segments[0].Text, "Line one\nLine two")
	}
	if segments[1].Text != `Say "hello"` {
		t.Errorf("segment 1 text = %q, want %q", segments[1].Text, `Say "hello"`)
	}
	if segments[2].Text != `Back\slash` {
		t.Errorf("segment 2 text = %q, want %q", segments[2].Text, `Back\slash`)
	}
}

func TestPOReconstruct(t *testing.T) {
	input := `# Comment preserved
msgid "Hello"
msgstr "Hola"

msgid "World"
msgstr "Mundo"
`
	translations := map[string]string{
		"Hello": "Bonjour",
		"World": "Le Monde",
	}

	p := PO{}
	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	out := string(result)
	if !strings.Contains(out, `# Comment preserved`) {
		t.Error("comment was not preserved")
	}
	if !strings.Contains(out, `msgstr "Bonjour"`) {
		t.Errorf("expected translated msgstr, got:\n%s", out)
	}
	if !strings.Contains(out, `msgstr "Le Monde"`) {
		t.Errorf("expected translated msgstr, got:\n%s", out)
	}
	// Verify msgid is untouched.
	if !strings.Contains(out, `msgid "Hello"`) {
		t.Errorf("msgid was modified:\n%s", out)
	}
}

func TestPOParsePOTEmptyMsgstr(t *testing.T) {
	input := `msgid "Untranslated"
msgstr ""

msgid "Also untranslated"
msgstr ""
`
	p := PO{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}
	if segments[0].Text != "Untranslated" {
		t.Errorf("text = %q, want %q", segments[0].Text, "Untranslated")
	}
	if segments[0].Target != "" {
		t.Errorf("target = %q, want empty", segments[0].Target)
	}
}

func TestPOParseHeaderSkipping(t *testing.T) {
	input := `msgid ""
msgstr ""
"Content-Type: text/plain; charset=UTF-8\n"
"Content-Transfer-Encoding: 8bit\n"

msgid "Real entry"
msgstr "Entrada real"
`
	p := PO{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment (header skipped), got %d", len(segments))
	}
	if segments[0].Text != "Real entry" {
		t.Errorf("text = %q, want %q", segments[0].Text, "Real entry")
	}
}

func TestPOParseFuzzySkipped(t *testing.T) {
	input := `#, fuzzy
msgid "Fuzzy entry"
msgstr "Entrada difusa"

msgid "Normal entry"
msgstr "Entrada normal"
`
	p := PO{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment (fuzzy skipped), got %d", len(segments))
	}
	if segments[0].Text != "Normal entry" {
		t.Errorf("text = %q, want %q", segments[0].Text, "Normal entry")
	}
}

func TestPOParseMsgctxt(t *testing.T) {
	input := `msgctxt "menu"
msgid "File"
msgstr "Archivo"

msgctxt "dialog"
msgid "File"
msgstr "Fichero"
`
	p := PO{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}
	if segments[0].Key != "menu|File" {
		t.Errorf("segment 0 key = %q, want %q", segments[0].Key, "menu|File")
	}
	if segments[0].Context != "menu" {
		t.Errorf("segment 0 context = %q, want %q", segments[0].Context, "menu")
	}
	if segments[1].Key != "dialog|File" {
		t.Errorf("segment 1 key = %q, want %q", segments[1].Key, "dialog|File")
	}
}

func TestPOReconstructMultiline(t *testing.T) {
	input := `msgid ""
"Multi "
"line"
msgstr ""
"Old "
"translation"
`
	translations := map[string]string{
		"Multi line": "Nueva traducción",
	}

	p := PO{}
	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	out := string(result)
	if !strings.Contains(out, `msgstr "Nueva traducción"`) {
		t.Errorf("expected translated msgstr in output:\n%s", out)
	}
	// The old msgstr continuation lines must go, or they append to the new value.
	for _, old := range []string{`"Old "`, `"translation"`} {
		if strings.Contains(out, old) {
			t.Errorf("old msgstr line %s left in output:\n%s", old, out)
		}
	}
	// Original msgid lines should be preserved.
	if !strings.Contains(out, `"Multi "`) {
		t.Errorf("msgid continuation lines were not preserved:\n%s", out)
	}
}
