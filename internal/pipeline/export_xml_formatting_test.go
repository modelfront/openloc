package pipeline

import (
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parsers"
)

// Generic XML has no element that means "bold", so formatting a translator adds
// in the editor is dropped on export: the delivered file gets clean text, never
// the editor's markup as literal "&lt;strong&gt;" characters. Chips (the
// document's own inline elements) still round-trip.
func TestExportGenericXMLDropsEditorFormatting(t *testing.T) {
	original := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<manual>
  <para>Hello &amp; welcome</para>
  <para>Press <b>Start</b> now</para>
</manual>
`)
	p, err := exportParser("xml", parsers.ParseOptions{InlineHTML: true}, original, nil)
	if err != nil {
		t.Fatal(err)
	}
	segs, err := p.Parse(original)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(segs) != 2 || segs[1].RichText == "" {
		t.Fatalf("segments = %+v, want two, the second with a chip", segs)
	}
	edited := "<strong>Hola</strong> &amp; <em>bienvenido</em>"
	chipped := strings.Replace(segs[1].RichText, "Press", "<em>Pulse</em>", 1)
	plain := "Pulse Start now"
	rows := []db.ListApprovedForExportRow{
		{ID: 1, SegmentKey: &segs[0].Key, ActiveText: &edited},
		{ID: 2, SegmentKey: &segs[1].Key, ActiveText: &plain, ActiveRichText: &chipped},
	}
	out, err := p.Reconstruct(original, exportTranslations(rows, "xml", true))
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "<para>Hola &amp; bienvenido</para>") {
		t.Errorf("formatted plain text exported as:\n%s", got)
	}
	if !strings.Contains(got, "<para>Pulse <b>Start</b> now</para>") {
		t.Errorf("formatted rich text exported as:\n%s", got)
	}
	if strings.Contains(got, "strong") || strings.Contains(got, "em&gt;") || strings.Contains(got, "<em>") {
		t.Errorf("editor formatting leaked into the file:\n%s", got)
	}
}

// Android strings keep editor formatting: <b> and <i> are part of the format.
func TestExportAndroidKeepsEditorFormatting(t *testing.T) {
	key := "k"
	text := "<b>Hola</b>"
	rows := []db.ListApprovedForExportRow{{ID: 1, SegmentKey: &key, ActiveText: &text}}
	if got := exportTranslations(rows, "android_xml", true)[key]; got != text {
		t.Errorf("android_xml export = %q, want %q", got, text)
	}
}
