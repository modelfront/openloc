package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	db "openloc/internal/database/sqlcgen"
)

// RunState is what a progress writer learns about its job at a chunk boundary.
type RunState struct {
	// Deleted: the job row is gone (deleted mid-flight).
	Deleted bool
	// StopRequested: a user asked the run to stop (POST /jobs/{id}/stop).
	StopRequested bool
	Translated    *int32
	Total         *int32
}

// LoadRunState reads the job's run state on a fresh short-lived context, so
// the check works even when the run's own context is already cancelled.
func LoadRunState(q db.Querier, jobID int64) (RunState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	row, err := q.GetJobRunState(ctx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunState{Deleted: true}, nil
	}
	if err != nil {
		return RunState{}, err
	}
	return RunState{StopRequested: row.StopRequested, Translated: row.TranslatedCount, Total: row.TotalCount}, nil
}

// StopRequested reports whether runErr is the cooperative stop: the run
// context was cancelled AND the job carries a pending stop request. Any other
// cancellation (server drain, job deleted) or failure returns false so the
// caller keeps treating it as an error.
func StopRequested(q db.Querier, jobID int64, runErr error) (RunState, bool) {
	if jobID == 0 || q == nil || !errors.Is(runErr, context.Canceled) {
		return RunState{}, false
	}
	st, err := LoadRunState(q, jobID)
	if err != nil || !st.StopRequested {
		return RunState{}, false
	}
	return st, true
}

// StoppedStepLabel is the current_step written when a run ends by user stop.
// It tells the reader what was kept and how to resume. It must not end in
// "complete" (IsPipelineRunning treats that suffix as a finished phase).
func StoppedStepLabel(phase string, st RunState) string {
	unit, resume := "segments translated", "run Pre-translate again to resume"
	if phase == PhaseQEAPE {
		unit, resume = "segments reviewed", "use Re-run AI step to resume"
	}
	if st.Translated != nil && st.Total != nil {
		return fmt.Sprintf("Stopped — %d / %d %s; %s", *st.Translated, *st.Total, unit, resume)
	}
	return "Stopped — " + resume
}

// PhaseQEAPE is the internal phase key (and event detail) for the AI step. It
// is not what the user reads — see AIStepLabel.
const PhaseQEAPE = "QE+APE"

// AIStepMarker is the substring every current_step this phase writes must
// carry. The frontend editor lock keys on it (isAIStepRunning), so a label
// without it silently unlocks the editor while the step is rewriting rows.
//
// Mode-independent on purpose: the lock's marker must not also be the step's
// name, or one cannot be correct without breaking the other. See AIStepLabel.
const AIStepMarker = "AI step"

// AIStepLabel names the step for the user: the marker the lock needs, plus the
// mode actually running. An empty or unknown mode yields the marker alone,
// which is what the labels written before the engine resolves can honestly say.
func AIStepLabel(mode string) string {
	switch mode {
	case "qe":
		return AIStepMarker + " (QE)"
	case "ape":
		return AIStepMarker + " (APE)"
	case "qe_ape":
		return AIStepMarker + " (QE+APE)"
	}
	return AIStepMarker
}

// PhaseNoun renders the run's phases as a noun phrase for event details and
// stop labels. Not PhaseDescription — that returns progress labels ("Running
// TM lookup + MT") which read garbled inside history sentences.
func PhaseNoun(ph TranslatePhases) string {
	switch {
	case ph.UseTM && ph.UseMT:
		return "TM + MT pre-translation"
	case ph.UseTM:
		return "TM pre-translation"
	case ph.UseMT:
		return "MT pre-translation"
	default:
		return "pre-translation"
	}
}
