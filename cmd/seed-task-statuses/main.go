// Command seed-task-statuses creates the "All task statuses" project: two
// files × five target languages on the four-step workflow (Human, AI, Hybrid,
// Human), where each of the eight jobs sits in a different state, with real
// translations so the CAT editor can be tried in every one of them.
//
//	make seed-task-statuses   (also run by `make reset`)
//
// The Tasks tab shows every task state (Not started, In progress, Completed,
// Error, Sent back, overdue), and the editor every task mode: editable,
// completed (a manager's edit reopens it), errored with Re-run (AI step and
// pre-translation), errored with Skip AI step (the hybrid Review step), AI
// step running, and the final step.
//
// Idempotent: the project (and its URL) is kept, its files and workflow steps
// are rebuilt, so edits made while testing are thrown away.
package main

import (
	"fmt"

	"openloc/internal/devseed"
)

const projectName = "All task statuses"

var targetLangs = []string{"de", "fr", "es", "ja", "it"}

// withProgress is sc with its run's label and counts sized to this project's
// files.
func withProgress(sc devseed.Scenario, label string, done, total int32) devseed.Scenario {
	sc.Label, sc.Done, sc.Total = label, done, total
	return sc
}

// overdue is the sent-back case with its reopened task past its deadline.
func overdue(sc devseed.Scenario) devseed.Scenario {
	steps := append([]devseed.StepPlan(nil), sc.Steps...)
	steps[0] = devseed.Active(steps[0].Who, devseed.In(-devseed.Day))
	sc.Steps = steps
	return sc
}

var (
	homepage = devseed.Sentences(0, 8)
	checkout = devseed.Sentences(3, 6)
)

// One scenario per target language, in targetLangs order.
var files = []devseed.File{
	{Name: "website-homepage.txt", Segments: homepage, Scenarios: []devseed.Scenario{
		devseed.WithText(devseed.Finished, devseed.TextPlan{Approved: 6, AutoApproved: 2}),
		devseed.WithText(devseed.Translating, devseed.TextPlan{Approved: 2, Filled: 6}),
		devseed.WithText(withProgress(devseed.AIRunning, "AI step (QE+APE): 3 / 8", 3, 8), devseed.TextPlan{Approved: 5, Filled: 8}),
		devseed.WithText(withProgress(devseed.AIFailed, "AI step (QE+APE): 3 / 8", 0, 0), devseed.TextPlan{Approved: 5, Filled: 8}),
		devseed.WithText(withProgress(devseed.AIFailedOnReview, "AI step (QE+APE): 3 / 8", 0, 0), devseed.TextPlan{Approved: 2, AutoApproved: 2, Filled: 8}),
	}},
	{Name: "checkout-emails.txt", Segments: checkout, Scenarios: []devseed.Scenario{
		devseed.WithText(withProgress(devseed.PretranslationFailed, "MT: 2 / 6 segments", 0, 0), devseed.TextPlan{Filled: 2}),
		devseed.WithText(overdue(devseed.SentBackToTranslation), devseed.TextPlan{Approved: 4, AutoApproved: 1, Filled: 6}),
		devseed.WithText(withProgress(devseed.Stopped,
			"Stopped — 3 / 6 segments translated; run Pre-translate again to resume", 3, 6), devseed.TextPlan{Filled: 3}),
		devseed.WithText(devseed.SigningOff, devseed.TextPlan{Approved: 5, AutoApproved: 1, Filled: 6}),
		devseed.WithText(devseed.Reviewing, devseed.TextPlan{Approved: 2, AutoApproved: 1, Filled: 6}),
	}},
}

func main() {
	res := devseed.Run(devseed.Spec{
		ProjectName: projectName, TargetLangs: targetLangs, Steps: devseed.FourSteps, Files: files, Translations: true,
	})

	fmt.Printf(`
✓ Project %q ready — organization %q (tenant %d)
  %d files × %d target languages (%v), workflow: Translation → Auto QE+APE (AI) → Review (Hybrid) → Sign-off

  website-homepage.txt   DE finished · FR translating · ES AI step running · JA AI step failed · IT AI failed on Review
  checkout-emails.txt    DE pre-translation failed · FR sent back (overdue) · ES stopped · JA signing off · IT reviewing

  Sign in to the %q organization, then open:
    %s/projects/%d            (Tasks)
`, projectName, res.TenantName, res.TenantID, len(files), len(targetLangs), targetLangs,
		res.TenantName, devseed.BaseURL(), res.ProjectID)
}
