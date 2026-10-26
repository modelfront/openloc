package apple_strings

import (
	"strings"
	"testing"
)

func TestAppleStringsParse(t *testing.T) {
	input := `/* Main screen */
"greeting" = "Hello";
"farewell" = "Goodbye";
"app_name" = "My App";
`
	p := AppleStrings{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segments))
	}

	expected := []struct {
		key  string
		text string
		pos  int
	}{
		{"greeting", "Hello", 0},
		{"farewell", "Goodbye", 1},
		{"app_name", "My App", 2},
	}

	for i, e := range expected {
		if segments[i].Key != e.key {
			t.Errorf("segment %d key = %q, want %q", i, segments[i].Key, e.key)
		}
		if segments[i].Text != e.text {
			t.Errorf("segment %d text = %q, want %q", i, segments[i].Text, e.text)
		}
		if segments[i].Position != e.pos {
			t.Errorf("segment %d position = %d, want %d", i, segments[i].Position, e.pos)
		}
	}
}

func TestAppleStringsParseEscaped(t *testing.T) {
	input := `"escaped_quote" = "Say \"hello\"";
"newline" = "Line one\nLine two";
"unicode" = "\U0041\U0042\U0043";
"backslash" = "back\\slash";
`
	p := AppleStrings{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 4 {
		t.Fatalf("expected 4 segments, got %d", len(segments))
	}

	if segments[0].Text != `Say "hello"` {
		t.Errorf("escaped quote: text = %q, want %q", segments[0].Text, `Say "hello"`)
	}
	if segments[1].Text != "Line one\nLine two" {
		t.Errorf("newline: text = %q, want %q", segments[1].Text, "Line one\nLine two")
	}
	if segments[2].Text != "ABC" {
		t.Errorf("unicode: text = %q, want %q", segments[2].Text, "ABC")
	}
	if segments[3].Text != `back\slash` {
		t.Errorf("backslash: text = %q, want %q", segments[3].Text, `back\slash`)
	}
}

func TestAppleStringsCommentsPreserved(t *testing.T) {
	input := `/* Section header */
"key1" = "value1";

// Single line comment
"key2" = "value2";
`
	p := AppleStrings{}
	translations := map[string]string{
		"key1": "translated1",
		"key2": "translated2",
	}

	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	out := string(result)
	if !strings.Contains(out, "/* Section header */") {
		t.Error("block comment was not preserved")
	}
	if !strings.Contains(out, "// Single line comment") {
		t.Error("line comment was not preserved")
	}
	if !strings.Contains(out, `"translated1"`) {
		t.Errorf("translation not applied:\n%s", out)
	}
	if !strings.Contains(out, `"translated2"`) {
		t.Errorf("translation not applied:\n%s", out)
	}
}

func TestAppleStringsReconstruct(t *testing.T) {
	input := `/* Greeting */
"greeting" = "Hello";
"farewell" = "Goodbye";
`
	translations := map[string]string{
		"greeting": "Hola",
		"farewell": "Adiós",
	}

	p := AppleStrings{}
	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	out := string(result)
	if !strings.Contains(out, `"greeting" = "Hola";`) {
		t.Errorf("greeting not translated:\n%s", out)
	}
	if !strings.Contains(out, `"farewell" = "Adi`) {
		t.Errorf("farewell not translated:\n%s", out)
	}
	if !strings.Contains(out, "/* Greeting */") {
		t.Error("comment not preserved")
	}
}

func TestAppleStringsParseEmpty(t *testing.T) {
	p := AppleStrings{}
	segments, err := p.Parse([]byte(""))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 0 {
		t.Fatalf("expected 0 segments, got %d", len(segments))
	}
}

func TestAppleStringsReconstructPartial(t *testing.T) {
	input := `"key1" = "value1";
"key2" = "value2";
"key3" = "value3";
`
	translations := map[string]string{
		"key1": "translated1",
		// key2 not translated
		"key3": "translated3",
	}

	p := AppleStrings{}
	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	out := string(result)
	if !strings.Contains(out, `"key1" = "translated1";`) {
		t.Errorf("key1 not translated:\n%s", out)
	}
	if !strings.Contains(out, `"key2" = "value2";`) {
		t.Errorf("key2 should be preserved:\n%s", out)
	}
	if !strings.Contains(out, `"key3" = "translated3";`) {
		t.Errorf("key3 not translated:\n%s", out)
	}
}
