package parser

import (
	"strings"
	"testing"

	pt "openloc/internal/parser/pptxtest"
)

// A run of a paragraph's sentences is one sentence over their range; export
// writes its translation there, the space between them included.
func TestPPTXJoin_RunOfSentences(t *testing.T) {
	content := pt.Build(slide(pt.TextBox(2, pt.P(pt.R(`lang="en-US"`, "First point. Second point. Third point.")))))
	blocks, err := PPTX{}.SentenceBlocks(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || len(blocks[0].Keys) != 3 {
		t.Fatalf("blocks = %+v", blocks)
	}
	r, err := blocks[0].Range(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "First point. Second point." {
		t.Errorf("range text %q", r.Text)
	}
	if i, j, ok := blocks[0].Locate(r.Key); !ok || i != 0 || j != 1 {
		t.Errorf("locate %s = %d %d %v", r.Key, i, j, ok)
	}
	out := pptxExport(t, content, map[string]string{r.Key: "Premier et deuxième.", blocks[0].Keys[2]: "Troisième."})
	got := pptxParagraphXML(t, pptxPart(t, out, "ppt/slides/slide1.xml"), "Premier")
	if !strings.Contains(got, "Premier et deuxième.") || !strings.Contains(got, "Troisième.") || strings.Contains(got, "point") {
		t.Errorf("paragraph %s, want the run and the third sentence translated", got)
	}
}
