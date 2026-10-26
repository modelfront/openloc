package xliff

import (
	"strings"
	"testing"
)

func TestXLIFFInlineTagPreservation(t *testing.T) {
	xliffWithTags := []byte(`<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="de" datatype="plaintext" original="test.txt">
    <body>
      <trans-unit id="1">
        <source>Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept> to continue</source>
        <target>Klicken Sie <bpt id="1">&lt;b&gt;</bpt>hier<ept id="1">&lt;/b&gt;</ept> um fortzufahren</target>
      </trans-unit>
      <trans-unit id="2">
        <source>Press <ph id="2">{key}</ph> to submit</source>
        <target>Drücken Sie <ph id="2">{key}</ph> zum Absenden</target>
      </trans-unit>
      <trans-unit id="3">
        <source>Plain text without tags</source>
        <target>Einfacher Text ohne Tags</target>
      </trans-unit>
    </body>
  </file>
</xliff>`)

	p := XLIFF{}
	segs, err := p.Parse(xliffWithTags)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segs))
	}

	// Segment 1: has bpt/ept tags
	if segs[0].Text != "Click here to continue" {
		t.Errorf("seg[0].Text = %q, want %q", segs[0].Text, "Click here to continue")
	}
	if segs[0].RichText == "" {
		t.Error("seg[0].RichText should not be empty — has bpt/ept tags")
	}
	// RichText should contain the tags
	// bpt/ept content stays XML-escaped so the tag remains well-formed
	if want := `Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept> to continue`; segs[0].RichText != want {
		t.Errorf("seg[0].RichText = %q, want %q", segs[0].RichText, want)
	}

	// Segment 2: has ph tag
	if segs[1].Text != "Press {key} to submit" {
		t.Errorf("seg[1].Text = %q, want %q", segs[1].Text, "Press {key} to submit")
	}
	if segs[1].RichText == "" {
		t.Error("seg[1].RichText should not be empty — has ph tag")
	}
	if want := `Press <ph id="2">{key}</ph> to submit`; segs[1].RichText != want { // ph content is plain text, no entity decoding needed
		t.Errorf("seg[1].RichText = %q, want %q", segs[1].RichText, want)
	}

	// Segment 3: no tags → RichText should be empty
	if segs[2].Text != "Plain text without tags" {
		t.Errorf("seg[2].Text = %q, want %q", segs[2].Text, "Plain text without tags")
	}
	if segs[2].RichText != "" {
		t.Errorf("seg[2].RichText = %q, want empty (no tags)", segs[2].RichText)
	}

	// TargetRichText: segments with tagged targets must populate TargetRichText
	if segs[0].Target != "Klicken Sie hier um fortzufahren" {
		t.Errorf("seg[0].Target = %q, want plain text without tags", segs[0].Target)
	}
	if want := `Klicken Sie <bpt id="1">&lt;b&gt;</bpt>hier<ept id="1">&lt;/b&gt;</ept> um fortzufahren`; segs[0].TargetRichText != want {
		t.Errorf("seg[0].TargetRichText = %q, want %q", segs[0].TargetRichText, want)
	}
	if segs[1].Target != "Drücken Sie {key} zum Absenden" {
		t.Errorf("seg[1].Target = %q, want plain text without tags", segs[1].Target)
	}
	if want := `Drücken Sie <ph id="2">{key}</ph> zum Absenden`; segs[1].TargetRichText != want {
		t.Errorf("seg[1].TargetRichText = %q, want %q", segs[1].TargetRichText, want)
	}
	// Segment 3: no tags → TargetRichText should be empty
	if segs[2].TargetRichText != "" {
		t.Errorf("seg[2].TargetRichText = %q, want empty (no tags)", segs[2].TargetRichText)
	}
}

func TestXLIFFReconstructWithInlineTags(t *testing.T) {
	// Test that Reconstruct preserves XLIFF inline tags when human_rich_text is used
	xliff := []byte(`<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="de" datatype="plaintext" original="test.txt">
    <body>
      <trans-unit id="1">
        <source>Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept> to continue</source>
        <target>Klicken Sie hier um fortzufahren</target>
      </trans-unit>
      <trans-unit id="2">
        <source>Press <ph id="2">{key}</ph> to submit</source>
      </trans-unit>
    </body>
  </file>
</xliff>`)

	p := XLIFF{}
	translations := map[string]string{
		// Rich text with XLIFF tags — should NOT be XML-escaped
		"1": `Klicken Sie <bpt id="1">&lt;b&gt;</bpt>hier<ept id="1">&lt;/b&gt;</ept> um fortzufahren`,
		// Rich text with ph tag
		"2": `Drücken Sie <ph id="2">{key}</ph> zum Absenden`,
	}

	result, err := p.Reconstruct(xliff, translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	// Make sure the tags were NOT double-escaped
	if strings.Contains(string(result), `&lt;bpt id`) {
		t.Error("bpt tag was double-escaped — should be raw XML in target")
	}

	// TU 1 replaces its existing target; TU 2 gets a new <target>. Both keep
	// their inline tags.
	segs, err := p.Parse(result)
	if err != nil {
		t.Fatalf("Parse reconstructed output: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}
	for i, key := range []string{"1", "2"} {
		if segs[i].TargetRichText != translations[key] {
			t.Errorf("seg[%d].TargetRichText = %q, want %q", i, segs[i].TargetRichText, translations[key])
		}
	}
}

func TestXLIFFReconstructPlainTextEscaped(t *testing.T) {
	// Verify that plain text (no XLIFF tags) is properly XML-escaped
	xliff := []byte(`<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="de" datatype="plaintext" original="test.txt">
    <body>
      <trans-unit id="1">
        <source>Hello</source>
        <target>Hallo</target>
      </trans-unit>
    </body>
  </file>
</xliff>`)

	p := XLIFF{}
	translations := map[string]string{
		"1": `Hallo & Welt <script>alert('xss')</script>`,
	}

	result, err := p.Reconstruct(xliff, translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	output := string(result)
	// Ampersand and angle brackets should be escaped
	if strings.Contains(output, `<script>`) {
		t.Error("plain text should be XML-escaped, but <script> was not escaped")
	}
	if !strings.Contains(output, `&amp;`) {
		t.Error("expected & to be escaped as &amp;")
	}
}

func TestHasXLIFFInlineTags(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{`Click <bpt id="1"><b></bpt>here`, true},
		{`Press <ph id="2">{key}</ph>`, true},
		{`End<ept id="1"></b></ept>`, true},
		{`Plain text without tags`, false},
		{`Contains <b>HTML</b> but not XLIFF`, false},
		{`<x id="3"/>`, true},
	}
	for _, tt := range tests {
		got := hasXLIFFInlineTags(tt.input)
		if got != tt.want {
			t.Errorf("hasXLIFFInlineTags(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
