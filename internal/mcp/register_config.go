package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolListMTEngines         = "openloc_list_mt_engines"
	toolGetMTEngineCatalog    = "openloc_get_mt_engine_catalog"
	toolListMTLanguages       = "openloc_list_mt_languages"
	toolCreateMTEngine        = "openloc_create_mt_engine"
	toolUpdateMTEngine        = "openloc_update_mt_engine"
	toolDeleteMTEngine        = "openloc_delete_mt_engine"
	toolGetAIRouter           = "openloc_get_ai_router"
	toolGetMTRouter           = "openloc_get_mt_router"
	toolGetStepDefinition     = "openloc_get_step_definition"
	toolGetWorkflow           = "openloc_get_workflow"
	toolPreviewAIPrompt       = "openloc_preview_ai_prompt"
	toolUpdateAIEngine        = "openloc_update_ai_engine"
	toolUpdateAIRouter        = "openloc_update_ai_router"
	toolUpdateMTRouter        = "openloc_update_mt_router"
	toolUpdateStepDefinition  = "openloc_update_step_definition"
	toolUpdateWorkflow        = "openloc_update_workflow"
	toolDeleteAIEngine        = "openloc_delete_ai_engine"
	toolDeleteAIRouter        = "openloc_delete_ai_router"
	toolDeleteMTRouter        = "openloc_delete_mt_router"
	toolDeleteStepDefinition  = "openloc_delete_step_definition"
	toolDeleteWorkflow        = "openloc_delete_workflow"
	toolListTemplates         = "openloc_list_templates"
	toolGetTemplate           = "openloc_get_template"
	toolCreateTemplate        = "openloc_create_template"
	toolDuplicateTemplate     = "openloc_duplicate_template"
	toolUpdateTemplate        = "openloc_update_template"
	toolListQAProfiles        = "openloc_list_qa_profiles"
	toolGetQAProfile          = "openloc_get_qa_profile"
	toolCreateQAProfile       = "openloc_create_qa_profile"
	toolUpdateQAProfile       = "openloc_update_qa_profile"
	toolUpsertQARules         = "openloc_upsert_qa_rules"
	toolDeleteQAProfile       = "openloc_delete_qa_profile"
	guardedCallsNote          = " Two calls: the first changes nothing and returns a preview; call again with confirm_token after the user approves."
	configProjectsUnaffected  = " Projects copy this when they are set up, so existing projects do not change."
	configSharedConfiguration = " The change applies org-wide, to everything that uses it."
)

// registerConfig registers the MT engines, AI and MT routers, step and workflow libraries, templates, QA profiles tools.
//
// Gates mirror internal/api/api.go. MT engines, MT routers, step definitions,
// workflows, templates and QA profiles are manager end to end.
// AI prompts, AI engines and AI routers are manager-read and
// owner/admin-write. Edits to shared configuration and every delete are
// guarded.
func registerConfig(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	registerConfigMT(s, srv, managerTier, b)
	registerConfigAI(s, srv, managerTier, b)
	registerConfigLibrary(s, srv, managerTier, b)
	registerConfigTemplates(s, srv, managerTier, b)
	registerConfigQA(s, srv, managerTier, b)
}

func registerConfigMT(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListMTEngines,
		title: "List MT engines",
		description: "List the organization's classic machine-translation engines with their ids, type, " +
			"options and whether they can translate (unusable says why not). An MT router routes a " +
			"language to one of these by mt_engine_id. API keys and endpoints are never returned.",
		requireManager: true,
	}, b.ListMTEngines)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetMTEngineCatalog,
		title: "Get MT engine types",
		description: "List the MT engine types this deployment can build, with the options each takes. " +
			"Types with needs_key=true (DeepL, Google) spend a provider key and are created in AI " +
			"settings; the others can be created with openloc_create_mt_engine.",
		requireManager: true,
	}, b.GetMTEngineCatalog)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListMTLanguages,
		title: "List an MT engine's languages",
		description: "List the languages an MT engine reads (sources) and writes (targets). Use it before " +
			"routing a target language to the engine.",
		requireManager: true,
		openWorld:      true,
	}, b.ListMTLanguages)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateMTEngine,
		title: "Create MT engine",
		description: "Create a classic MT engine of a type this deployment supplies (needs_key=false in " +
			"openloc_get_mt_engine_catalog). An engine does nothing until an MT router routes a " +
			"language to it — use openloc_create_mt_router or openloc_update_mt_router next.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateMTEngine)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateMTEngine,
		title: "Update MT engine",
		description: "Rename an MT engine, switch it on or off, or replace its options. The type, key " +
			"and endpoint are not editable here." + configSharedConfiguration + guardedCallsNote,
		requireManager: true,
	}, b.PreviewUpdateMTEngine, b.UpdateMTEngine)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteMTEngine,
		title: "Delete MT engine",
		description: "Delete an MT engine. Refused while any MT router routes to it — change those " +
			"routers first." + guardedCallsNote,
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteMTEngine, b.DeleteMTEngine)
}

func registerConfigAI(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetAIRouter,
		title: "Get AI router",
		description: "Read one AI router with its default engine, every language rule (with engine ids " +
			"and names) and the library steps that run it. Read it before " +
			"openloc_update_ai_router, which replaces the whole route list.",
		requireManager: true,
	}, b.GetAIRouter)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetMTRouter,
		title: "Get MT router",
		description: "Read one MT router with its default engine, every language rule (with engine ids " +
			"and names) and how many projects and templates use it. Read it before " +
			"openloc_update_mt_router, which replaces the whole route list.",
		requireManager: true,
	}, b.GetMTRouter)

	addTool(s, srv, managerTier, toolSpec{
		name:           toolPreviewAIPrompt,
		title:          "Preview AI prompt",
		description:    "Show the system message exactly as an AI engine sends it: the prompt's own instruction text plus what the platform appends (language direction, reply format, context). Give prompt_id for a stored prompt, or mode and text for a draft. Changes nothing.",
		requireManager: true,
	}, b.PreviewAIPrompt)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateAIEngine,
		title: "Update AI engine",
		description: "Change an AI engine's name, on/off state, connection, model, system prompt or " +
			"bad-output action; omitted fields keep their value. The purpose cannot change." +
			configSharedConfiguration + guardedCallsNote,
		requireManager:    true,
		requireOwnerAdmin: true,
		openWorld:         true,
	}, b.PreviewUpdateAIEngine, b.UpdateAIEngine)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateAIRouter,
		title: "Update AI router",
		description: "Replace an AI router's name, default engine and language rules — send the full set, " +
			"read with openloc_get_ai_router. Every hybrid or ai step on this router reviews with the " +
			"new routes from the next job it starts." + guardedCallsNote,
		requireManager:    true,
		requireOwnerAdmin: true,
	}, b.PreviewUpdateAIRouter, b.UpdateAIRouter)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateMTRouter,
		title: "Update MT router",
		description: "Replace an MT router's name, default engine and language rules — send the full set, " +
			"read with openloc_get_mt_router. Every project and template on this router translates " +
			"with the new routes from now on." + guardedCallsNote,
		requireManager: true,
	}, b.PreviewUpdateMTRouter, b.UpdateMTRouter)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteAIEngine,
		title: "Delete AI engine",
		description: "Delete an AI engine. Refused while any AI or MT router routes to it — change those " +
			"routers first." + guardedCallsNote,
		requireManager:    true,
		requireOwnerAdmin: true,
		destructive:       true,
	}, b.PreviewDeleteAIEngine, b.DeleteAIEngine)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteAIRouter,
		title: "Delete AI router",
		description: "Delete an AI router. Refused while any workflow step (library, project or " +
			"template) still points at it." + guardedCallsNote,
		requireManager:    true,
		requireOwnerAdmin: true,
		destructive:       true,
	}, b.PreviewDeleteAIRouter, b.DeleteAIRouter)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteMTRouter,
		title: "Delete MT router",
		description: "Delete an MT router. Refused while any project or template still points at it." +
			guardedCallsNote,
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteMTRouter, b.DeleteMTRouter)
}

func registerConfigLibrary(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetStepDefinition,
		title: "Get workflow step definition",
		description: "Read one step of the step library: its name, type, AI router and the library " +
			"workflows that compose it.",
		requireManager: true,
	}, b.GetStepDefinition)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetWorkflow,
		title: "Get workflow",
		description: "Read one library workflow: its ordered steps with default assignees, and how many " +
			"projects and templates copied it. Read it before openloc_update_workflow.",
		requireManager: true,
	}, b.GetWorkflow)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateStepDefinition,
		title: "Update workflow step definition",
		description: "Replace a library step's name, type and AI router, with the same rules as " +
			"openloc_create_step_definition. Library workflows pick up the change." +
			configProjectsUnaffected + guardedCallsNote,
		requireManager: true,
	}, b.PreviewUpdateStepDefinition, b.UpdateStepDefinition)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateWorkflow,
		title: "Update workflow",
		description: "Rename a library workflow and replace its ordered step list (full list, with " +
			"default assignees)." + configProjectsUnaffected + guardedCallsNote,
		requireManager: true,
	}, b.PreviewUpdateWorkflow, b.UpdateWorkflow)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteStepDefinition,
		title: "Delete workflow step definition",
		description: "Remove a step from the step library. Refused while a library workflow composes " +
			"it." + configProjectsUnaffected + guardedCallsNote,
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteStepDefinition, b.DeleteStepDefinition)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:           toolDeleteWorkflow,
		title:          "Delete workflow",
		description:    "Remove a workflow from the library." + configProjectsUnaffected + guardedCallsNote,
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteWorkflow, b.DeleteWorkflow)
}

func registerConfigTemplates(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListTemplates,
		title: "List project templates",
		description: "List the organization's project templates, newest first, with what a project " +
			"made from each starts with. Pass a template_id to openloc_create_project to use one.",
		requireManager: true,
	}, b.ListTemplates)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetTemplate,
		title: "Get project template",
		description: "Read one template with its settings: upload automation, TM scoring, QA profile, " +
			"MT router, TM profile, workflow steps, and how many projects were created from it.",
		requireManager: true,
	}, b.GetTemplate)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateTemplate,
		title: "Create project template",
		description: "Create a project template: the name, languages, customer and settings new " +
			"projects start with. Get ids from the list tools named on each field. Without " +
			"workflow_id the template gets Translation then Review.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateTemplate)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolDuplicateTemplate,
		title: "Duplicate project template",
		description: "Copy a template with all its settings and assignments, named \"Copy of …\". " +
			"Rename it with openloc_update_template.",
		requireManager: true,
		requireWrite:   true,
	}, b.DuplicateTemplate)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateTemplate,
		title: "Update project template",
		description: "Change a template; omitted fields keep their value, and 0 clears an id field." +
			configProjectsUnaffected + guardedCallsNote,
		requireManager: true,
	}, b.PreviewUpdateTemplate, b.UpdateTemplate)
}

func registerConfigQA(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListQAProfiles,
		title: "List QA profiles",
		description: "List the organization's QA profiles — the sets of checks a project's QA runs. " +
			"Projects and templates pick one by qa_profile_id.",
		requireManager: true,
	}, b.ListQAProfiles)

	addTool(s, srv, managerTier, toolSpec{
		name:           toolGetQAProfile,
		title:          "Get QA profile",
		description:    "Read one QA profile with every rule (check type, on/off, severity, options) and how many projects and templates use it.",
		requireManager: true,
	}, b.GetQAProfile)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateQAProfile,
		title: "Create QA profile",
		description: "Create a QA profile, optionally with its first rules. It checks nothing until a " +
			"project or template uses it.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateQAProfile)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateQAProfile,
		title: "Update QA profile",
		description: "Rename a QA profile or replace its description. Projects and templates point at " +
			"the profile, so they show the change." + guardedCallsNote,
		requireManager: true,
	}, b.PreviewUpdateQAProfile, b.UpdateQAProfile)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpsertQARules,
		title: "Set QA profile rules",
		description: "Create or replace rules on a QA profile, one per check type; rules you do not list " +
			"stay as they are. The next QA run of every project using the profile applies them." +
			guardedCallsNote,
		requireManager: true,
	}, b.PreviewUpsertQARules, b.UpsertQARules)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteQAProfile,
		title: "Delete QA profile",
		description: "Delete a QA profile and its rules. Refused while any project or template uses it." +
			guardedCallsNote,
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteQAProfile, b.DeleteQAProfile)
}
