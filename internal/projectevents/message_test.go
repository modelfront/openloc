package projectevents

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShortMessage(t *testing.T) {
	if got := ShortMessage("boom"); got != "boom" {
		t.Errorf("short message changed: %q", got)
	}
	// 199 ASCII bytes then a 3-byte rune straddling the 200-byte cap.
	long := strings.Repeat("a", 199) + "€" + strings.Repeat("b", 50)
	got := ShortMessage(long)
	if !utf8.ValidString(got) {
		t.Fatalf("cut produced invalid UTF-8: %q", got)
	}
	if got != strings.Repeat("a", 199)+"…" {
		t.Errorf("cut = %q, want the 199 a's and an ellipsis", got)
	}
}
