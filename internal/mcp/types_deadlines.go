package mcp

// --- Workflow step deadlines ---

type ListProjectWorkflowStepsIn struct {
	ProjectID int64 `json:"project_id" jsonschema:"the project whose workflow to read, from openloc_list_projects"`
}

// StepLanguageDeadline is one target language's own due date on a step.
type StepLanguageDeadline struct {
	TargetLanguage string `json:"target_language"`
	Deadline       string `json:"deadline"`
}

type ProjectWorkflowStepSummary struct {
	WorkflowStepID int64  `json:"workflow_step_id"`
	Name           string `json:"name"`
	StepOrder      int32  `json:"step_order"`
	StepType       string `json:"step_type"`
	AssigneeName   string `json:"assignee_name"`
	// Deadline is the step's due date for every target language without its
	// own; empty when none is set. An ai step never has one.
	Deadline          string                 `json:"deadline"`
	LanguageDeadlines []StepLanguageDeadline `json:"language_deadlines"`
}

type ProjectWorkflowStepList struct {
	ProjectID       int64                        `json:"project_id"`
	TargetLanguages []string                     `json:"target_languages"`
	Items           []ProjectWorkflowStepSummary `json:"items"`
	Count           int                          `json:"count"`
}

// WorkflowStepDeadlineIn sets one due date. Omit target_language to set the
// step's value for every language that has none of its own.
type WorkflowStepDeadlineIn struct {
	WorkflowStepID int64  `json:"workflow_step_id" jsonschema:"the step to set a due date on, from openloc_list_project_workflow_steps"`
	TargetLanguage string `json:"target_language,omitempty" jsonschema:"one of the project's target languages; omit to set the step's date for every language"`
	Deadline       string `json:"deadline" jsonschema:"ISO 8601 due date (e.g. 2026-09-24T17:00:00+02:00); empty clears it"`
}

type SetWorkflowStepDeadlinesIn struct {
	ProjectID int64                    `json:"project_id" jsonschema:"the project to schedule, from openloc_list_projects"`
	Deadlines []WorkflowStepDeadlineIn `json:"deadlines" jsonschema:"the due dates to set; steps left out keep theirs"`
}

type SetWorkflowStepDeadlinesResult struct {
	ProjectID int64                        `json:"project_id"`
	Items     []ProjectWorkflowStepSummary `json:"items"`
	Count     int                          `json:"count"`
	Note      string                       `json:"note"`
}
