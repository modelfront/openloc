package pptx

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"openloc/internal/parsers/common"
	pt "openloc/internal/parsers/pptx/pptxtest"
)

// With nothing translated, the export is the upload, byte for byte.
func TestPPTXReconstruct_NoTranslationsIsIdentical(t *testing.T) {
	content := pt.Build(pt.JourneyDeck())
	out := pptxExport(t, content, nil)
	if !bytes.Equal(out, content) {
		t.Error("export without translations changed the file")
	}
}

// A partial translation rewrites only the paragraphs it translates: every
// other part, the picture included, is copied raw, and an untouched
// paragraph keeps its exact bytes.
func TestPPTXReconstruct_TouchesOnlyTranslatedParagraphs(t *testing.T) {
	content := pt.Build(pt.JourneyDeck())
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	title := pptxSegment(t, segs, "Quarterly results")
	out := pptxExport(t, content, map[string]string{title.Key: "Résultats trimestriels"})

	before, after := zipEntries(t, content), zipEntries(t, out)
	if len(before) != len(after) {
		t.Fatalf("export has %d entries, want %d", len(after), len(before))
	}
	for name, raw := range before {
		if name == "ppt/slides/slide1.xml" {
			continue
		}
		if !bytes.Equal(raw, after[name]) {
			t.Errorf("%s changed, but nothing in it was translated", name)
		}
	}
	slide1 := pptxPart(t, out, "ppt/slides/slide1.xml")
	if !strings.Contains(slide1, "Résultats trimestriels") {
		t.Error("translation missing from slide 1")
	}
	original := pptxParagraphXML(t, pptxPart(t, content, "ppt/slides/slide1.xml"), "Revenue grew")
	if !strings.Contains(slide1, original) {
		t.Error("the untranslated subtitle's paragraph was rewritten")
	}
}

// zipEntries returns each entry's raw (compressed) bytes.
func zipEntries(t *testing.T, content []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.OpenRaw()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = b
	}
	return out
}

// Every segment of the export reads back as its translation, under the same
// key, and every rebuilt part is still well-formed XML.
func TestPPTXReconstruct_RoundTrip(t *testing.T) {
	content := pt.Build(pt.JourneyDeck())
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	translations := map[string]string{}
	for _, s := range segs {
		tr := "FR " + s.Text
		if s.RichText != "" {
			tr = "FR " + s.RichText
		}
		translations[s.Key] = tr
	}
	out := pptxExport(t, content, translations)
	again, err := PPTX{}.Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(again) != len(segs) {
		t.Fatalf("re-parse gave %d segments, want %d", len(again), len(segs))
	}
	for i, s := range again {
		if s.Key != segs[i].Key {
			t.Errorf("segment %d key %s, want %s", i, s.Key, segs[i].Key)
		}
		if want := "FR " + segs[i].Text; s.Text != want {
			t.Errorf("segment %d text %q, want %q", i, s.Text, want)
		}
		if segs[i].RichText != "" && s.RichText != "FR "+segs[i].RichText {
			t.Errorf("segment %d rich %q, want %q", i, s.RichText, "FR "+segs[i].RichText)
		}
	}
	for name := range zipEntries(t, out) {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			if _, err := common.ParseXMLDoc([]byte(pptxPart(t, out, name))); err != nil {
				t.Errorf("%s is not well-formed: %v", name, err)
			}
		}
	}
}

// A chip brings back the formatting of the run it came from, wherever the
// translator put it; text outside chips gets the base formatting; a deleted
// chip leaves its text in the base style.
func TestPPTXReconstruct_ChipsRestoreRunProperties(t *testing.T) {
	content := pt.Build(slide(pt.TextBox(2,
		pt.P(pt.R(`lang="en-US" sz="1800"`, "Click "), pt.R(`lang="en-US" sz="1800" b="1"`, "Save"),
			pt.R(`lang="en-US" sz="1800"`, " or "), pt.R(`lang="en-US" sz="1800" i="1"`, "Cancel")),
		pt.P(pt.R(`lang="en-US"`, "Keep "), pt.R(`lang="en-US" b="1"`, "this"), pt.R(`lang="en-US"`, " plain")),
	)))
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	reorder := pptxSegment(t, segs, "Click Save or Cancel")
	drop := pptxSegment(t, segs, "Keep this plain")
	out := pptxExport(t, content, map[string]string{
		reorder.Key: `<g id="2">Annuler</g> ou <g id="1">Enregistrer</g>`,
		drop.Key:    `Garder ceci`,
	})
	slide1 := pptxPart(t, out, "ppt/slides/slide1.xml")
	for _, want := range []string{
		`<a:r><a:rPr lang="fr-CA" sz="1800" i="1"/><a:t>Annuler</a:t></a:r>`,
		`<a:r><a:rPr lang="fr-CA" sz="1800"/><a:t> ou </a:t></a:r>`,
		`<a:r><a:rPr lang="fr-CA" sz="1800" b="1"/><a:t>Enregistrer</a:t></a:r>`,
		`<a:r><a:rPr lang="fr-CA"/><a:t>Garder ceci</a:t></a:r>`,
	} {
		if !strings.Contains(slide1, want) {
			t.Errorf("slide is missing %s\n%s", want, slide1)
		}
	}
	if strings.Contains(slide1, `b="1"/><a:t>ceci`) {
		t.Error("a deleted chip's formatting reached the text")
	}
}

// The editor's Shift+Enter adds a line break chip with an id the source never
// had; it, and a newline in the text, become line breaks on the slide. Text
// that looks like markup is escaped, never written as XML.
func TestPPTXReconstruct_LineBreaksAndEscaping(t *testing.T) {
	content := pt.Build(slide(pt.TextBox(2, pt.P(pt.R("", "Quarterly"), pt.Br(), pt.R("", "results")))))
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	out := pptxExport(t, content, map[string]string{
		segs[0].Key: `A <x id="1" ctype="lb"/>B<x id="7" ctype="lb"/>C` + "\nD & <E> <b>",
	})
	got := pptxParagraphXML(t, pptxPart(t, out, "ppt/slides/slide1.xml"), "<a:t>A </a:t>")
	want := `<a:p><a:r><a:t>A </a:t></a:r><a:br><a:rPr lang="en-US"/></a:br><a:r><a:t>B</a:t></a:r><a:br/>` +
		`<a:r><a:t>C</a:t></a:r><a:br/><a:r><a:t>D &amp; &lt;E&gt; &lt;b&gt;</a:t></a:r></a:p>`
	if got != want {
		t.Errorf("paragraph:\n got %s\nwant %s", got, want)
	}
}

// A paragraph with one translated sentence keeps the other in the source,
// in its source language.
func TestPPTXReconstruct_UntranslatedSentenceKeepsSource(t *testing.T) {
	content := pt.Build(slide(pt.TextBox(2, pt.P(pt.R(`lang="en-US"`, "First point. Second point.")))))
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	out := pptxExport(t, content, map[string]string{segs[1].Key: "Deuxième point."})
	got := pptxParagraphXML(t, pptxPart(t, out, "ppt/slides/slide1.xml"), "First point.")
	want := `<a:p><a:r><a:rPr lang="en-US"/><a:t>First point. </a:t></a:r><a:r><a:rPr lang="fr-CA"/><a:t>Deuxième point.</a:t></a:r></a:p>`
	if got != want {
		t.Errorf("paragraph:\n got %s\nwant %s", got, want)
	}
}

// Translated text in the source language is marked with the target
// language, so PowerPoint spell-checks it in the right one. Text tagged with
// another language, or with none, keeps what it had. Spell-check state is
// dropped so PowerPoint checks the new text again.
func TestPPTXReconstruct_LanguageTags(t *testing.T) {
	content := pt.Build(slide(pt.TextBox(2,
		pt.P(pt.R(`lang="en-GB" err="1" dirty="0"`, "Colour"), `<a:endParaRPr lang="en-US" dirty="0"/>`),
		pt.P(pt.R(`lang="de-DE"`, "Kindergarten")),
		pt.P(pt.R(`sz="1200"`, "Untagged")),
	)))
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	out := pptxExport(t, content, map[string]string{
		segs[0].Key: "Couleur",
		segs[1].Key: "Jardin d'enfants",
		segs[2].Key: "Sans étiquette",
	})
	slide1 := pptxPart(t, out, "ppt/slides/slide1.xml")
	for _, want := range []string{
		`<a:p><a:r><a:rPr lang="fr-CA"/><a:t>Couleur</a:t></a:r><a:endParaRPr lang="fr-CA"/></a:p>`,
		`<a:p><a:r><a:rPr lang="de-DE"/><a:t>Jardin d'enfants</a:t></a:r></a:p>`,
		`<a:p><a:r><a:rPr sz="1200"/><a:t>Sans étiquette</a:t></a:r></a:p>`,
	} {
		if !strings.Contains(slide1, want) {
			t.Errorf("slide is missing %s\n%s", want, slide1)
		}
	}

	plain, err := PPTX{}.Reconstruct(content, map[string]string{segs[0].Key: "Couleur"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pptxPart(t, plain, "ppt/slides/slide1.xml"), `<a:rPr lang="en-GB"/><a:t>Couleur`) {
		t.Error("Reconstruct without the job's languages must leave lang alone")
	}
}

// Alt text is an attribute: quotes and newlines must be escaped so the value
// survives, and chips never reach it.
func TestPPTXReconstruct_AltText(t *testing.T) {
	content := pt.Build(pt.Deck{Slides: []pt.Slide{{Shapes: pt.Picture(2, "A chart. It shows growth."), Image: pt.PNG}}})
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 || segs[0].Context != "slide:1/alt" {
		t.Fatalf("segments = %+v, want two alt text sentences", segs)
	}
	out := pptxExport(t, content, map[string]string{
		segs[0].Key: `Un "graphique".`,
		segs[1].Key: "Il montre<x id=\"1\"/> la\ncroissance.",
	})
	again, err := PPTX{}.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := again[0].Text+" "+again[1].Text, "Un \"graphique\". Il montre la\ncroissance."; got != want {
		t.Errorf("alt text reads back %q, want %q", got, want)
	}
	if !strings.Contains(pptxPart(t, out, "ppt/slides/slide1.xml"), `descr="Un &quot;graphique&quot;. Il montre la&#xA;croissance."`) {
		t.Error("alt text not escaped as an attribute")
	}
}

// The editor numbers a line break it adds as the highest id in the
// translation plus one. When the translation dropped a source chip, that id
// can belong to the source's slide-number field; ctype="lb" still means a
// line break, never the field.
func TestPPTXReconstruct_EditorLineBreakIDCollision(t *testing.T) {
	content := pt.Build(slide(pt.TextBox(2, pt.P(pt.R("", "Slide "), pt.SlideNumber(3), pt.R("", " of ten")))))
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if segs[0].RichText != `Slide <x id="1" ctype="x-xml-fld"/> of ten` {
		t.Fatalf("source rich %q", segs[0].RichText)
	}
	out := pptxExport(t, content, map[string]string{segs[0].Key: `Diapositive<x id="1" ctype="lb"/>sur dix`})
	got := pptxParagraphXML(t, pptxPart(t, out, "ppt/slides/slide1.xml"), "Diapositive")
	if !strings.Contains(got, `<a:t>Diapositive</a:t></a:r><a:br/><a:r><a:t>sur dix</a:t>`) || strings.Contains(got, "<a:fld") {
		t.Errorf("the editor's line break must be a:br, not the dropped field:\n%s", got)
	}
}

// Runs that differ only by language merge into one, which takes the
// properties of its longest run. A short run in another language at the
// start must not decide the language of the whole translation.
func TestPPTXReconstruct_MixedLanguageRunsTakeTheLongest(t *testing.T) {
	content := pt.Build(slide(pt.TextBox(2, pt.P(
		pt.R(`lang="de-DE"`, "Hallo, "),
		pt.R(`lang="en-US"`, "and welcome to the quarterly meeting"),
	))))
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if segs[0].RichText != "" {
		t.Fatalf("runs differing only by language must merge, got %q", segs[0].RichText)
	}
	out := pptxExport(t, content, map[string]string{segs[0].Key: "Bonjour et bienvenue à la réunion"})
	got := pptxParagraphXML(t, pptxPart(t, out, "ppt/slides/slide1.xml"), "Bonjour")
	if !strings.Contains(got, `<a:rPr lang="fr-CA"/><a:t>Bonjour et bienvenue à la réunion</a:t>`) {
		t.Errorf("translation must be marked fr-CA:\n%s", got)
	}
}
