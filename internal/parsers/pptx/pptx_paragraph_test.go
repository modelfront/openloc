package pptx

import (
	"reflect"
	"strings"
	"testing"

	"openloc/internal/parsers/common"
	pt "openloc/internal/parsers/pptx/pptxtest"
)

// PowerPoint splits text into runs for spell-check marks and language tags.
// Those runs look the same, so the translator sees one plain sentence, not a
// row of tags.
func TestPPTXParagraph_NoiseRunsMerge(t *testing.T) {
	segs := pptxSegments(t, slide(pt.TextBox(2, pt.P(
		pt.R(`lang="en-US" dirty="0"`, "The new "),
		pt.R(`lang="en-US" err="1" dirty="0"`, "ofice"),
		pt.R(`lang="en-GB" noProof="1" smtClean="0"`, " opens in May"),
	))))
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1", len(segs))
	}
	if segs[0].Text != "The new ofice opens in May" || segs[0].RichText != "" {
		t.Errorf("text %q rich %q, want plain text and no chips", segs[0].Text, segs[0].RichText)
	}
}

// Text that looks different becomes a chip named for what sets it apart.
func TestPPTXParagraph_ChipCtypes(t *testing.T) {
	link := `<a:r><a:rPr lang="en-US"><a:hlinkClick r:id="rId9"/></a:rPr><a:t>our site</a:t></a:r>`
	segs := pptxSegments(t, slide(pt.TextBox(2,
		pt.P(pt.R(`lang="en-US"`, "Read "), pt.R(`lang="en-US" b="1"`, "this"), pt.R(`lang="en-US"`, " first")),
		pt.P(pt.R(`lang="en-US"`, "Read "), pt.R(`lang="en-US" i="1"`, "this"), pt.R(`lang="en-US"`, " first")),
		pt.P(pt.R(`lang="en-US"`, "Read "), pt.R(`lang="en-US" u="sng"`, "this"), pt.R(`lang="en-US"`, " first")),
		pt.P(pt.R(`lang="en-US"`, "Visit "), link, pt.R(`lang="en-US"`, " today")),
		pt.P(pt.R(`lang="en-US"`, "Read "), pt.R(`lang="en-US" sz="2800"`, "this"), pt.R(`lang="en-US"`, " first")),
		pt.P(pt.R(`lang="en-US"`, "Read "), pt.R(`lang="en-US" b="1" i="1"`, "this"), pt.R(`lang="en-US"`, " first")),
	)))
	want := []string{
		`Read <g id="1" ctype="bold">this</g> first`,
		`Read <g id="1" ctype="italic">this</g> first`,
		`Read <g id="1" ctype="underlined">this</g> first`,
		`Visit <g id="1" ctype="x-html-a">our site</g> today`,
		`Read <g id="1" ctype="x-html-span">this</g> first`,
		`Read <g id="1" ctype="x-html-span">this</g> first`,
	}
	var got []string
	for _, s := range segs {
		got = append(got, s.RichText)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rich text:\n got %q\nwant %q", got, want)
	}
}

// The base style is the one covering the most text, so in a bold paragraph
// with one plain word, the plain word is the chip.
func TestPPTXParagraph_BaseIsMostText(t *testing.T) {
	segs := pptxSegments(t, slide(pt.TextBox(2, pt.P(
		pt.R(`b="1"`, "Sales rose "),
		pt.R("", "sharply"),
		pt.R(`b="1"`, " this quarter"),
	))))
	if want := `Sales rose <g id="1" ctype="x-html-span">sharply</g> this quarter`; segs[0].RichText != want {
		t.Errorf("rich = %q, want %q", segs[0].RichText, want)
	}
}

// A sentence never ends inside a chip, and chip ids start at 1 in every
// sentence, as in every other format.
func TestPPTXParagraph_SentencesRespectChips(t *testing.T) {
	segs := pptxSegments(t, slide(pt.TextBox(2, pt.P(
		pt.R("", "See "),
		pt.R(`b="1"`, "Fig. 2. Then"),
		pt.R("", " decide. Next "),
		pt.R(`i="1"`, "step"),
		pt.R("", " follows."),
	))))
	got := []string{}
	for _, s := range segs {
		got = append(got, s.RichText+"|"+s.Text)
	}
	want := []string{
		`See <g id="1" ctype="bold">Fig. 2. Then</g> decide.|See Fig. 2. Then decide.`,
		`Next <g id="1" ctype="italic">step</g> follows.|Next step follows.`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sentences:\n got %q\nwant %q", got, want)
	}
}

// Whitespace and line breaks at a paragraph's edges are not part of the
// segment.
func TestPPTXParagraph_EdgesTrimmed(t *testing.T) {
	segs := pptxSegments(t, slide(pt.TextBox(2, pt.P(
		pt.R("", "  "), pt.Br(), pt.R("", "Welcome "), pt.Br(),
	))))
	if len(segs) != 1 || segs[0].Text != "Welcome" || segs[0].RichText != "" {
		t.Errorf("segments = %+v, want one plain %q", segs, "Welcome")
	}
}

// A run that holds only whitespace looks the same in any style, so it never
// becomes a chip. A newline written inside a run's text is a line break,
// exactly like a:br, so it survives a round trip as one.
func TestPPTXParagraph_WhitespaceAndNewlineRuns(t *testing.T) {
	content := pt.Build(slide(pt.TextBox(2,
		pt.P(pt.R(`sz="4200"`, "Acme Trading"), pt.R(`sz="1650"`, "\n"), pt.R(`sz="4200"`, " Annual review")),
		pt.P(pt.R("", "Hello"), pt.R(`b="1"`, " "), pt.R("", "world"), pt.R(`i="1"`, " "), pt.R(`b="1"`, "now")),
	)))
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, s := range segs {
		got = append(got, s.Text+"|"+s.RichText)
	}
	want := []string{
		"Acme Trading\n Annual review|" + `Acme Trading<x id="1" ctype="lb"/> Annual review`,
		"Hello world now|" + `Hello world <g id="1" ctype="bold">now</g>`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segments:\n got %q\nwant %q", got, want)
	}

	out := pptxExport(t, content, map[string]string{segs[0].Key: `Acme<x id="1" ctype="lb"/>Rapport`})
	again, err := PPTX{}.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].RichText != `Acme<x id="1" ctype="lb"/>Rapport` {
		t.Errorf("after export the line break reads back as %q", again[0].RichText)
	}
	slide1 := pptxPart(t, out, "ppt/slides/slide1.xml")
	if !strings.Contains(slide1, `<a:t>Acme</a:t></a:r><a:br><a:rPr sz="1650"/></a:br>`) {
		t.Errorf("the newline must come back as an a:br with its run's properties:\n%s", slide1)
	}
}

// Text with no letters or digits — a bullet drawn as its own text box, an
// arrow, a dash — has nothing to translate and is left in the deck as it is.
func TestPPTXParagraph_SkipsTextWithoutLettersOrDigits(t *testing.T) {
	content := pt.Build(pt.Deck{Slides: []pt.Slide{{
		Shapes: pt.TextBox(2, pt.P(pt.R("", "•")), pt.P(pt.R("", " → ")), pt.P(pt.R("", "12%")), pt.P(pt.R("", "①")), pt.P(pt.R("", "Next"))) +
			pt.Picture(3, "—"),
		Image: pt.PNG,
	}}})
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pptxTexts(segs), []string{"12%", "①", "Next"}; !reflect.DeepEqual(got, want) {
		t.Errorf("texts = %q, want %q", got, want)
	}
	out := pptxExport(t, content, map[string]string{segs[2].Key: "Suivant"})
	slide1 := pptxPart(t, out, "ppt/slides/slide1.xml")
	for _, want := range []string{"<a:t>•</a:t>", "<a:t> → </a:t>", `descr="—"`, "<a:t>Suivant</a:t>"} {
		if !strings.Contains(slide1, want) {
			t.Errorf("export is missing %s", want)
		}
	}
}

// PowerPoint splits sentences exactly as the other formats do, so a sentence
// segments the same in a deck and in a Word file and the translation memory
// matches across them. Alt text follows the same rule.
func TestPPTXParagraph_SentencesMatchOtherFormats(t *testing.T) {
	texts := []string{
		"Owner: Sales",
		"Note: Save first. Then close.",
		"Revenue grew 12% in Q3. Costs fell, e.g. travel. Margins rose!",
		"See Fig. 2 for details. Is it ready? Yes.",
	}
	var paras []string
	var want []string
	for _, text := range texts {
		paras = append(paras, pt.P(pt.R("", text)))
		for _, r := range common.SplitSentences(text, nil) {
			want = append(want, text[r.Start:r.End])
		}
	}
	segs := pptxSegments(t, pt.Deck{Slides: []pt.Slide{{
		Shapes: pt.TextBox(2, paras...) + pt.Picture(3, texts[1]),
		Image:  pt.PNG,
	}}})
	want = append(want, "Note:", "Save first.", "Then close.")
	if got := pptxTexts(segs); !reflect.DeepEqual(got, want) {
		t.Errorf("texts = %q, want %q", got, want)
	}
}
