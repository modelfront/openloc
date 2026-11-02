package parser

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"openloc/internal/segmentation"
)

// oracleSeeds are texts that sit on the edges of the oracle's rules.
var oracleSeeds = []string{
	"Hello world. This is a test.",
	"Dr. Smith arrived. Mr. Jones left.",
	"FIG. 3 shows it. Approx. 5 units.",
	"See e.g. the manual. I.E. Not this.",
	"E.G. Foo. Type B. Next one.",
	"İnc. Foo and vſ. Bar and fİg. Baz.",
	"Wait... Then! Really?! Yes: No.",
	"Ratio 1:2 and I:O. Time 10:30. Done.",
	"He said \"Hi.\" Then (left). [Next] {x}. «Quote» “double” ‘single’.",
	"  leading and trailing  ",
	"end. Next Line.　Full.\u0085Nel.\vVtab.",
	"invalid \xff\xfe bytes. Next \xc3. More\x80. Ok",
	"3. Item and a. b. c. D. E.",
	"Ünïcödé. Ölçü. Ξένο. Кириллица. 中文。日本語.",
	"{count, plural, one {Item. Next} other {Items. More}}. After.",
	"",
	".",
	". A",
}

func assertOracleEqual(t *testing.T, text string) {
	t.Helper()
	cons := segmentation.Conservative()
	for name, veto := range map[string]func(int) bool{"no veto": nil, "brace veto": braceFree(text)} {
		got, err := cons.Split(text, veto)
		if err != nil {
			t.Fatalf("%s: conservative set on %q: %v", name, text, err)
		}
		want := oracleSplitSentences(text, veto)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: conservative set split %q into %v, the oracle into %v", name, text, got, want)
		}
	}
}

// TestConservativeMatchesOracle_Fixtures splits every line and paragraph of
// the parser's text fixtures, and the plain text of every segment the XML
// reference fixture yields, with both splitters.
func TestConservativeMatchesOracle_Fixtures(t *testing.T) {
	for _, s := range oracleSeeds {
		assertOracleEqual(t, s)
	}
	err := filepath.WalkDir("testdata", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) == ".pptx" {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		for _, para := range strings.Split(text, "\n\n") {
			assertOracleEqual(t, para)
		}
		for _, line := range strings.Split(text, "\n") {
			assertOracleEqual(t, line)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseXMLDoc(readFixture(t, st4SourcePath))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(n *xmlNode)
	blocks := 0
	walk = func(n *xmlNode) {
		if isSegmentContainer(doc.src, n) {
			_, rng := trimmedContentRange(doc.src, n)
			if rng.Start < rng.End {
				full := buildXMLContent(doc.src, rng, n, buildReferenceIndex(doc))
				blocks++
				got, err := segmentation.Conservative().Split(full.plain, full.canBreakAt())
				if err != nil {
					t.Fatal(err)
				}
				if want := oracleSplitSentences(full.plain, full.canBreakAt()); !reflect.DeepEqual(got, want) {
					t.Fatalf("XML block %q: conservative %v, oracle %v", full.plain, got, want)
				}
			}
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(doc.root)
	if blocks < 100 {
		t.Fatalf("walked %d XML blocks, want the reference's 100+", blocks)
	}
}

// FuzzConservativeMatchesOracle asserts the conservative built-in set splits
// any text exactly as the oracle does, with and without a veto.
func FuzzConservativeMatchesOracle(f *testing.F) {
	for _, s := range oracleSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		assertOracleEqual(t, text)
	})
}
