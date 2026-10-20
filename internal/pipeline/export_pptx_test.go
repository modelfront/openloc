package pipeline

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parser"
	pt "openloc/internal/parser/pptxtest"
)

// A PowerPoint export writes the stored rich text, so a chip brings back its
// run's formatting; marks the text with the job's target language, which only
// the project knows; and drops editor formatting, which has no place on a
// slide.
func TestExportFile_PPTXChipsLanguageAndFormatting(t *testing.T) {
	upload := pt.Build(pt.Deck{Slides: []pt.Slide{{Shapes: pt.TextBox(2, pt.P(
		pt.R(`lang="en-US"`, "Click "),
		pt.R(`lang="en-US" b="1"`, "Save"),
		pt.R(`lang="en-US"`, " now"),
	))}}})
	segs, err := parser.PPTX{}.Parse(upload)
	if err != nil {
		t.Fatal(err)
	}
	if segs[0].RichText != `Click <g id="1" ctype="bold">Save</g> now` {
		t.Fatalf("source rich text %q", segs[0].RichText)
	}

	const tenantID, fileID = 1, 1
	mq := newMockQuerier()
	mq.addFileProject(fileID, 1, nil, "en", []string{"fr-ca"})
	file := mq.files[fileID]
	file.Format = db.FileFormatPptx
	file.Name = "deck.pptx"
	mq.files[fileID] = file
	key, text := segs[0].Key, "Cliquez Enregistrer maintenant"
	rich := `<strong>Cliquez</strong> <g id="1" ctype="bold">Enregistrer</g> maintenant`
	mq.approvedForExport[fileLangKey(fileID, "fr-ca")] = []db.ListApprovedForExportRow{
		{SegmentKey: &key, ActiveText: &text, ActiveRichText: &rich, Status: "approved"},
	}
	svc := NewService(mq, &passthroughTxRunner{q: mq}, memStorage(upload))

	out, name, err := svc.ExportFile(context.Background(), tenantID, fileID, "fr-ca")
	if err != nil {
		t.Fatalf("ExportFile: %v", err)
	}
	if name != "deck.pptx" {
		t.Errorf("name = %q", name)
	}
	slide := pt.ReadPart(t, out, "ppt/slides/slide1.xml")
	want := `<a:r><a:rPr lang="fr-CA"/><a:t>Cliquez </a:t></a:r><a:r><a:rPr lang="fr-CA" b="1"/><a:t>Enregistrer</a:t></a:r>` +
		`<a:r><a:rPr lang="fr-CA"/><a:t> maintenant</a:t></a:r>`
	if !strings.Contains(slide, want) {
		t.Errorf("slide is missing %s\n%s", want, slide)
	}
	if strings.Contains(slide, "strong") {
		t.Error("editor formatting reached the slide")
	}
}
