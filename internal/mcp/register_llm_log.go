package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolListLLMCalls        = "openloc_list_llm_calls"
	toolSupportListLLMCalls = "openloc_support_list_llm_calls"
)

// llmLogWindow tells the model how far back the log reaches, so an empty
// result for an old job is not read as "no calls were made".
const llmLogWindow = "The log keeps recent calls only (a day or two; oldest gives the time of the " +
	"oldest call it still holds), so an older job has no calls left to read; partial true means " +
	"the log could not be read in full in time, so call again before concluding a call is missing. "

const llmLogUsage = "Filter by job_id, file_id, project_id, kind and a from/to time; group_by job or " +
	"file returns one row per job or file with its call, error and retry counts instead of the calls. " +
	"Calls come newest first, up to 20 per page; while has_more is true, call again with next_cursor."

// registerLLMLog registers the LLM call log tools, the MCP side of the
// /debug/llm page, when the server keeps a log. openloc_list_llm_calls is a
// manager's own organization's calls, on /mcp and in the built-in assistant.
// openloc_support_list_llm_calls is every organization's calls, on the
// operator tiers only, so never in the built-in assistant.
func registerLLMLog(s *Server, srv *sdk.Server, managerTier, operator bool, b Backend) {
	if !s.deps.LLMLog {
		return
	}
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListLLMCalls,
		title: "List LLM calls",
		description: "Managers only. List the requests OpenLoc sent to AI providers for this organization's " +
			"files (AI translation, quality estimation, post-editing, extensions): time, kind, provider, " +
			"model, engine, workflow step, job, file, project, status, the system and user messages and " +
			"the response. " + llmLogWindow + llmLogUsage,
		requireManager: true,
	}, b.ListLLMCalls)

	if !operator {
		return
	}
	addTool(s, srv, managerTier, toolSpec{
		name:  toolSupportListLLMCalls,
		title: "List every organization's LLM calls",
		description: "Support team only. List the requests OpenLoc sent to AI providers for every " +
			"organization, as the /debug/llm page shows them: time, kind, provider, model, engine, " +
			"workflow step, job, file, project, status, the system and user messages and the response. " +
			llmLogWindow + llmLogUsage,
		requireOperator: true,
	}, b.SupportListLLMCalls)
}
