package terminology

import "testing"

func TestParseCSVHeader(t *testing.T) {
	// Notes-last export order (what HandleExportCSV now writes). domain before
	// notes must not be swapped.
	cols := parseCSVHeader([]string{"source_term", "target_term", "status", "domain", "notes"})
	if cols.source != 0 || cols.target != 1 || cols.status != 2 || cols.domain != 3 || cols.notes != 4 {
		t.Fatalf("notes-last order mismapped: %+v", cols)
	}

	// All-languages export header: target_language is present but unused, and it
	// must not shift the other columns' meaning.
	cols = parseCSVHeader([]string{"source_term", "target_language", "target_term", "status", "domain", "notes"})
	if cols.source != 0 || cols.target != 2 || cols.status != 3 || cols.domain != 4 || cols.notes != 5 {
		t.Fatalf("target_language column shifted mapping: %+v", cols)
	}

	// Fully scrambled + short aliases + absent columns.
	cols = parseCSVHeader([]string{"target", "notes", "source"})
	if cols.source != 2 || cols.target != 0 || cols.notes != 1 {
		t.Fatalf("scrambled header mismapped: %+v", cols)
	}
	if cols.status != -1 || cols.domain != -1 {
		t.Fatalf("absent columns must be -1: %+v", cols)
	}
}

func TestCSVColumnsAt(t *testing.T) {
	cols := csvColumns{source: 0, target: 1, status: 2, notes: 3, domain: 4}
	record := []string{"hello", "hola"} // short row: only 2 columns present

	if got := cols.at(record, cols.source); got != "hello" {
		t.Errorf("source: got %q", got)
	}
	// Missing trailing columns and the -1 sentinel both yield "" (no panic).
	if got := cols.at(record, cols.domain); got != "" {
		t.Errorf("out-of-range column should be empty, got %q", got)
	}
	if got := cols.at(record, -1); got != "" {
		t.Errorf("absent (-1) column should be empty, got %q", got)
	}
}
