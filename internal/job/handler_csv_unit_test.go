package job

import (
	"testing"
	"time"
)

// Pure-function unit tests for the CSV helpers.
// Lives in `package job` (internal test) so it can call unexported
// helpers directly without hitting the DB.

func TestBuildAnalysisCSVFilename_WithProjectName(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	got := buildAnalysisCSVFilename("Demo Project", 42, now)
	want := "Demo Project-analysis-2026-05-07.csv"
	if got != want {
		t.Errorf("filename: want %q, got %q", want, got)
	}
}

// Anti-tautology guard: empty project name MUST take the
// `analysis-<id>-<date>.csv` fallback path. Without the guard the
// filename would render as `-analysis-<date>.csv` (leading hyphen),
// which is awkward and breaks downstream tooling that expects the
// project name as the filename's leading segment.
func TestBuildAnalysisCSVFilename_EmptyNameFallback(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	got := buildAnalysisCSVFilename("", 42, now)
	want := "analysis-42-2026-05-07.csv"
	if got != want {
		t.Errorf("empty-name fallback: want %q, got %q", want, got)
	}
}
