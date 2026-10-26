package xliff2

import (
	"os"
	"strings"
	"testing"

	"openloc/internal/parsers/common"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// segmentByKey indexes parsed segments by key, failing on a duplicate.
func segmentByKey(t *testing.T, segs []common.Segment) map[string]common.Segment {
	t.Helper()
	m := map[string]common.Segment{}
	for _, s := range segs {
		if _, dup := m[s.Key]; dup {
			t.Fatalf("duplicate key %q", s.Key)
		}
		m[s.Key] = s
	}
	return m
}

func TestXLIFF2ParsePlain(t *testing.T) {
	segs, err := XLIFF2{}.Parse(readFixture(t, "basic.xlf"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// translate="no" and blank sources are skipped; the ignorable is not a segment.
	want := []common.Segment{
		{Key: "greeting", Text: "Hello", Target: "Hallo", Position: 0},
		{Key: "farewell", Text: "Goodbye", Position: 1},
		{Key: "multi:a", Text: "First sentence.", Position: 2},
		{Key: "multi:b", Text: "Second sentence.", Target: "Zweiter Satz.", Position: 3},
	}
	if len(segs) != len(want) {
		t.Fatalf("got %d segments, want %d: %+v", len(segs), len(want), segs)
	}
	for i, w := range want {
		if segs[i] != w {
			t.Errorf("segment %d = %+v, want %+v", i, segs[i], w)
		}
	}
}

func TestXLIFF2ParseInlineChips(t *testing.T) {
	segs, err := XLIFF2{}.Parse(readFixture(t, "inline.xlf"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	m := segmentByKey(t, segs)
	tests := []struct {
		key, text, rich string
	}{
		// A <pc> is a span chip; a <ph> is a standalone chip with no text.
		{"u1", "Click here to  continue.", `Click <g id="1">here</g> to <x id="2"/> continue.`},
		// An <sc>/<ec> pair is a span chip too.
		{"u2", "Press Enter now.", `Press <g id="3">Enter</g> now.`},
		{"u3", "Read terms.", `Read <g id="m1">terms</g>.`},
	}
	for _, tt := range tests {
		s, ok := m[tt.key]
		if !ok {
			t.Fatalf("no segment %q", tt.key)
		}
		if s.Text != tt.text {
			t.Errorf("%s: Text = %q, want %q", tt.key, s.Text, tt.text)
		}
		if s.RichText != tt.rich {
			t.Errorf("%s: RichText = %q, want %q", tt.key, s.RichText, tt.rich)
		}
	}
	if got := m["u2"].TargetRichText; got != `Drücken Sie <g id="3">Eingabe</g> jetzt.` {
		t.Errorf("u2 TargetRichText = %q", got)
	}
}

func TestXLIFF2ReconstructPlain(t *testing.T) {
	doc := readFixture(t, "basic.xlf")
	out, err := XLIFF2{}.Reconstruct(doc, map[string]string{
		"greeting": "Hallo Welt",
		"farewell": "Tschüss",
	})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"<target>Hallo Welt</target>",
		// The farewell had no target: one is added right after its source.
		"<source>Goodbye</source><target>Tschüss</target>",
		// Untouched segments keep their bytes.
		"<target>Zweiter Satz.</target>",
		"<source>Do not translate</source>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "Hallo</target>") {
		t.Errorf("old target was not replaced:\n%s", got)
	}
	// The output is still XLIFF 2 and parses back with the new targets.
	segs, err := XLIFF2{}.Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	m := segmentByKey(t, segs)
	if m["greeting"].Target != "Hallo Welt" || m["farewell"].Target != "Tschüss" {
		t.Errorf("reparsed targets = %q, %q", m["greeting"].Target, m["farewell"].Target)
	}
}

func TestXLIFF2ReconstructSkipsTranslateNo(t *testing.T) {
	doc := readFixture(t, "basic.xlf")
	out, err := XLIFF2{}.Reconstruct(doc, map[string]string{"skip-me": "Nicht übersetzen"})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if string(out) != string(doc) {
		t.Errorf("translate=\"no\" unit was rewritten")
	}
}

func TestXLIFF2ReconstructChipsMoveButKeepSourceMarkup(t *testing.T) {
	doc := readFixture(t, "inline.xlf")
	out, err := XLIFF2{}.Reconstruct(doc, map[string]string{
		// The translator moved the span and the standalone code.
		"u1": `Klicken Sie <g id="1">hier</g>, um <x id="2"/> fortzufahren.`,
		// The span's text changed; its <sc>/<ec> are the source's own bytes.
		"u2": `Drücken Sie <g id="3">Eingabe</g> jetzt.`,
	})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		`<target>Klicken Sie <pc id="1" dataRefStart="d1" dataRefEnd="d2">hier</pc>, um <ph id="2" dataRef="d3"/> fortzufahren.</target>`,
		`<target>Drücken Sie <sc id="3" dataRef="d4"/>Eingabe<ec startRef="3" dataRef="d5"/> jetzt.</target>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
}

func TestXLIFF2ReconstructRoundTrip(t *testing.T) {
	doc := readFixture(t, "inline.xlf")
	segs, err := XLIFF2{}.Parse(doc)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Exporting each segment's own chip form must reproduce its source markup.
	translations := map[string]string{}
	for _, s := range segs {
		translations[s.Key] = s.RichText
	}
	out, err := XLIFF2{}.Reconstruct(doc, translations)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	reparsed, err := XLIFF2{}.Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	m := segmentByKey(t, reparsed)
	if got := m["u1"].TargetRichText; got != `Click <g id="1">here</g> to <x id="2"/> continue.` {
		t.Errorf("u1 round-trip rich = %q", got)
	}
}

func TestXLIFF2ReconstructEscapesPlainText(t *testing.T) {
	doc := readFixture(t, "basic.xlf")
	out, err := XLIFF2{}.Reconstruct(doc, map[string]string{"greeting": "A & B < C"})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if !strings.Contains(string(out), "<target>A &amp; B &lt; C</target>") {
		t.Errorf("plain text not escaped:\n%s", out)
	}
}

// Each refusal is a user-facing ExportError naming the problem, so the export
// stops with a message instead of writing invalid XLIFF.
func TestXLIFF2ReconstructRefusesBadChips(t *testing.T) {
	tests := []struct {
		name, rich, want string
	}{
		{"chip not in source", `Klicken <g id="9">hier</g>`, `"9" is not in the source`},
		{"span left open", `Klicken <g id="1">hier`, `"1" has no closing tag`},
		{"chip twice", `<x id="2"/> und <x id="2"/>`, `"2" appears more than once`},
		{"span id used for a standalone", `Klicken <x id="1"/>`, `"1" is not in the source`},
		{"closing with no opening", `Klicken hier</g>`, "a closing inline tag has no opening tag"},
	}
	doc := readFixture(t, "inline.xlf")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := XLIFF2{}.Reconstruct(doc, map[string]string{"u1": tt.rich})
			if err == nil {
				t.Fatalf("no error for %q", tt.rich)
			}
			if _, ok := err.(*common.ExportError); !ok {
				t.Errorf("error %T is not an ExportError", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestXLIFF2TargetLanguage(t *testing.T) {
	lang, err := XLIFF2TargetLanguage(readFixture(t, "basic.xlf"))
	if err != nil || lang != "de" {
		t.Errorf("XLIFF2TargetLanguage = %q, %v; want \"de\"", lang, err)
	}
	none, err := XLIFF2TargetLanguage([]byte(`<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en"></xliff>`))
	if err != nil || none != "" {
		t.Errorf("no trgLang: got %q, %v; want \"\"", none, err)
	}
}

func TestXLIFF2DetectFormat(t *testing.T) {
	v2 := readFixture(t, "basic.xlf")
	v12 := []byte(`<?xml version="1.0"?><xliff version="1.2"><file source-language="en" target-language="de"><body/></file></xliff>`)
	resources := []byte(`<?xml version="1.0"?><resources><string name="a">A</string></resources>`)
	tests := []struct {
		name    string
		file    string
		content []byte
		want    string
	}{
		{"2.x xlf", "messages.xlf", v2, "xliff2"},
		{"2.x xliff", "messages.xliff", v2, "xliff2"},
		{"2.x inside .xml", "messages.xml", v2, "xliff2"},
		{"1.2 xlf stays xliff", "messages.xlf", v12, "xliff"},
		{"android resources stay android_xml", "strings.xml", resources, "android_xml"},
	}
	for _, tt := range tests {
		if got := common.DetectFormat(tt.file, tt.content); got != tt.want {
			t.Errorf("%s: DetectFormat(%q) = %q, want %q", tt.name, tt.file, got, tt.want)
		}
	}
}

// A module's own <source> and <target> (here, Translation Candidates) are not
// the segment's: parse and export both ignore them. A module element inside
// a <source> keeps its text but is not a chip.
func TestXLIFF2ModuleElementsAreNotSegmentParts(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" xmlns:mtc="urn:oasis:names:tc:xliff:matches:2.0" xmlns:its="http://www.w3.org/2005/11/its" version="2.1" srcLang="en" trgLang="de">
  <file id="f1">
    <unit id="u1">
      <mtc:matches>
        <mtc:match id="m1"><source>Wrong</source><target>Falsch</target></mtc:match>
      </mtc:matches>
      <segment>
        <source>Say <its:span>this</its:span> now.</source>
        <mtc:matches><mtc:match><source>Hi</source><target>Hi-de</target></mtc:match></mtc:matches>
      </segment>
    </unit>
  </file>
</xliff>`)
	segs, err := XLIFF2{}.Parse(doc)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1: %+v", len(segs), segs)
	}
	if segs[0].Text != "Say this now." || segs[0].RichText != "" || segs[0].Target != "" {
		t.Errorf("segment = %+v, want plain \"Say this now.\" with no chips or target", segs[0])
	}

	out, err := XLIFF2{}.Reconstruct(doc, map[string]string{"u1": "Sag jetzt dies."})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"<target>Sag jetzt dies.</target>",
		// The candidates keep their own target, untouched.
		"<target>Falsch</target>",
		"<target>Hi-de</target>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
	if strings.Count(got, "<target>") != 3 {
		t.Errorf("want 3 targets (candidate, candidate, segment), got:\n%s", got)
	}
}
