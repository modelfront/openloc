package parser

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func sentenceBlocks(t *testing.T, format, content string) []SentenceBlock {
	t.Helper()
	p, err := GetParser(format)
	if err != nil {
		t.Fatal(err)
	}
	j, ok := p.(SentenceJoiner)
	if !ok {
		t.Fatalf("%s is not a SentenceJoiner", format)
	}
	blocks, err := j.SentenceBlocks([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return blocks
}

// A run of sentences is one segment under a range key, which export writes
// over those sentences and the space between them.
func TestSentenceJoin_PlainText(t *testing.T) {
	src := "One one. Two two. Three three.\nKeep.\n"
	blocks := sentenceBlocks(t, "txt", src)
	if len(blocks) != 1 || !slices.Equal(blocks[0].Keys, []string{"line:1#1", "line:1#2", "line:1#3"}) {
		t.Fatalf("blocks = %+v", blocks)
	}
	r, err := blocks[0].Range(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Key != "line:1#1-2" || r.Text != "One one. Two two." || r.Rich != "" {
		t.Errorf("range = %+v", r)
	}
	if i, j, ok := blocks[0].Locate("line:1#1-2"); !ok || i != 0 || j != 1 {
		t.Errorf("locate range = %d %d %v", i, j, ok)
	}
	if i, j, ok := blocks[0].Locate("line:1#3"); !ok || i != 2 || j != 2 {
		t.Errorf("locate sentence = %d %d %v", i, j, ok)
	}
	if _, err := blocks[0].Range(1, 1); !errors.Is(err, ErrSentenceRange) {
		t.Errorf("one sentence is not a run: %v", err)
	}

	out := reconstructWith(t, "txt", src, map[string]string{r.Key: "Eins zwei.", "line:1#3": "Drei."})
	if out != "Eins zwei. Drei.\nKeep.\n" {
		t.Errorf("export = %q", out)
	}
}

// Chips are numbered across the run, and each sentence's own ids map into
// that numbering by its offset.
func TestSentenceJoin_RichText(t *testing.T) {
	src := "<p><b>Bold one.</b> Plain <i>two</i>. Three.</p>"
	blocks := sentenceBlocks(t, "html", src)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %+v", blocks)
	}
	r, err := blocks[0].Range(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Rich, `<g id="1"`) || !strings.Contains(r.Rich, `<g id="2"`) || !slices.Equal(r.Offsets, []int{0, 1}) {
		t.Errorf("range = %+v, want chips 1 and 2, the second sentence's own 1 at offset 1", r)
	}
	out := reconstructWith(t, "html", src, map[string]string{
		r.Key: `<g id="1" ctype="bold">Fett eins</g> und <g id="2" ctype="italic">zwei</g>.`,
	})
	if want := "<p><b>Fett eins</b> und <i>zwei</i>. Three.</p>"; !strings.Contains(out, want) {
		t.Errorf("export = %s, want %s", out, want)
	}
}

// In generic XML a run is one segment over its combined source range; export
// writes it there and leaves the element's other sentences alone.
func TestSentenceJoin_XML(t *testing.T) {
	src := `<root><p>First one. Second <b>bold</b> one. Third one.</p></root>`
	blocks := sentenceBlocks(t, "xml", src)
	if len(blocks) != 1 || len(blocks[0].Keys) != 3 {
		t.Fatalf("blocks = %+v", blocks)
	}
	r, err := blocks[0].Range(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "Second bold one. Third one." || !strings.Contains(r.Rich, `<g id="1"`) || !slices.Equal(r.Offsets, []int{0, 0}) {
		t.Errorf("range = %+v", r)
	}
	if i, j, ok := blocks[0].Locate(r.Key); !ok || i != 1 || j != 2 {
		t.Errorf("locate %s = %d %d %v", r.Key, i, j, ok)
	}
	chip := r.Rich[strings.Index(r.Rich, "<g"):strings.Index(r.Rich, ">")+1]
	out := reconstructWith(t, "xml", src, map[string]string{
		blocks[0].Keys[0]: "Erste.",
		r.Key:             "Zweite " + chip + "fett</g> und dritte.",
	})
	if want := `<p>Erste. Zweite <b>fett</b> und dritte.</p>`; !strings.Contains(out, want) {
		t.Errorf("export = %s, want %s", out, want)
	}
}
