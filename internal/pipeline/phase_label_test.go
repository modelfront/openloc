package pipeline

import "testing"

func TestFailedPhaseOf(t *testing.T) {
	cases := []struct {
		status, label string
		want          FailedPhase
	}{
		// Worker AI-step labels.
		{"failed", "Queued for AI step", FailedPhaseAIStep},
		{"failed", "Running AI step", FailedPhaseAIStep},
		{"failed", "AI step (QE): 4 / 30", FailedPhaseAIStep},
		// The synchronous re-run upper-cases its progress label.
		{"failed", "AI STEP (QE+APE): 4 / 30", FailedPhaseAIStep},
		// Older wording still stored on job rows.
		{"failed", "Running QE+APE", FailedPhaseAIStep},
		{"failed", "QE/APE: 2 / 9", FailedPhaseAIStep},
		// Pre-translation labels, worker and sync.
		{"failed", "Running TM lookup + MT", FailedPhasePretranslation},
		{"failed", "TM lookup: 3 / 10", FailedPhasePretranslation},
		{"failed", "MT: 5 / 10 segments", FailedPhasePretranslation},
		{"failed", "Queued for pre-translation", FailedPhasePretranslation},
		{"failed", "", FailedPhasePretranslation},
		// Not failed: nothing to re-run, whatever the label says.
		{"in_progress", "Running AI step", ""},
		{"completed", "feedback sent", ""},
		{"stopped", "Stopped — use Re-run AI step to resume", ""},
	}
	for _, c := range cases {
		if got := FailedPhaseOf(c.status, c.label); got != c.want {
			t.Errorf("FailedPhaseOf(%q, %q) = %q, want %q", c.status, c.label, got, c.want)
		}
	}
}

func TestIdleLabelsDoNotReadAsRunning(t *testing.T) {
	for _, label := range []string{ReopenedStepLabel, ErrorDismissedStepLabel} {
		if IsPipelineRunning("in_progress", label) {
			t.Errorf("%q must read as idle on an in_progress job", label)
		}
	}
}

func TestRetryingLabelsReadAsRunning(t *testing.T) {
	ai := RetryingStepLabel(PhaseQEAPE)
	if !IsPipelineRunning("queued", ai) {
		t.Errorf("%q must read as running", ai)
	}
	if !IsAIStepLabel(ai) {
		t.Errorf("%q must keep the AI-step marker so the editor lock holds", ai)
	}
	pre := RetryingStepLabel("TM + MT pre-translation")
	if !IsPipelineRunning("queued", pre) {
		t.Errorf("%q must read as running", pre)
	}
	if IsAIStepLabel(pre) {
		t.Errorf("%q must not read as the AI step", pre)
	}
}
