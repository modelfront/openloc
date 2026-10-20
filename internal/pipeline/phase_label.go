package pipeline

import "strings"

// FailedPhase names the automation a failed job stopped in. A failed job keeps
// the current_step label of the run that failed (UpdateJobError leaves it), so
// the label is the record of which automation to run again.
type FailedPhase string

const (
	FailedPhaseAIStep         FailedPhase = "ai_step"
	FailedPhasePretranslation FailedPhase = "pretranslation"
)

// Idle labels for an in_progress job. Each ends in "complete", which is what
// IsPipelineRunning reads as "no run in flight".
const (
	// ReopenedStepLabel: a completed task was reopened by an edit.
	ReopenedStepLabel = "Task reopen complete"
	// ErrorDismissedStepLabel: an errored task was completed or sent back,
	// accepting its failure.
	ErrorDismissedStepLabel = "Error dismissal complete"
)

// IsAIStepLabel reports whether a current_step label belongs to the AI step.
// Case-insensitive: the synchronous re-run upper-cases its progress labels.
// "QE+APE" and "QE/APE" are older wordings still stored on job rows.
func IsAIStepLabel(label string) bool {
	l := strings.ToLower(label)
	return strings.Contains(l, strings.ToLower(AIStepMarker)) ||
		strings.Contains(l, "qe+ape") || strings.Contains(l, "qe/ape")
}

// FailedPhaseOf returns the automation a failed job stopped in, or "" when the
// job is not failed. Pre-translation is the only other automation that fails a
// job, so every label without the AI-step marker is pre-translation.
func FailedPhaseOf(status, label string) FailedPhase {
	if status != "failed" {
		return ""
	}
	if IsAIStepLabel(label) {
		return FailedPhaseAIStep
	}
	return FailedPhasePretranslation
}

// RetryingStepLabel is the label of a run waiting for asynq's next attempt
// after an error. It reads as running, and the AI step's keeps the marker, so
// the editor lock holds until the last attempt settles the job.
func RetryingStepLabel(phase string) string {
	if phase == PhaseQEAPE {
		return "Retrying " + AIStepMarker + " after an error"
	}
	return "Retrying " + phase + " after an error"
}
