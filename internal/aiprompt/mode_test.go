package aiprompt_test

import (
	"testing"

	"openloc/internal/aiprompt"
)

// Every mode survives the trip through the four stored flags and back, and the
// comment-only flags are not read as APE (both store no status).
func TestModeFlagsRoundTrip(t *testing.T) {
	for _, mode := range []string{"mt", "qe", "ape", "qe_ape", "comment"} {
		in, ok := aiprompt.FlagsForMode(mode)
		if !ok {
			t.Fatalf("FlagsForMode(%q) is unknown", mode)
		}
		if got := aiprompt.ModeForFlags(in.OutStatus, in.OutText, in.OutComments); got != mode {
			t.Errorf("FlagsForMode(%q) reads back as %q", mode, got)
		}
	}
	in, _ := aiprompt.FlagsForMode("comment")
	if in.OutStatus || in.OutText || in.OutNote || !in.OutComments {
		t.Errorf("comment-only flags = %+v, want the comment output alone", in)
	}
}
