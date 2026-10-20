package pipeline

import (
	"strings"
	"testing"
)

// 'stopped' is terminal: the buttons come back and polling ends.
func TestIsPipelineRunning_StoppedIsTerminal(t *testing.T) {
	label := StoppedStepLabel("MT pre-translation", RunState{})
	if IsPipelineRunning("stopped", label) {
		t.Fatalf("a stopped job must not count as running (label %q)", label)
	}
}

// The stop label must never end in "complete": IsPipelineRunning reads that
// suffix as a finished phase, and the label is also shown to the user.
func TestStoppedStepLabel(t *testing.T) {
	n30, n100 := int32(30), int32(100)
	cases := []struct {
		name  string
		phase string
		st    RunState
		want  []string
	}{
		{"mt with counts", "MT pre-translation", RunState{Translated: &n30, Total: &n100}, []string{"30 / 100", "segments translated", "Pre-translate"}},
		{"qe/ape with counts", PhaseQEAPE, RunState{Translated: &n30, Total: &n100}, []string{"30 / 100", "segments reviewed", "Re-run AI step"}},
		{"no counts", "MT pre-translation", RunState{}, []string{"Stopped", "Pre-translate"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := StoppedStepLabel(c.phase, c.st)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("label %q lacks %q", got, w)
				}
			}
			if strings.HasSuffix(strings.ToLower(got), "complete") {
				t.Errorf("label %q ends in 'complete' — IsPipelineRunning would read it as finished", got)
			}
			if !IsPipelineRunning("in_progress", got) {
				t.Errorf("label %q on a live job must still count as running", got)
			}
		})
	}
}
