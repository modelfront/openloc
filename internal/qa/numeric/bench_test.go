package numeric

import (
	"strings"
	"testing"
)

// A typical segment: a sentence with a few numbers.
func BenchmarkRun_Sentence(b *testing.B) {
	src := "Free cancellation until 24 hours before check-in on March 14, 2026, for stays of 3 nights or more."
	tgt := "Kostenlose Stornierung bis 24 Stunden vor der Anreise am 14.03.2026 für Aufenthalte ab 3 Nächten."
	for b.Loop() {
		Run(src, tgt, "en", "de", allChecks)
	}
}

// A long paragraph with few numbers: the cost should follow the numbers, not
// the length.
func BenchmarkRun_LongText(b *testing.B) {
	src := strings.Repeat("The quick brown fox jumps over the lazy dog near the river bank. ", 60) + "It costs 25.16 EUR."
	tgt := strings.Repeat("Der schnelle braune Fuchs springt über den faulen Hund am Flussufer. ", 60) + "Es kostet 25,16 EUR."
	for b.Loop() {
		Run(src, tgt, "en", "de", allChecks)
	}
}
