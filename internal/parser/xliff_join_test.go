package parser

import (
	"errors"
	"strings"
	"testing"
)

const segmentedSDLXLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff xmlns:sdl="http://sdl.com/FileTypes/SdlXliff/1.0" xmlns="urn:oasis:names:tc:xliff:document:1.2" version="1.2">
  <file original="a.docx" datatype="x-sdlfilterframework2" source-language="en-US" target-language="de-DE">
    <body>
      <trans-unit id="a1">
        <source>One. Two. Three.</source>
        <seg-source><mrk mtype="seg" mid="1">One.</mrk> <mrk mtype="seg" mid="2">Two.</mrk> <mrk mtype="seg" mid="3">Three.</mrk></seg-source>
        <target><mrk mtype="seg" mid="1">Eins.</mrk> <mrk mtype="seg" mid="2">Zwei.</mrk> <mrk mtype="seg" mid="3">Drei.</mrk></target>
        <sdl:seg-defs><sdl:seg id="1" conf="Translated"/><sdl:seg id="2" conf="Translated"/><sdl:seg id="3"/></sdl:seg-defs>
      </trans-unit>
    </body>
  </file>
</xliff>`

// Joining <mrk> sentences merges them into the first, in <seg-source> and
// <target>, and drops the merged-away <sdl:seg>; <source> stays.
func TestXLIFFJoin_MergesMrks(t *testing.T) {
	blocks, err := XLIFF{}.SentenceBlocks([]byte(segmentedSDLXLIFF))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || strings.Join(blocks[0].Keys, ",") != "a1:1,a1:2,a1:3" {
		t.Fatalf("blocks = %+v", blocks)
	}
	r, err := blocks[0].Range(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "One. Two." || r.Key != "a1:1~2" {
		t.Errorf("range = %+v", r)
	}
	if i, j, ok := blocks[0].Locate(r.Key); !ok || i != 0 || j != 1 {
		t.Errorf("locate = %d %d %v", i, j, ok)
	}

	out, err := XLIFF{}.Reconstruct([]byte(segmentedSDLXLIFF), map[string]string{r.Key: "Eins und zwei.", "a1:3": "Drei!"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		`<source>One. Two. Three.</source>`,
		`<seg-source><mrk mtype="seg" mid="1">One. Two.</mrk> <mrk mtype="seg" mid="3">Three.</mrk></seg-source>`,
		`<target><mrk mtype="seg" mid="1">Eins und zwei.</mrk> <mrk mtype="seg" mid="3">Drei!</mrk></target>`,
		`<sdl:seg-defs><sdl:seg id="1" conf="Translated"/><sdl:seg id="3"/></sdl:seg-defs>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("export lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, `mid="2"`) {
		t.Errorf("the merged-away mrk is still there:\n%s", got)
	}
}

// Sentences with an element between them are not merged: the run is refused.
func TestXLIFFJoin_RefusesUnsafeRuns(t *testing.T) {
	src := strings.Replace(segmentedSDLXLIFF, `</mrk> <mrk mtype="seg" mid="2">`, `</mrk><x id="9"/><mrk mtype="seg" mid="2">`, 1)
	blocks, err := XLIFF{}.SentenceBlocks([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocks[0].Range(0, 1); !errors.Is(err, ErrSentenceRange) {
		t.Errorf("run across an element: %v, want ErrSentenceRange", err)
	}
	if _, err := blocks[0].Range(1, 2); err != nil {
		t.Errorf("run of the next two: %v", err)
	}
}
