package worker

import "openloc/internal/pipeline"

// pipelineCompletedDetails is the job.pipeline_completed payload: the phase
// noun the History sentence names, plus what each phase did.
func pipelineCompletedDetails(phases pipeline.TranslatePhases, outcome pipeline.TranslateOutcome) map[string]any {
	details := outcome.HistoryDetails(phases)
	details["phase"] = pipeline.PhaseNoun(phases)
	return details
}
