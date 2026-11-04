package scopecomplete

import "openloc/internal/segmentbulk"

// Outcomes of one job.
const (
	// OutcomeAdvanced: the step is completed and the next one started.
	OutcomeAdvanced = "advanced"
	// OutcomeCompleted: the last step is completed and so is the job.
	OutcomeCompleted = "completed"
	// OutcomeBlocked: approve all left rows the step cannot complete without
	// (Blocked lists them); nothing else was done.
	OutcomeBlocked = "blocked"
	// OutcomeFailed: a step of the way refused or failed; Error says why.
	OutcomeFailed = "failed"
)

// JobResult is what happened to one job.
type JobResult struct {
	JobID      int64              `json:"job_id"`
	Outcome    string             `json:"outcome"`
	NextStepID int64              `json:"next_step_id,omitempty"`
	Blocked    []segmentbulk.Skip `json:"blocked,omitempty"`
	Error      *JobError          `json:"error,omitempty"`
}

// JobError is a refusal or failure, with the code the frontend branches on
// when the step said one.
type JobError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}
