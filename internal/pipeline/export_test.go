package pipeline

import (
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parsers"
	"openloc/internal/parsers/android_xml"
)

// TestExportTranslationMap_PrefersRichTextForTagAwareFormats verifies that the
// translation map builder prefers active_rich_text over active_text for XLIFF
// formats, preserving inline tags for round-trip export.
func TestExportTranslationMap_PrefersRichTextForTagAwareFormats(t *testing.T) {
	richText := `Klicken Sie <bpt id="1">&lt;b&gt;</bpt>hier<ept id="1">&lt;/b&gt;</ept> um fortzufahren`
	plainText := "Klicken Sie hier um fortzufahren"
	segKey := "1"

	rows := []db.ListApprovedForExportRow{
		{
			ID:             1,
			SegmentID:      1,
			TargetLanguage: "de",
			SourceText:     "Click here to continue",
			SegmentKey:     &segKey,
			ActiveText:     &plainText,
			ActiveRichText: &richText,
			Status:         "approved",
		},
	}

	// For XLIFF format: should use active_rich_text (has inline tags)
	translations := exportTranslations(rows, "xliff", true)
	if got := translations[segKey]; got != richText {
		t.Errorf("XLIFF export: got %q, want active_rich_text %q", got, richText)
	}

	// A format without inline markup uses active_text.
	translations = exportTranslations(rows, "markdown", true)
	if got := translations[segKey]; got != plainText {
		t.Errorf("Markdown export: got %q, want active_text %q", got, plainText)
	}

	// For XLIFF format with no active_rich_text: should fall back to active_text
	rows[0].ActiveRichText = nil
	translations = exportTranslations(rows, "xliff", true)
	if got := translations[segKey]; got != plainText {
		t.Errorf("XLIFF export (no rich text): got %q, want active_text %q", got, plainText)
	}

	// For HTML format: should also use active_rich_text (has <g> tags)
	gTagRichText := `Haga clic <g id="1" ctype="bold">aqui</g> para continuar`
	rows[0].ActiveRichText = &gTagRichText
	translations = exportTranslations(rows, "html", true)
	if got := translations[segKey]; got != gTagRichText {
		t.Errorf("HTML export: got %q, want active_rich_text %q", got, gTagRichText)
	}

	// Generic XML also round-trips inline chips from the rich text.
	translations = exportTranslations(rows, "xml", true)
	if got := translations[segKey]; got != gTagRichText {
		t.Errorf("XML export: got %q, want active_rich_text %q", got, gTagRichText)
	}
}

// TestExportTranslationMap_StripsHTMLForPlainFormats verifies that HTML
// formatting tags are stripped for formats without inline markup.
func TestExportTranslationMap_StripsHTMLForPlainFormats(t *testing.T) {
	htmlText := "This is <strong>bold</strong> text"
	segKey := "1"

	rows := []db.ListApprovedForExportRow{
		{
			SegmentKey: &segKey,
			ActiveText: &htmlText,
			Status:     "approved",
		},
	}

	// Generic XML has no element for bold either.
	for _, format := range []string{"markdown", "xml"} {
		if got := exportTranslations(rows, format, true)[segKey]; got != "This is bold text" {
			t.Errorf("%s export: got %q, want %q", format, got, "This is bold text")
		}
	}

	// HTML-capable formats keep the formatting.
	for _, format := range []string{"html", "android_xml"} {
		translations := exportTranslations(rows, format, true)
		translations = exportTranslations(rows, format, true)
		if got := translations[segKey]; got != htmlText {
			t.Errorf("%s export: got %q, want %q", format, got, htmlText)
		}
	}
}

// JSON, PO, TXT and XLSX strings that are HTML export the rich text, so a
// chip turns back into the source's tag, and the text as is when there is no
// rich text: the export's tag rule, not a strip, decides what a tag becomes.
// Plain-text strings export the text as is.
func TestExportTranslationMap_TextFormats(t *testing.T) {
	rich := `Klick <g id="1" ctype="x-html-a">hier</g>`
	plain := "Klick hier"
	formatted := "Klick <strong>hier</strong> &amp; mehr"
	key := "k"
	for _, format := range []string{"json_i18n", "po", "pot", "txt", "xlsx"} {
		rows := []db.ListApprovedForExportRow{{ID: 1, SegmentKey: &key, ActiveText: &plain, ActiveRichText: &rich}}
		if got := exportTranslations(rows, format, true)[key]; got != rich {
			t.Errorf("%s, HTML strings: got %q, want the rich text %q", format, got, rich)
		}
		if got := exportTranslations(rows, format, false)[key]; got != plain {
			t.Errorf("%s, plain-text strings: got %q, want the text %q", format, got, plain)
		}
		rows[0].ActiveText, rows[0].ActiveRichText = &formatted, nil
		for _, inlineHTML := range []bool{true, false} {
			if got := exportTranslations(rows, format, inlineHTML)[key]; got != formatted {
				t.Errorf("%s, inlineHTML=%v: got %q, want the text as is %q", format, inlineHTML, got, formatted)
			}
		}
	}
}

// Android strings hold inline markup, so export needs the rich text: from the
// plain text, Reconstruct cannot know where <b> was.
func TestExportTranslationMap_AndroidUsesRichText(t *testing.T) {
	rich := `Ana es <g id="1" ctype="bold">valiente</g> & alta`
	plain := "Ana es valiente & alta"
	key := "styled"
	rows := []db.ListApprovedForExportRow{{ID: 1, SegmentKey: &key, ActiveText: &plain, ActiveRichText: &rich}}

	if got := exportTranslations(rows, "android_xml", true)[key]; got != rich {
		t.Errorf("android_xml export = %q, want the rich text %q", got, rich)
	}
	rows[0].ActiveRichText = nil
	if got := exportTranslations(rows, "android_xml", true)[key]; got != plain {
		t.Errorf("android_xml export without rich text = %q, want %q", got, plain)
	}
}

// An Android file whose stored segments hold the raw string content is
// exported with RawText, so its stored escapes are not escaped again; a file
// stored decoded, and every other format, keeps the plain parser.
func TestExportParser_AndroidStoredRaw(t *testing.T) {
	original := []byte(`<resources><string name="a">It\'s</string></resources>`)
	key := "a"
	row := func(source string) []db.ListApprovedForExportRow {
		return []db.ListApprovedForExportRow{{ID: 1, SegmentKey: &key, SourceText: source}}
	}

	p, err := exportParser("android_xml", parsers.ParseOptions{InlineHTML: true}, original, row(`It\'s`))
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := p.(android_xml.AndroidXML); !ok || !a.RawText {
		t.Errorf("raw stored text: parser = %#v, want AndroidXML{RawText: true}", p)
	}
	p, err = exportParser("android_xml", parsers.ParseOptions{InlineHTML: true}, original, row("It's"))
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := p.(android_xml.AndroidXML); !ok || a.RawText {
		t.Errorf("decoded stored text: parser = %#v, want AndroidXML{}", p)
	}
	p, err = exportParser("json_i18n", parsers.ParseOptions{InlineHTML: true}, []byte(`{"a": "x"}`), row("x"))
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := parsers.GetParser("json_i18n"); p != want {
		t.Errorf("json: parser = %#v, want the format's own parser %#v", p, want)
	}
}
