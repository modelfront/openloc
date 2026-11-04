package worker

import (
	"openloc/internal/ai"
	"openloc/internal/pipeline"
)

// aiStepOutcome is the event detail of what an AI step run decided. A
// comment-only run approves and rejects nothing, so it carries the comment
// count alone; zero approval counts would read as a review that found nothing.
func aiStepOutcome(res pipeline.AIStepResult) map[string]any {
	if res.Mode == ai.ModeComment {
		return map[string]any{"commented": res.Commented}
	}
	return map[string]any{"auto_approved": res.AutoApproved, "needs_review": res.NeedsReview, "commented": res.Commented}
}

// aiStepPhase names the run in the project history: "finished <phase> for …".
func aiStepPhase(mode string) string {
	if mode == ai.ModeComment {
		return "AI comment review"
	}
	return pipeline.PhaseQEAPE
}
