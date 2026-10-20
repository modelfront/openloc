package terminology

import (
	"errors"
	"testing"
)

// importConceptsBatched must: (1) commit whole batches via the fast bulk path,
// and (2) when a batch's bulk insert fails, fall back to inserting that batch's
// concepts one at a time so a single bad concept is skipped rather than dropping
// its healthy neighbors. Pure control-flow test with injected fakes (no DB).
func TestImportConceptsBatched_FallbackIsolatesBadConcept(t *testing.T) {
	oneTerm := func(text string) ConceptInput {
		return ConceptInput{Terms: []ConceptTermInput{{Language: "en", Text: text}}}
	}
	concepts := []ConceptInput{oneTerm("alpha"), oneTerm("bad"), oneTerm("gamma")}

	// bulk fails if the batch contains "bad" (simulating the tx abort); single
	// fails only for "bad".
	batchHas := func(b []ConceptInput, text string) bool {
		for _, ci := range b {
			if ci.Terms[0].Text == text {
				return true
			}
		}
		return false
	}
	var bulkCalls, singleCalls int
	bulk := func(b []ConceptInput) (int, error) {
		bulkCalls++
		if batchHas(b, "bad") {
			return 0, errors.New("nul byte")
		}
		terms := 0
		for _, ci := range b {
			terms += len(ci.Terms)
		}
		return terms, nil
	}
	single := func(ci ConceptInput) error {
		singleCalls++
		if ci.Terms[0].Text == "bad" {
			return errors.New("nul byte")
		}
		return nil
	}

	// One batch holding all three → bulk fails → per-concept fallback.
	imported, terms, skipped := importConceptsBatched(concepts, 200, bulk, single)
	if imported != 2 || skipped != 1 || terms != 2 {
		t.Errorf("bad concept not isolated: imported=%d skipped=%d terms=%d (want 2/1/2)", imported, skipped, terms)
	}
	if bulkCalls != 1 || singleCalls != 3 {
		t.Errorf("want 1 bulk attempt + 3 single retries, got bulk=%d single=%d", bulkCalls, singleCalls)
	}
}

// All-good input crosses the batch boundary with no fallback: every concept
// imported via the fast path, single() never called.
func TestImportConceptsBatched_BatchBoundaryNoFallback(t *testing.T) {
	concepts := make([]ConceptInput, 5)
	for i := range concepts {
		concepts[i] = ConceptInput{Terms: []ConceptTermInput{{Language: "en", Text: "t"}, {Language: "de", Text: "t"}}}
	}
	var bulkCalls, singleCalls int
	bulk := func(b []ConceptInput) (int, error) {
		bulkCalls++
		terms := 0
		for _, ci := range b {
			terms += len(ci.Terms)
		}
		return terms, nil
	}
	single := func(ConceptInput) error { singleCalls++; return nil }

	// batchSize 2 over 5 concepts → 3 batches, all succeed.
	imported, terms, skipped := importConceptsBatched(concepts, 2, bulk, single)
	if imported != 5 || skipped != 0 || terms != 10 {
		t.Errorf("want imported=5 skipped=0 terms=10, got %d/%d/%d", imported, skipped, terms)
	}
	if bulkCalls != 3 || singleCalls != 0 {
		t.Errorf("want 3 bulk batches + 0 single retries, got bulk=%d single=%d", bulkCalls, singleCalls)
	}
}

// DedupeConceptTerms drops repeat (language, text) within a concept; language is
// normalized for the comparison but text is exact (case-sensitive).
func TestDedupeConceptTerms(t *testing.T) {
	out := DedupeConceptTerms([]ConceptTermInput{
		{Language: "EN", Text: "car"},
		{Language: "en", Text: "car"}, // dup once language normalizes
		{Language: "de", Text: "Auto"},
		{Language: "en", Text: "CAR"}, // NOT a dup — text case differs
	})
	if len(out) != 3 {
		t.Fatalf("want 3 terms after dedup, got %d: %+v", len(out), out)
	}
}
