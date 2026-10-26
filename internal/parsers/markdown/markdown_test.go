package markdown

import (
	"strings"
	"testing"
)

func TestMarkdownParseBasic(t *testing.T) {
	input := `# Welcome

This is a paragraph.

- Item one
- Item two
`

	p := Markdown{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	expected := []struct {
		key  string
		text string
	}{
		{"block:1", "# Welcome"},
		{"block:2", "This is a paragraph."},
		{"block:3", "- Item one"},
		{"block:4", "- Item two"},
	}

	if len(segments) != len(expected) {
		t.Fatalf("expected %d segments, got %d: %+v", len(expected), len(segments), segments)
	}
	for i, e := range expected {
		if segments[i].Key != e.key {
			t.Errorf("segment %d key = %q, want %q", i, segments[i].Key, e.key)
		}
		if segments[i].Text != e.text {
			t.Errorf("segment %d text = %q, want %q", i, segments[i].Text, e.text)
		}
		if segments[i].Position != i {
			t.Errorf("segment %d position = %d, want %d", i, segments[i].Position, i)
		}
	}
}

func TestMarkdownParseCodeBlocksSkipped(t *testing.T) {
	input := "# Heading\n\n```go\nfunc main() {}\n```\n\nAfter code.\n\n    indented code line\n\nFinal paragraph.\n"

	p := Markdown{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d: %+v", len(segments), segments)
	}
	if segments[0].Text != "# Heading" {
		t.Errorf("segment 0 text = %q", segments[0].Text)
	}
	if segments[1].Text != "After code." {
		t.Errorf("segment 1 text = %q", segments[1].Text)
	}
	if segments[2].Text != "Final paragraph." {
		t.Errorf("segment 2 text = %q", segments[2].Text)
	}
}

func TestMarkdownParseInlineMarkup(t *testing.T) {
	input := "This has **bold** and *italic* and `code` and [a link](http://example.com).\n"

	p := Markdown{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	text := segments[0].Text
	if !strings.Contains(text, "**bold**") {
		t.Errorf("bold not preserved: %q", text)
	}
	if !strings.Contains(text, "*italic*") {
		t.Errorf("italic not preserved: %q", text)
	}
	if !strings.Contains(text, "`code`") {
		t.Errorf("inline code not preserved: %q", text)
	}
	if !strings.Contains(text, "[a link](http://example.com)") {
		t.Errorf("link not preserved: %q", text)
	}
}

func TestMarkdownParseHorizontalRule(t *testing.T) {
	input := "Before\n\n---\n\nAfter\n"

	p := Markdown{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d: %+v", len(segments), segments)
	}
	if segments[0].Text != "Before" {
		t.Errorf("segment 0 = %q", segments[0].Text)
	}
	if segments[1].Text != "After" {
		t.Errorf("segment 1 = %q", segments[1].Text)
	}
}

func TestMarkdownParseOrderedList(t *testing.T) {
	input := "1. First\n2. Second\n3. Third\n"

	p := Markdown{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d: %+v", len(segments), segments)
	}
	if segments[0].Text != "1. First" {
		t.Errorf("segment 0 = %q", segments[0].Text)
	}
}

func TestMarkdownReconstruct(t *testing.T) {
	input := "# Welcome\n\nHello world.\n\n- Item one\n- Item two\n\n---\n\n```go\ncode\n```\n\nEnd.\n"

	translations := map[string]string{
		"block:1": "# Bienvenidos",
		"block:2": "Hola mundo.",
		"block:3": "- Elemento uno",
		"block:4": "- Elemento dos",
		"block:5": "Fin.",
	}

	p := Markdown{}
	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	out := string(result)
	if !strings.Contains(out, "# Bienvenidos") {
		t.Errorf("heading not translated:\n%s", out)
	}
	if !strings.Contains(out, "Hola mundo.") {
		t.Errorf("paragraph not translated:\n%s", out)
	}
	if !strings.Contains(out, "- Elemento uno") {
		t.Errorf("list item 1 not translated:\n%s", out)
	}
	if !strings.Contains(out, "Fin.") {
		t.Errorf("end paragraph not translated:\n%s", out)
	}
	// Code block should be preserved.
	if !strings.Contains(out, "```go") {
		t.Errorf("code fence not preserved:\n%s", out)
	}
	if !strings.Contains(out, "code") {
		t.Errorf("code content not preserved:\n%s", out)
	}
	// Horizontal rule preserved.
	if !strings.Contains(out, "---") {
		t.Errorf("horizontal rule not preserved:\n%s", out)
	}
	// Original text should not appear.
	for _, src := range []string{"Welcome", "Hello world.", "End."} {
		if strings.Contains(out, src) {
			t.Errorf("source text %q still present:\n%s", src, out)
		}
	}
}

func TestMarkdownReconstructPartial(t *testing.T) {
	input := "# Title\n\nKeep this.\n\nReplace this.\n"

	translations := map[string]string{
		"block:3": "Replaced.",
	}

	p := Markdown{}
	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	out := string(result)
	if !strings.Contains(out, "# Title") {
		t.Errorf("untranslated heading lost:\n%s", out)
	}
	if !strings.Contains(out, "Keep this.") {
		t.Errorf("untranslated paragraph lost:\n%s", out)
	}
	if !strings.Contains(out, "Replaced.") {
		t.Errorf("translated paragraph missing:\n%s", out)
	}
	if strings.Contains(out, "Replace this.") {
		t.Errorf("source paragraph still present next to its translation:\n%s", out)
	}
}

func TestMarkdownParseMultilineParagraph(t *testing.T) {
	input := "Line one\nline two\nline three\n\nNext para.\n"

	p := Markdown{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d: %+v", len(segments), segments)
	}
	if segments[0].Text != "Line one\nline two\nline three" {
		t.Errorf("multiline paragraph = %q", segments[0].Text)
	}
	if segments[1].Text != "Next para." {
		t.Errorf("second paragraph = %q", segments[1].Text)
	}
}
