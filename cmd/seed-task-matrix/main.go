// Command seed-task-matrix creates a project that shows every state the
// project page's Tasks and Files tabs can draw, for manual QA of the task
// matrix and file readiness.
//
//	make seed-task-matrix
//
// Five files × four target languages × a four-step workflow (Human, AI,
// Hybrid, Human), where every job is a different case: finished, translating,
// overdue and unassigned, AI step running, in review, sent back, signing off,
// later steps assigned in advance, failed (pre-translation and AI step),
// stopped. Each file mixes them so the Files tab shows Ready, In progress,
// Processing, Failed and Stopped.
//
// Idempotent like seed-spellcheck: the project (and its URL) is kept, its files
// and workflow steps are rebuilt, so edits made while testing are thrown away.
// Steps and jobs are written directly — no pipeline runs — so the files have
// segments but no stored originals: Download is not part of what this shows.
// The segments carry no translations; the "All task statuses" project
// (`make seed-task-statuses`) is the one to edit in.
package main

import (
	"fmt"
	"time"

	"openloc/internal/devseed"
)

const projectName = "Task Matrix Showcase"

var targetLangs = []string{"de", "fr", "es", "ja"}

// The showcase's cases beyond the shared ones.
var (
	lateUnassigned = devseed.Scenario{Name: "late & unassigned",
		Steps:  []devseed.StepPlan{devseed.Active(-1, devseed.In(-2*devseed.Day)), devseed.Pending(), devseed.Pending(), devseed.Pending()},
		Status: "in_progress", Label: devseed.Waiting}
	reviewing = devseed.Scenario{Name: "reviewing",
		Steps: []devseed.StepPlan{devseed.Completed(0, 3*devseed.Day), devseed.Completed(-1, 3*devseed.Day),
			devseed.Active(2, devseed.In(12*time.Hour)), devseed.Pending()},
		Status: "in_progress", Label: devseed.Waiting}
	preAssigned = devseed.Scenario{Name: "later steps assigned in advance",
		Steps: []devseed.StepPlan{devseed.Active(0, devseed.In(4*devseed.Day)), devseed.Pending(),
			devseed.Planned(2, devseed.In(9*devseed.Day)), devseed.Planned(3, devseed.In(14*devseed.Day))},
		Status: "in_progress", Label: devseed.Waiting}
)

// One row per file; one scenario per target language, in targetLangs order.
var files = []devseed.File{
	{Name: "onboarding-emails.txt", Segments: devseed.Sentences(0, 8),
		Scenarios: []devseed.Scenario{devseed.Finished, devseed.Finished, devseed.Finished, devseed.Finished}},
	{Name: "product-faq.txt", Segments: devseed.Sentences(8, 10),
		Scenarios: []devseed.Scenario{devseed.Translating, reviewing, devseed.SigningOff, devseed.SentBackToTranslation}},
	{Name: "release-notes-2026.txt", Segments: devseed.Sentences(4, 12),
		Scenarios: []devseed.Scenario{devseed.AIRunning, lateUnassigned, preAssigned, reviewing}},
	{Name: "checkout-confirmation-emails-final-v3-reviewed-by-legal-and-brand.txt", Segments: devseed.Sentences(2, 6),
		Scenarios: []devseed.Scenario{devseed.PretranslationFailed, devseed.Stopped, devseed.Translating, devseed.Finished}},
	{Name: "privacy-policy.txt", Segments: devseed.Sentences(10, 9),
		Scenarios: []devseed.Scenario{devseed.Stopped, reviewing, devseed.SigningOff, devseed.AIFailed}},
}

func main() {
	res := devseed.Run(devseed.Spec{
		ProjectName: projectName, TargetLangs: targetLangs, Steps: devseed.FourSteps, Files: files,
	})

	fmt.Printf(`
✓ Project %q ready — organization %q (tenant %d)
  %d files × %d target languages (%v), workflow: Translation → Auto QE+APE (AI) → Review (Hybrid) → Sign-off

  Every job is a different case: finished, translating, overdue and unassigned,
  AI step running, in review (due < 24h), sent back with a reason, signing off,
  later steps assigned in advance, failed (pre-translation and AI step), stopped.

  Sign in to the %q organization, then open:
    %s/projects/%d            (Tasks)
    %s/projects/%d/files      (Files: Ready, In progress, Processing, Failed, Stopped)
`, projectName, res.TenantName, res.TenantID, len(files), len(targetLangs), targetLangs,
		res.TenantName, devseed.BaseURL(), res.ProjectID, devseed.BaseURL(), res.ProjectID)
}
