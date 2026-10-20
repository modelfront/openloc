package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolListSteps        = "openloc_list_project_workflow_steps"
	toolSetStepDeadlines = "openloc_set_workflow_step_deadlines"
)

// registerDeadlines registers the project workflow-step deadline pair: a
// project's jobs are file × target language, so a step can be due on a
// different date per language. Manager-only, like the REST route.
func registerDeadlines(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListSteps,
		title: "List a project's workflow steps",
		description: "List a project's workflow steps in order, with who picks each one up by " +
			"default and when it is due — the step's own date plus any target language " +
			"with a date of its own. Start here to answer \"when is X due?\" and to get " +
			"the workflow_step_id openloc_set_workflow_step_deadlines needs. " +
			"An AI step runs by itself and never has a deadline.",
		requireManager: true,
	}, b.ListProjectWorkflowSteps)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetStepDeadlines,
		title: "Set workflow step deadlines",
		description: "Set when a project's workflow steps are due. A project's jobs are one file " +
			"per target language, so a step can be due on a different date per language: " +
			"give a target_language for one language's own date, or leave it out to set " +
			"the step's date for every language that has none. Jobs that already exist " +
			"are re-dated too, except steps already completed. " +
			"A patch — a step you leave out keeps its dates; an empty deadline clears one. " +
			"Get the ids from openloc_list_project_workflow_steps. " +
			"This is not the project's own deadline, which applies only where neither " +
			"the language nor the step has a date of its own.",
		requireManager: true,
		requireWrite:   true,
	}, b.SetWorkflowStepDeadlines)
}
