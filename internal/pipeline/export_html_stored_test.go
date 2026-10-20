package pipeline

import (
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parser"
)

// The HTML export reads each stored translation's chips by the numbering its
// segment's stored source rich text uses, so a translation stored with only
// formatting elements numbered keeps every link on its own href.
func TestExportHTMLReadsChipsByTheStoredSource(t *testing.T) {
	original := []byte(`<html><body><p><small>N</small> <a href="/x">X</a> and <a href="/y">Y</a>.</p></body></html>`)
	key := "seg:1"
	storedSource := `N <g id="1" ctype="x-html-a" original_markup="&lt;a href=&quot;/x&quot;&gt;">X</g> and <g id="2" ctype="x-html-a" original_markup="&lt;a href=&quot;/y&quot;&gt;">Y</g>.`
	text := "Ye y Equis."
	rich := `<g id="2" ctype="x-html-a">Ye</g> y <g id="1" ctype="x-html-a">Equis</g>.`
	rows := []db.ListApprovedForExportRow{{ID: 1, SegmentKey: &key, SourceText: "N X and Y.", SourceRichText: &storedSource, ActiveText: &text, ActiveRichText: &rich}}

	p, err := exportParser("html", parser.ParseOptions{InlineHTML: true}, original, rows)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Reconstruct(original, exportTranslations(rows, "html", true))
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if want := `<p><a href="/y">Ye</a> y <a href="/x">Equis</a>.</p>`; !strings.Contains(string(out), want) {
		t.Fatalf("export:\n%s\nwant it to contain:\n%s", out, want)
	}
}
