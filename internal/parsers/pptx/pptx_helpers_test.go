package pptx

import (
	"context"
	"strings"
	"testing"

	"openloc/internal/parsers/common"
	pt "openloc/internal/parsers/pptx/pptxtest"
)

// pptxSegments builds deck and parses it.
func pptxSegments(t *testing.T, deck pt.Deck) []common.Segment {
	t.Helper()
	segs, err := PPTX{}.Parse(pt.Build(deck))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return segs
}

// pptxTexts returns the plain text of every segment, in order.
func pptxTexts(segs []common.Segment) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.Text
	}
	return out
}

// pptxSegment returns the segment with the given text.
func pptxSegment(t *testing.T, segs []common.Segment, text string) common.Segment {
	t.Helper()
	for _, s := range segs {
		if s.Text == text {
			return s
		}
	}
	t.Fatalf("no segment %q in %q", text, pptxTexts(segs))
	return common.Segment{}
}

// pptxPart returns a part of a .pptx, unzipped.
func pptxPart(t *testing.T, content []byte, name string) string {
	t.Helper()
	return pt.ReadPart(t, content, name)
}

// pptxExport rebuilds content with translations for an en → fr-ca job.
func pptxExport(t *testing.T, content []byte, translations map[string]string) []byte {
	t.Helper()
	out, err := PPTX{}.ReconstructForLang(context.Background(), content, translations, common.ExportLangs{Source: "en", Target: "fr-ca"})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	return out
}

// pptxParagraphXML returns the first a:p element of part that contains want.
func pptxParagraphXML(t *testing.T, part, want string) string {
	t.Helper()
	for _, p := range strings.SplitAfter(part, "</a:p>") {
		if i := strings.LastIndex(p, "<a:p>"); i >= 0 && strings.Contains(p[i:], want) {
			return p[i:]
		}
		if i := strings.LastIndex(p, "<a:p "); i >= 0 && strings.Contains(p[i:], want) {
			return p[i:]
		}
	}
	t.Fatalf("no paragraph containing %q in %s", want, part)
	return ""
}

// slide is a one-slide deck with the given shapes.
func slide(shapes ...string) pt.Deck {
	return pt.Deck{Slides: []pt.Slide{{Shapes: strings.Join(shapes, "")}}}
}
