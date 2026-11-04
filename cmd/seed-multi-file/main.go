// Command seed-multi-file creates the "App strings (many small files)"
// project: forty short files of interface strings, one per app screen, in
// German and French, for trying the multi-file editor. The screens share
// strings ("Save", "Cancel") and near-copies of them ("Save changes", "Save
// all changes"), so opening several files together shows repetitions,
// propagation and internal fuzzy matches across files.
//
//	make seed-multi-file   (also run by `make reset`)
//
// The first screen's strings are confirmed, so the others find fuzzy matches
// in it from the start. Every job waits on the Translation step.
//
// Idempotent: the project (and its URL) is kept; its files and workflow steps
// are rebuilt, so edits made while testing are thrown away.
package main

import (
	"fmt"

	"openloc/internal/devseed"
)

const (
	projectName = "App strings (many small files)"
	screenCount = 40
)

var targetLangs = []string{"de", "fr"}

// strings is the pool each screen takes a few lines from. Neighbours are
// near-copies of each other, so a screen's line is often a fuzzy match of
// another screen's.
var strings = []string{
	"Save", "Save changes", "Save all changes", "Cancel", "Delete",
	"Delete this item?", "Delete these items?", "Sign in to your account", "Sign in to your work account",
	"Your changes have been saved.", "Your changes could not be saved.", "Try again", "Back",
	"Search projects", "Search files", "No results found.", "No files found.", "Settings",
	"Notification settings", "Account settings",
}

// screen is the i-th screen's strings: "Save" and "Cancel" on every one,
// plus three lines that walk through the pool.
func screen(i int) []string {
	out := []string{"Save", "Cancel"}
	for k := 0; k < 3; k++ {
		out = append(out, strings[(2+i*3+k)%len(strings)])
	}
	return out
}

func main() {
	translating := devseed.Translating
	confirmed := devseed.WithText(devseed.Translating, devseed.TextPlan{Approved: 5})
	files := make([]devseed.File, screenCount)
	for i := range files {
		sc := translating
		if i == 0 {
			sc = confirmed
		}
		files[i] = devseed.File{
			Name:      fmt.Sprintf("screen-%02d.txt", i+1),
			Segments:  screen(i),
			Scenarios: []devseed.Scenario{sc, sc},
		}
	}
	res := devseed.Run(devseed.Spec{
		ProjectName: projectName, TargetLangs: targetLangs, Steps: devseed.FourSteps,
		Files: files, Translations: true,
	})
	fmt.Printf(`
✓ Project %q ready — organization %q (tenant %d)
  %d files × %d target languages (%v), every job on its Translation step

  Sign in to the %q organization, then open:
    %s/projects/%d            (Tasks: select files, then Open in editor)
`, projectName, res.TenantName, res.TenantID, screenCount, len(targetLangs), targetLangs,
		res.TenantName, devseed.BaseURL(), res.ProjectID)
}
