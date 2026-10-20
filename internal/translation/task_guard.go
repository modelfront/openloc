package translation

// task_guard.go — this package's side of the task edit guard (taskedit):
// every handler that changes translation text checks the job's current task
// first, and tells the editor when its write reopened a completed task.

import "openloc/internal/taskedit"

// activeStep is the in-progress task a history row is attributed to, nil when
// the job has none (no workflow, or edits that predate jobs).
func activeStep(res taskedit.Result) *int64 {
	if res.ActiveID == 0 {
		return nil
	}
	id := res.ActiveID
	return &id
}
