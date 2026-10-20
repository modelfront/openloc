package parser

import (
	"context"
	"errors"
	"strings"
	"testing"

	pt "openloc/internal/parser/pptxtest"
)

const (
	saDoc   = "{00000000-0000-0000-0000-000000000001}"
	saPlan  = "{00000000-0000-0000-0000-000000000002}"
	saShip  = "{00000000-0000-0000-0000-000000000003}"
	saPresA = "{00000000-0000-0000-0000-000000000012}"
	saPresB = "{00000000-0000-0000-0000-000000000013}"
)

func smartArtDeck(drawing string) []byte {
	return pt.Build(pt.Deck{Slides: []pt.Slide{{
		Shapes: pt.SmartArtFrame(2),
		SmartArt: &pt.SmartArt{
			Points: pt.DocPoint(saDoc) +
				pt.NodePoint(saPlan, pt.P(pt.R(`lang="en-US"`, "Plan the launch"))) +
				pt.NodePoint(saShip, pt.P(pt.R(`lang="en-US"`, "Ship "), pt.R(`lang="en-US" b="1"`, "it"))) +
				pt.PresPoint(saPresA, saPlan) +
				pt.PresPoint(saPresB, saShip),
			Drawing: drawing,
		},
	}}})
}

// SmartArt text is held twice, but the translator sees it once: segments
// come from the data part only, and the export writes each translation into
// the data part and into the drawing viewers draw from. The drawing keeps its
// own run properties (its laid-out font size).
func TestPPTXSmartArt_TranslatedInDataAndDrawing(t *testing.T) {
	content := smartArtDeck(
		pt.DrawingShape(saPresA, pt.P(pt.R(`lang="en-US" sz="2400"`, "Plan the launch"))) +
			// A drawing shape may name the data point directly.
			pt.DrawingShape(saShip, pt.P(pt.R(`lang="en-US" sz="2400"`, "Ship "), pt.R(`lang="en-US" sz="2400" b="1"`, "it"))),
	)
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if got := pptxTexts(segs); len(got) != 2 || got[0] != "Plan the launch" || got[1] != "Ship it" {
		t.Fatalf("segments = %q, want each node once", got)
	}
	for _, s := range segs {
		if s.Context != "slide:1/smartart" {
			t.Errorf("context %q, want slide:1/smartart", s.Context)
		}
	}
	out := pptxExport(t, content, map[string]string{
		segs[0].Key: "Planifier le lancement",
		segs[1].Key: `Le <g id="1">livrer</g>`,
	})
	data := pptxPart(t, out, "ppt/diagrams/data1.xml")
	drawing := pptxPart(t, out, "ppt/diagrams/drawing1.xml")
	for _, want := range []string{
		`<a:rPr lang="fr-CA"/><a:t>Planifier le lancement</a:t>`,
		`<a:rPr lang="fr-CA" b="1"/><a:t>livrer</a:t>`,
	} {
		if !strings.Contains(data, want) {
			t.Errorf("data part is missing %s", want)
		}
	}
	for _, want := range []string{
		`<a:rPr lang="fr-CA" sz="2400"/><a:t>Planifier le lancement</a:t>`,
		`<a:rPr lang="fr-CA" sz="2400" b="1"/><a:t>livrer</a:t>`,
	} {
		if !strings.Contains(drawing, want) {
			t.Errorf("drawing is missing %s\n%s", want, drawing)
		}
	}
}

// A drawing paragraph that does not line up with its data paragraph keeps
// its source text and is reported, rather than getting a translation whose
// chips mean something else.
func TestPPTXSmartArt_MismatchKeepsDrawingAndIsCounted(t *testing.T) {
	stale := pt.P(pt.R(`lang="en-US" sz="2400"`, "Plan the launch"))
	content := smartArtDeck(
		pt.DrawingShape(saPresA, stale) +
			// Same text as the data, but the chip is gone: ids would not match.
			pt.DrawingShape(saPresB, pt.P(pt.R(`lang="en-US" sz="2400"`, "Ship it"))),
	)
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	out, unmirrored, err := reconstructPPTX(context.Background(), content, map[string]string{
		segs[1].Key: `Le <g id="1">livrer</g>`,
	}, ExportLangs{Source: "en", Target: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	if unmirrored != 1 {
		t.Errorf("unmirrored = %d, want 1", unmirrored)
	}
	drawing := pptxPart(t, out, "ppt/diagrams/drawing1.xml")
	if !strings.Contains(drawing, "Ship it") || strings.Contains(drawing, "livrer") {
		t.Errorf("mismatched drawing paragraph must keep its source text:\n%s", drawing)
	}
	if !strings.Contains(drawing, stale) {
		t.Error("an untranslated drawing paragraph changed")
	}
	if !strings.Contains(pptxPart(t, out, "ppt/diagrams/data1.xml"), "livrer") {
		t.Error("the data part must still be translated")
	}
}

// Some files carry no drawing part, or an empty one; PowerPoint then lays
// the diagram out from the data part. The data is still translated, and
// there is nothing to mirror.
func TestPPTXSmartArt_NoOrEmptyDrawing(t *testing.T) {
	points := pt.DocPoint(saDoc) + pt.NodePoint(saPlan, pt.P(pt.R(`lang="en-US"`, "Plan the launch")))
	for name, sa := range map[string]*pt.SmartArt{
		"no drawing part":    {Points: points, NoDrawing: true},
		"empty drawing part": {Points: points},
	} {
		t.Run(name, func(t *testing.T) {
			content := pt.Build(pt.Deck{Slides: []pt.Slide{{Shapes: pt.SmartArtFrame(2), SmartArt: sa}}})
			segs, err := PPTX{}.Parse(content)
			if err != nil {
				t.Fatal(err)
			}
			if len(segs) != 1 || segs[0].Text != "Plan the launch" {
				t.Fatalf("segments = %q", pptxTexts(segs))
			}
			out, unmirrored, err := reconstructPPTX(context.Background(), content, map[string]string{segs[0].Key: "Planifier"}, ExportLangs{})
			if err != nil {
				t.Fatal(err)
			}
			if unmirrored != 0 {
				t.Errorf("unmirrored = %d, want 0", unmirrored)
			}
			if !strings.Contains(pptxPart(t, out, "ppt/diagrams/data1.xml"), "<a:t>Planifier</a:t>") {
				t.Error("the data part must be translated")
			}
		})
	}
}

// Drawn text the data part does not hold cannot be mirrored: it keeps its
// source text and is counted, so the export can report it.
func TestPPTXSmartArt_UnmatchedDrawingTextIsCounted(t *testing.T) {
	content := smartArtDeck(
		pt.DrawingShape(saPresA, pt.P(pt.R(`lang="en-US" sz="2400"`, "Plan the launch"))) +
			pt.DrawingShape("{00000000-0000-0000-0000-0000000000FF}", pt.P(pt.R(`lang="en-US" sz="2400"`, "Stale caption"))),
	)
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	out, unmirrored, err := reconstructPPTX(context.Background(), content, map[string]string{segs[0].Key: "Planifier le lancement"}, ExportLangs{})
	if err != nil {
		t.Fatal(err)
	}
	if unmirrored != 1 {
		t.Errorf("unmirrored = %d, want 1", unmirrored)
	}
	drawing := pptxPart(t, out, "ppt/diagrams/drawing1.xml")
	if !strings.Contains(drawing, "Planifier le lancement") || !strings.Contains(drawing, "Stale caption") {
		t.Errorf("the matched paragraph is mirrored and the unmatched one kept:\n%s", drawing)
	}
}

// Groups in a drawing nest no deeper than shapes on a slide.
func TestPPTXSmartArt_DrawingDepthGuard(t *testing.T) {
	deep := strings.Repeat(`<dsp:grpSp><dsp:nvGrpSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvGrpSpPr/></dsp:nvGrpSpPr><dsp:grpSpPr/>`, maxPPTXShapeDepth+2) +
		strings.Repeat(`</dsp:grpSp>`, maxPPTXShapeDepth+2)
	err := callGuarded(t, func(c []byte) error { _, err := PPTX{}.Parse(c); return err }, smartArtDeck(deep))
	if !errors.Is(err, ErrPPTXMalformed) || errors.Is(err, errRecoveredPanic) {
		t.Fatalf("err = %v, want the depth guard's ErrPPTXMalformed", err)
	}
}
