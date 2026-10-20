// Package taskedit decides whether a segment edit may land on a job, from the
// job's current task: the in-progress task, or, when nothing is in progress,
// the last completed one. Every endpoint that changes translation text asks it
// first (Guard), so the editor's read-only states hold on the server too.
//
//   - in progress → editable, except while the AI step runs, while
//     pre-translation runs on a Pre-translation step, or when the job's
//     automation failed (the task is errored until it is re-run or completed);
//   - completed → a manager's edit reopens it; anyone else is refused;
//   - nothing started → refused.
package taskedit

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/stepdef"
)

// Refusal codes. The frontend branches on them (lib/task-edit.ts), so they
// are part of the API.
const (
	CodeTaskErrored   = "task_errored"
	CodeAIStepRunning = "ai_step_running"
	// CodePretranslationRunning: the job's Pre-translation step is still
	// filling in the file.
	CodePretranslationRunning = "pretranslation_running"
	CodeTaskCompleted         = "task_completed"
	CodeTaskNotStarted        = "task_not_started"
)

// LockedError is a refused edit: the HTTP status, a stable code and a message
// for the user.
type LockedError struct {
	Status  int
	Code    string
	Message string
}

func (e *LockedError) Error() string { return e.Message }

// StatusCode and ErrorCode let httputil.ServiceError write the refusal with
// its own status and code.
func (e *LockedError) StatusCode() int   { return e.Status }
func (e *LockedError) ErrorCode() string { return e.Code }

var (
	errTaskErrored = &LockedError{http.StatusConflict, CodeTaskErrored,
		"This task has an error. Re-run its automation or complete the task before editing."}
	errAIStepRunning = &LockedError{http.StatusConflict, CodeAIStepRunning,
		"The AI step is running — this task is locked until it completes"}
	errPretranslationRunning = &LockedError{http.StatusConflict, CodePretranslationRunning,
		"Pre-translation is running — this task is locked until it completes"}
	errTaskCompleted = &LockedError{http.StatusForbidden, CodeTaskCompleted,
		"This task is completed. Only a manager can reopen it by editing."}
	errTaskNotStarted = &LockedError{http.StatusConflict, CodeTaskNotStarted,
		"This task has not started yet."}
)

// State is what the decision reads about a job. ActiveID and LastCompletedID
// are 0 when the job has no such task.
type State struct {
	JobID              int64
	JobStatus          string
	JobCurrentStep     string
	TargetLanguage     string
	FileID             int64
	FileName           string
	ProjectID          int64
	HasWorkflow        bool
	ActiveID           int64
	ActiveIsAIStep     bool
	ActiveStepType     string
	ActiveStepOrder    int32
	LastCompletedID    int64
	LastCompletedOrder int32
	LastCompletedName  string
	LastCompletedType  string
}

// inFirstStep reports whether the current task is the workflow's first step
// (true for a job without a workflow). First-step-only actions — Apply TM/MT
// and clearing a segment — read it.
func (st State) inFirstStep() bool {
	switch {
	case st.ActiveID != 0:
		return st.ActiveStepOrder == 0
	case st.LastCompletedID != 0:
		return st.LastCompletedOrder == 0
	}
	return true
}

// Opts adjusts the rule for one caller.
type Opts struct {
	// AllowPretranslationError lets Apply TM/MT run on a task whose
	// pre-translation failed: running it again is how that error is fixed.
	AllowPretranslationError bool
	// NoReopen refuses a completed task even for a manager. A context marked
	// WithoutReopen (every MCP tool call) sets it too.
	NoReopen bool
}

type verdict int

const (
	allow verdict = iota + 1
	reopen
)

func decide(st State, role string, opts Opts) (verdict, *LockedError) {
	if !st.HasWorkflow {
		return allow, nil
	}
	failed := st.JobStatus == "failed"
	if st.ActiveID != 0 {
		if failed {
			if opts.AllowPretranslationError &&
				pipeline.FailedPhaseOf(st.JobStatus, st.JobCurrentStep) == pipeline.FailedPhasePretranslation {
				return allow, nil
			}
			return 0, errTaskErrored
		}
		// Only the AI step and a Pre-translation step lock: pre-translation
		// on a human or hybrid step stays editable, like the editor. A
		// Pre-translation step's work is the pipeline, and it completes
		// itself when the pipeline finishes.
		running := pipeline.IsPipelineRunning(st.JobStatus, st.JobCurrentStep)
		if st.ActiveIsAIStep && running && pipeline.IsAIStepLabel(st.JobCurrentStep) {
			return 0, errAIStepRunning
		}
		if st.ActiveStepType == stepdef.TypePretranslate && running {
			return 0, errPretranslationRunning
		}
		return allow, nil
	}
	if st.LastCompletedID != 0 {
		if failed {
			return 0, errTaskErrored
		}
		if !opts.NoReopen && auth.IsManagerRole(role) {
			return reopen, nil
		}
		return 0, errTaskCompleted
	}
	return 0, errTaskNotStarted
}

func stateFromJob(r db.GetTaskEditStateRow) State {
	return State{
		JobID: r.JobID, JobStatus: r.JobStatus, JobCurrentStep: r.JobCurrentStep,
		TargetLanguage: r.TargetLanguage, FileID: r.FileID, FileName: r.FileName, ProjectID: r.ProjectID,
		HasWorkflow: r.HasWorkflow, ActiveID: r.ActiveID, ActiveIsAIStep: r.ActiveIsAiStep, ActiveStepType: r.ActiveStepType, ActiveStepOrder: r.ActiveStepOrder,
		LastCompletedID: r.LastCompletedID, LastCompletedOrder: r.LastCompletedOrder,
		LastCompletedName: r.LastCompletedName, LastCompletedType: r.LastCompletedType,
	}
}

func stateFromTranslation(r db.GetTaskEditStateForTranslationRow) State {
	return stateFromJob(db.GetTaskEditStateRow(r))
}
