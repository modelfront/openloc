package pptxtest

import (
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite e2e/fixtures/journey-deck.pptx from JourneyDeck")

const journeyFixture = "../../../../e2e/fixtures/journey-deck.pptx"

// The file-format journey uploads a committed copy of JourneyDeck. The copy
// must hold the same parts: compressed bytes may differ between Go versions,
// so the parts are compared unzipped. Run with -update after changing the
// deck.
func TestJourneyFixtureIsCurrent(t *testing.T) {
	if *update {
		if err := os.WriteFile(journeyFixture, Build(JourneyDeck()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	content, err := os.ReadFile(journeyFixture)
	if err != nil {
		t.Fatalf("read %s (run with -update to create it): %v", journeyFixture, err)
	}
	want := Parts(JourneyDeck())
	got := ReadParts(t, content)
	if len(got) != len(want) {
		t.Errorf("fixture has %d parts, JourneyDeck %d (run with -update)", len(got), len(want))
	}
	for name, body := range want {
		if got[name] != body {
			t.Errorf("fixture part %s differs from JourneyDeck (run with -update)", name)
		}
	}
}
