package parser

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	pt "openloc/internal/parser/pptxtest"
)

var updatePPTXGolden = flag.Bool("update", false, "rewrite testdata/pptx/*.segments.golden from the current parser")

// pptxDecks are the decks every invariant runs on: the built journey deck,
// and every .pptx in testdata/pptx. Decks made in PowerPoint go there, with
// made-up content only: the directory ships with the source.
func pptxDecks(t *testing.T) map[string][]byte {
	t.Helper()
	decks := map[string][]byte{"journey": pt.Build(pt.JourneyDeck())}
	paths, err := filepath.Glob(filepath.Join("testdata", "pptx", "*.pptx"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		decks[strings.TrimSuffix(filepath.Base(p), ".pptx")] = b
	}
	return decks
}

func sortedNames(decks map[string][]byte) []string {
	names := make([]string, 0, len(decks))
	for n := range decks {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Each deck's segments are pinned in a golden file — position, context, key,
// text and rich text — so any change to what a translator sees shows up in
// review. Run with -update to rewrite them after checking the change.
func TestPPTXTestdata_Golden(t *testing.T) {
	decks := pptxDecks(t)
	for _, name := range sortedNames(decks) {
		t.Run(name, func(t *testing.T) {
			segs, err := PPTX{}.Parse(decks[name])
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var b strings.Builder
			for _, s := range segs {
				fmt.Fprintf(&b, "%d\t%s\t%s\t%q\t%q\n", s.Position, s.Context, s.Key, s.Text, s.RichText)
			}
			path := filepath.Join("testdata", "pptx", name+".segments.golden")
			if *updatePPTXGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s (run with -update to create it): %v", path, err)
			}
			if b.String() != string(want) {
				t.Errorf("segments differ from %s (run with -update after checking):\n got:\n%s\nwant:\n%s", path, b.String(), want)
			}
		})
	}
}

// For every deck: an export without translations is the upload; a full
// pseudo-translation reads back under the same keys with every chip in
// place; and every part that is not XML is copied raw.
func TestPPTXTestdata_RoundTripInvariants(t *testing.T) {
	decks := pptxDecks(t)
	for _, name := range sortedNames(decks) {
		t.Run(name, func(t *testing.T) {
			content := decks[name]
			if !bytes.Equal(pptxExport(t, content, nil), content) {
				t.Error("export without translations changed the file")
			}
			segs, err := PPTX{}.Parse(content)
			if err != nil {
				t.Fatal(err)
			}
			translations := map[string]string{}
			for _, s := range segs {
				translations[s.Key] = "Ẍ " + s.Text
				if s.RichText != "" {
					translations[s.Key] = "Ẍ " + s.RichText
				}
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
				want := segs[i]
				if s.Key != want.Key || s.Text != "Ẍ "+want.Text {
					t.Errorf("segment %d reads back %s %q, want %s %q", i, s.Key, s.Text, want.Key, "Ẍ "+want.Text)
				}
				if want.RichText != "" && s.RichText != "Ẍ "+want.RichText {
					t.Errorf("segment %d chips %q, want %q", i, s.RichText, "Ẍ "+want.RichText)
				}
			}
			before, after := zipEntries(t, content), zipEntries(t, out)
			for part, raw := range before {
				if strings.HasSuffix(part, ".xml") || strings.HasSuffix(part, ".rels") {
					continue
				}
				if !bytes.Equal(raw, after[part]) {
					t.Errorf("%s was not copied raw", part)
				}
			}
		})
	}
}

// FuzzPPTXParse feeds mutated decks to Parse and Reconstruct: neither may
// panic or hang, whatever the input. Plain `go test` runs the seeds.
func FuzzPPTXParse(f *testing.F) {
	f.Add(pt.Build(pt.JourneyDeck()))
	f.Add(pt.Build(slide(pt.TextBox(2, pt.P(pt.R(`b="1"`, "Bold. "), pt.R("", "Plain"), pt.Br(), pt.SlideNumber(1))))))
	f.Add([]byte("PK\x03\x04"))
	f.Fuzz(func(t *testing.T, content []byte) {
		segs, err := PPTX{}.Parse(content)
		if errors.Is(err, errRecoveredPanic) {
			t.Fatalf("Parse panicked: %v", err)
		}
		if err != nil {
			return
		}
		translations := map[string]string{}
		for _, s := range segs {
			translations[s.Key] = s.RichText + "x"
		}
		if _, err := (PPTX{}).Reconstruct(content, translations); err != nil {
			t.Fatalf("a deck that parses must export: %v", err)
		}
	})
}
