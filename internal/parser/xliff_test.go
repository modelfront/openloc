package parser

import (
	"strings"
	"testing"
)

const testXLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:1.2" version="1.2">
  <file source-language="en" target-language="de">
    <body>
      <trans-unit id="greeting">
        <source>Hello</source>
        <target>Hallo</target>
      </trans-unit>
      <trans-unit id="farewell">
        <source>Goodbye</source>
      </trans-unit>
      <trans-unit id="skip-me" translate="no">
        <source>Do not translate</source>
      </trans-unit>
      <trans-unit id="empty">
        <source></source>
      </trans-unit>
    </body>
  </file>
</xliff>`

const testSDLXLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff xmlns:sdl="http://sdl.com/FileTypes/SdlXliff/1.0" xmlns="urn:oasis:names:tc:xliff:document:1.2" version="1.2" sdl:version="1.0">
  <file source-language="de" target-language="es">
    <body>
      <trans-unit id="tu-1">
        <source>Betriebsanleitung</source>
        <seg-source><mrk mtype="seg" mid="1">Betriebsanleitung</mrk></seg-source>
        <target><mrk mtype="seg" mid="1">Instrucciones de uso</mrk></target>
      </trans-unit>
      <trans-unit translate="no" id="tu-skip">
        <source><x id="0"/></source>
      </trans-unit>
      <trans-unit id="tu-2">
        <source>Vorwort</source>
        <seg-source><mrk mtype="seg" mid="2">Vorwort</mrk></seg-source>
        <target><mrk mtype="seg" mid="2">Prólogo</mrk></target>
      </trans-unit>
    </body>
  </file>
</xliff>`

func TestXLIFFParsePlain(t *testing.T) {
	p := XLIFF{}
	segments, err := p.Parse([]byte(testXLIFF))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Should skip translate="no" and empty source
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}

	if segments[0].Key != "greeting" || segments[0].Text != "Hello" {
		t.Errorf("segment 0: got key=%q text=%q", segments[0].Key, segments[0].Text)
	}
	if segments[0].Target != "Hallo" {
		t.Errorf("segment 0 target: got %q, want %q", segments[0].Target, "Hallo")
	}
	if segments[1].Key != "farewell" || segments[1].Text != "Goodbye" {
		t.Errorf("segment 1: got key=%q text=%q", segments[1].Key, segments[1].Text)
	}
	if segments[1].Target != "" {
		t.Errorf("segment 1 target: got %q, want empty", segments[1].Target)
	}
}

func TestXLIFFParseSDLXLIFF(t *testing.T) {
	p := XLIFF{}
	segments, err := p.Parse([]byte(testSDLXLIFF))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}

	if segments[0].Text != "Betriebsanleitung" {
		t.Errorf("segment 0 text: got %q", segments[0].Text)
	}
	if segments[0].Target != "Instrucciones de uso" {
		t.Errorf("segment 0 target: got %q, want %q", segments[0].Target, "Instrucciones de uso")
	}
	if segments[1].Text != "Vorwort" {
		t.Errorf("segment 1 text: got %q", segments[1].Text)
	}
	if segments[1].Target != "Prólogo" {
		t.Errorf("segment 1 target: got %q, want %q", segments[1].Target, "Prólogo")
	}

	// A trans-unit with a single seg mrk keys on the bare unit id.
	if segments[0].Key != "tu-1" {
		t.Errorf("segment 0 key: got %q, want %q", segments[0].Key, "tu-1")
	}
}

// assertReparsedXLIFF parses reconstructed output again and checks each
// segment's key, unchanged source text, and new target.
func assertReparsedXLIFF(t *testing.T, out []byte, want []Segment) {
	t.Helper()
	segs, err := XLIFF{}.Parse(out)
	if err != nil {
		t.Fatalf("Parse reconstructed output: %v\n%s", err, out)
	}
	if len(segs) != len(want) {
		t.Fatalf("got %d segments, want %d:\n%s", len(segs), len(want), out)
	}
	for i, w := range want {
		if segs[i].Key != w.Key || segs[i].Text != w.Text || segs[i].Target != w.Target {
			t.Errorf("segment %d: got key=%q text=%q target=%q, want key=%q text=%q target=%q",
				i, segs[i].Key, segs[i].Text, segs[i].Target, w.Key, w.Text, w.Target)
		}
	}
}

func TestXLIFFReconstructPlain(t *testing.T) {
	p := XLIFF{}
	translations := map[string]string{
		"greeting": "Hola",
		"farewell": "Adiós",
	}

	result, err := p.Reconstruct([]byte(testXLIFF), translations)
	if err != nil {
		t.Fatalf("Reconstruct error: %v", err)
	}

	assertReparsedXLIFF(t, result, []Segment{
		{Key: "greeting", Text: "Hello", Target: "Hola"},
		{Key: "farewell", Text: "Goodbye", Target: "Adiós"},
	})
	// translate="no" should be unchanged
	if !strings.Contains(string(result), "Do not translate") {
		t.Error("translate=no content should be preserved")
	}
}

func TestXLIFFReconstructSDLXLIFF(t *testing.T) {
	p := XLIFF{}
	translations := map[string]string{
		"tu-1": "Manual de instrucciones",
		"tu-2": "Prefacio",
	}

	result, err := p.Reconstruct([]byte(testSDLXLIFF), translations)
	if err != nil {
		t.Fatalf("Reconstruct error: %v", err)
	}

	assertReparsedXLIFF(t, result, []Segment{
		{Key: "tu-1", Text: "Betriebsanleitung", Target: "Manual de instrucciones"},
		{Key: "tu-2", Text: "Vorwort", Target: "Prefacio"},
	})
}

func TestXLIFFFormatDetection(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"file.xliff", "xliff"},
		{"file.xlf", "xliff"},
		{"file.sdlxliff", "xliff"},
		{"file.SDLXLIFF", "xliff"},
		{"file.mqxliff", "xliff"},
		{"file.mxliff", "xliff"},
		{"file.json", "json_i18n"},
		{"file.txt", "txt"},
	}
	for _, tt := range tests {
		got := FormatFromFilename(tt.filename)
		if got != tt.want {
			t.Errorf("FormatFromFilename(%q) = %q, want %q", tt.filename, got, tt.want)
		}
	}
}

// <alt-trans> carries its own <source> and <target> (a TM or MT candidate).
// Those must never replace the trans-unit's own source and target.
func TestXLIFFParse_AltTransDoesNotReplaceSourceOrTarget(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file original="doc.txt" source-language="en" target-language="es" datatype="plaintext">
    <body>
      <trans-unit id="line:1">
        <source>Triple tap the right earbud.</source>
        <target state="translated">Toca tres veces el auricular derecho.</target>
        <alt-trans match-quality="74" origin="tm">
          <source>Double tap the left earbud.</source>
          <target>Toca dos veces el auricular izquierdo.</target>
        </alt-trans>
      </trans-unit>
    </body>
  </file>
</xliff>`
	segs, err := XLIFF{}.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	if segs[0].Text != "Triple tap the right earbud." {
		t.Errorf("source = %q, want the trans-unit's own source", segs[0].Text)
	}
	if segs[0].Target != "Toca tres veces el auricular derecho." {
		t.Errorf("target = %q, want the trans-unit's own target", segs[0].Target)
	}
}
