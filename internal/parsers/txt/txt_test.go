package txt

import (
	"testing"
)

func TestTXTParse(t *testing.T) {
	input := "Hello world\nThis is a test\n\nAnother line\n"
	p := TXT{}
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
		{"line:1", "Hello world", 0},
		{"line:2", "This is a test", 1},
		{"line:4", "Another line", 2},
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

func TestTXTParseWindows(t *testing.T) {
	input := "Line one\r\nLine two\r\n"
	p := TXT{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}

	if segments[0].Text != "Line one" {
		t.Errorf("segment 0 text = %q, want %q", segments[0].Text, "Line one")
	}
	if segments[1].Text != "Line two" {
		t.Errorf("segment 1 text = %q, want %q", segments[1].Text, "Line two")
	}
}

func TestTXTReconstruct(t *testing.T) {
	original := "Hello world\nUntranslated\n\nAnother line\n"
	translations := map[string]string{
		"line:1": "Hola mundo",
		"line:4": "Otra linea",
	}

	p := TXT{}
	result, err := p.Reconstruct([]byte(original), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	expected := "Hola mundo\nUntranslated\n\nOtra linea\n"
	if string(result) != expected {
		t.Errorf("got:\n%s\nwant:\n%s", string(result), expected)
	}
}

func TestTXTParseEmpty(t *testing.T) {
	p := TXT{}
	segments, err := p.Parse([]byte(""))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 0 {
		t.Fatalf("expected 0 segments, got %d", len(segments))
	}
}
