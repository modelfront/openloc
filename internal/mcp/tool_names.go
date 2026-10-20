package mcp

// Tool names. Every tool is prefixed `openloc_` because a user may have a
// dozen MCP servers connected at once, and bare names like `list_projects`
// collide conceptually with Linear, Asana, and Jira. Ugly, unambiguous, correct.
//
// tools/list is deterministic (the SDK sorts by name), which the spec asks for
// so clients can cache and LLM prompt-cache hit rates stay high. New tools
// declare their name constant next to their register<Domain> function.
const (
	// Reads.
	toolGetDocPage     = "openloc_get_doc_page"
	toolGetJob         = "openloc_get_job"
	toolGetJobLeverage = "openloc_get_job_leverage"
	toolGetProject     = "openloc_get_project"
	toolGetQAReport    = "openloc_get_qa_report"
	toolListCustomers  = "openloc_list_customers"
	toolListDocs       = "openloc_list_docs"
	toolListFiles      = "openloc_list_files"
	toolListJobs       = "openloc_list_jobs"
	toolListMembers    = "openloc_list_members"
	toolListMyTasks    = "openloc_list_my_tasks"
	toolListProjects   = "openloc_list_projects"
	toolListSegments   = "openloc_list_segments"
	toolLookupTerm     = "openloc_lookup_term"
	toolLookupConcept  = "openloc_lookup_concept"

	// Theme / appearance. User-scoped, not manager-gated.
	toolGetAppearance       = "openloc_get_appearance"
	toolUpsertPalette       = "openloc_upsert_custom_palette"
	toolSetActivePalette    = "openloc_set_active_palette"
	toolDeleteCustomPalette = "openloc_delete_custom_palette"
	toolQueryMetrics        = "openloc_query_metrics"
	toolSearchDocs          = "openloc_search_docs"
	toolSearchTM            = "openloc_search_tm"

	// Writes — every one gated by requireWrite.
	toolAddComment      = "openloc_add_comment"
	toolLockSegment     = "openloc_lock_segment"
	toolSaveDraft       = "openloc_save_draft"
	toolRunQA           = "openloc_run_qa"
	toolIgnoreQAFinding = "openloc_ignore_qa_finding"
	toolCreateChart     = "openloc_create_chart"
	toolSetDashFilters  = "openloc_set_dashboard_filters"
	toolSetChartFilters = "openloc_set_chart_filters"
	toolCreateProject   = "openloc_create_project"
	// AI configuration: discover the pieces, then assemble them.
	toolListAIPrompts       = "openloc_list_ai_prompts"
	toolListAIConnections   = "openloc_list_ai_connections"
	toolListAIEngines       = "openloc_list_ai_engines"
	toolListAIRouters       = "openloc_list_ai_routers"
	toolListMTRouters       = "openloc_list_mt_routers"
	toolListStepDefinitions = "openloc_list_step_definitions"
	toolListWorkflows       = "openloc_list_workflows"
	toolCreateAIPrompt      = "openloc_create_ai_prompt"
	toolUpdateAIPrompt      = "openloc_update_ai_prompt"
	toolCreateAIEngine      = "openloc_create_ai_engine"
	toolCreateAIRouter      = "openloc_create_ai_router"
	toolCreateMTRouter      = "openloc_create_mt_router"
	toolCreateStepDef       = "openloc_create_step_definition"
	toolCreateWorkflow      = "openloc_create_workflow"
	toolSetProjectMTRouter  = "openloc_set_project_mt_router"
	toolCreateCustomer      = "openloc_create_customer"
	toolCreateTermBase      = "openloc_create_term_base"
	toolAddTerm             = "openloc_add_term"
	toolCreateConcept       = "openloc_create_concept"
	toolAddTermToConcept    = "openloc_add_term_to_concept"
	toolUpdateConcept       = "openloc_update_concept"
	toolUpdateTerm          = "openloc_update_term"
	serverName              = "openloc"
	serverVersionUnset      = "dev"
	toolErrCodeNotFound     = "not_found"
	toolErrCodeInternal     = "internal"
	toolErrCodeDenied       = "denied"
	toolErrCodeReadOnly     = "read_only_token"
	toolErrCodeInvalid      = "invalid_argument"
	toolErrCodeConflict     = "conflict"
	toolErrCodeRateLimited  = "rate_limited"
)
