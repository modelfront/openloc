// Command seed-ai-steps creates the "AI step states" project: a two-step
// workflow (Post-editing, Hybrid → Auto QE+APE, AI) where each step holds two
// tasks, one whose AI run failed and one whose AI run is in progress.
//
//	make seed-ai-steps   (also run by `make reset`)
//
// The editor shows, per step type: the error row with Re-run and Complete,
// plus Skip AI step on the Hybrid step for a manager; and the AI-running lock.
//
// Idempotent: the project (and its URL) is kept, its files and workflow steps
// are rebuilt, so edits made while testing are thrown away.
package main

import (
	"fmt"

	"openloc/internal/devseed"
)

const projectName = "AI step states"

var targetLangs = []string{"de", "fr"}

var steps = []devseed.Step{
	{Name: "Post-editing", Role: "linguist", Type: "hybrid", UsesRouter: true},
	{Name: "Auto QE+APE", Role: "linguist", Type: "ai", UsesRouter: true},
}

const aiRunLabel = "AI step (QE+APE): 3 / 8"

var (
	// On the Hybrid step: pre-translated, then the AI run before the person.
	hybridFailed = devseed.Scenario{Name: "Hybrid step, AI failed",
		Steps:  []devseed.StepPlan{devseed.Active(0, devseed.In(2*devseed.Day)), devseed.Pending()},
		Status: "failed", Label: aiRunLabel, ErrMsg: devseed.FailedAIStepError,
		Text: devseed.TextPlan{Filled: 8}}
	hybridRunning = devseed.Scenario{Name: "Hybrid step, AI running",
		Steps:  []devseed.StepPlan{devseed.Active(0, devseed.In(2*devseed.Day)), devseed.Pending()},
		Status: "in_progress", Label: aiRunLabel, Done: 3, Total: 8,
		Text: devseed.TextPlan{Filled: 8}}
	// On the AI step: Post-editing is done, the AI step checks the result.
	aiFailed = devseed.Scenario{Name: "AI step, AI failed",
		Steps:  []devseed.StepPlan{devseed.Completed(0, devseed.Day), devseed.Active(-1, nil)},
		Status: "failed", Label: aiRunLabel, ErrMsg: devseed.FailedAIStepError,
		Text: devseed.TextPlan{Approved: 5, Filled: 8}}
	aiRunning = devseed.Scenario{Name: "AI step, AI running",
		Steps:  []devseed.StepPlan{devseed.Completed(0, devseed.Day), devseed.Active(-1, nil)},
		Status: "in_progress", Label: aiRunLabel, Done: 3, Total: 8,
		Text: devseed.TextPlan{Approved: 5, Filled: 8}}
)

// One scenario per target language, in targetLangs order.
var files = []devseed.File{
	{Name: "user-guide.txt", Segments: devseed.Sentences(0, 8),
		Scenarios: []devseed.Scenario{hybridFailed, hybridRunning}},
	{Name: "release-notes.txt", Segments: devseed.Sentences(4, 8),
		Scenarios: []devseed.Scenario{aiFailed, aiRunning}},
}

func main() {
	res := devseed.Run(devseed.Spec{
		ProjectName: projectName, TargetLangs: targetLangs, Steps: steps, Files: files, Translations: true,
	})

	fmt.Printf(`
✓ Project %q ready — organization %q (tenant %d)
  %d files × %d target languages (%v), workflow: Post-editing (Hybrid) → Auto QE+APE (AI)

  user-guide.txt      DE Post-editing: AI failed · FR Post-editing: AI running
  release-notes.txt   DE Auto QE+APE: AI failed  · FR Auto QE+APE: AI running

  Sign in to the %q organization, then open:
    %s/projects/%d            (Tasks)
`, projectName, res.TenantName, res.TenantID, len(files), len(targetLangs), targetLangs,
		res.TenantName, devseed.BaseURL(), res.ProjectID)
}
