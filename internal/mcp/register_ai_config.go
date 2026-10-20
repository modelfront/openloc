package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

// registerAIConfig registers the AI configuration tools.
//
// Assembly order: a system prompt, then an AI engine that
// binds them to a provider connection, then a step definition that runs the
// system, then a workflow that orders the steps. The list tools exist so a
// caller can find the ids each step needs.
//
// The three owner/admin tools mirror the REST gates exactly
// (auth.RequireRole("owner","admin") on /api/ai/prompts and
// /api/ai/systems): a project manager may read them but not create them,
// and MCP must not be the way around that.
func registerAIConfig(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListAIPrompts,
		title: "List AI system prompts",
		description: "List the organization's system prompts with their ids and output modes — " +
			"the instruction text an AI engine runs. Use before openloc_create_ai_engine to " +
			"pick one, or to check whether a suitable prompt already exists before creating " +
			"another. Filter by purpose: mt for translation prompts, qe_ape for review ones.",
		requireManager: true,
	}, b.ListAIPrompts)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListAIConnections,
		title: "List AI provider connections",
		description: "List the AI provider connections already set up, with their token ids and " +
			"the models each offers. openloc_create_ai_engine needs a token_id from here. API " +
			"keys are never returned, and a new connection cannot be created over MCP — it is " +
			"added once in AI settings.",
		requireManager: true,
		openWorld:      true,
	}, b.ListAIConnections)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListAIEngines,
		title: "List AI engines",
		description: "List the organization's AI engines with their ids and purpose — mt engines " +
			"translate, qe_ape engines review and post-edit. An engine is reached through a " +
			"router, so use this to see what a router can be built from, or to check what " +
			"already exists.",
		requireManager: true,
	}, b.ListAIEngines)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListAIRouters,
		title: "List AI routers",
		description: "List the organization's AI routers with their ids and what each one routes " +
			"— a router maps a target language to a qe_ape engine, with an optional default for " +
			"every other language. openloc_create_step_definition attaches one to a hybrid or " +
			"ai step; openloc_create_ai_router adds one.",
		requireManager: true,
	}, b.ListAIRouters)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListMTRouters,
		title: "List MT routers",
		description: "List the organization's MT routers with their ids and what each one routes " +
			"— a router maps a target language to a machine-translation engine, classic or LLM, " +
			"with an optional default for every other language. Pass an id to " +
			"openloc_set_project_mt_router; openloc_create_mt_router adds one.",
		requireManager: true,
	}, b.ListMTRouters)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListStepDefinitions,
		title: "List workflow step definitions",
		description: "List the step library — the reusable steps a workflow is composed from, " +
			"with their ids, types and attached AI router. Every organization starts with " +
			"Translation and Review. openloc_create_workflow orders these by id.",
		requireManager: true,
	}, b.ListStepDefinitions)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListWorkflows,
		title: "List workflows",
		description: "List the organization's library workflows with their ids and step names. " +
			"Pass a workflow_id to openloc_create_project to run a project on one.",
		requireManager: true,
	}, b.ListWorkflows)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateAIPrompt,
		title: "Create AI system prompt",
		description: "Create a system prompt: the standing instruction an AI engine runs on every " +
			"segment. Pick the mode deliberately — mt returns a translation, qe a verdict and " +
			"score, ape a corrected translation, qe_ape both — it cannot be changed afterwards. " +
			"Say what each reply field should contain, but do NOT restate the JSON structure: " +
			"the field names, types and the results envelope are appended automatically, as is " +
			"the inline-tag instruction on segments that carry tags, so describing those only " +
			"conflicts with them. Check openloc_list_ai_prompts first — the built-in " +
			"templates are often a better starting point than a new prompt.",
		requireManager:    true,
		requireOwnerAdmin: true,
		requireWrite:      true,
	}, b.CreateAIPrompt)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateAIPrompt,
		title: "Update AI system prompt",
		description: "Edit a system prompt's instruction text, its per-segment context and its " +
			"per-language rules. The mode cannot be changed — engines are bound to it — so " +
			"create a new prompt if you need a different one. Built-in prompts are read-only. " +
			"segment_context and language_additions REPLACE what is stored rather than adding " +
			"to it: read the prompt with openloc_list_ai_prompts and send the full set.",
		requireManager:    true,
		requireOwnerAdmin: true,
		requireWrite:      true,
	}, b.UpdateAIPrompt)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateAIEngine,
		title: "Create AI engine",
		description: "Create an AI engine by binding a system prompt to a provider " +
			"connection. The purpose must match the system prompt's mode: an " +
			"mt prompt makes an engine that translates, a qe/ape/qe_ape prompt one that reviews. " +
			"Get token_id from openloc_list_ai_connections and the prompt id from the " +
			"prompt list tools; create any missing prompt first.",
		requireManager:    true,
		requireOwnerAdmin: true,
		requireWrite:      true,
		openWorld:         true,
	}, b.CreateAIEngine)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateAIRouter,
		title: "Create AI router",
		description: "Create an AI router: the thing a hybrid or ai workflow step actually points " +
			"at. It maps a target language to a qe_ape engine and may name a default engine for " +
			"every other language, so one step can review German with one engine and everything " +
			"else with another. An engine is only reachable through a router — create the engine " +
			"with openloc_create_ai_engine first, then pass this router's id to " +
			"openloc_create_step_definition.",
		requireManager:    true,
		requireOwnerAdmin: true,
		requireWrite:      true,
	}, b.CreateAIRouter)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateMTRouter,
		title: "Create MT router",
		description: "Create an MT router: the thing a project points at for machine translation. " +
			"It maps a target language to a translation engine — a classic one (mt_engine_id) or " +
			"an LLM engine with purpose mt (ai_engine_id) — and may name a default for every " +
			"other language. Creating it does not translate anything on its own: pass the id to " +
			"openloc_set_project_mt_router to put a project on it.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateMTRouter)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateStepDef,
		title: "Create workflow step definition",
		description: "Add a reusable step to the step library, optionally running an AI router. " +
			"human = no router; hybrid = a person works the step and the router's engine reviews " +
			"it when the step starts; ai = that engine runs it. The router picks the qe_ape " +
			"engine by the job's target language — get one from openloc_list_ai_routers. " +
			"Machine translation is NOT attached here: it runs from the project's MT router, " +
			"not from a step. Check openloc_list_step_definitions first; Translation and " +
			"Review already exist.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateStepDefinition)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateWorkflow,
		title: "Create workflow",
		description: "Create a library workflow: an ordered list of existing step definitions. " +
			"It defines the order only — the steps themselves come from the step library, so " +
			"create any missing one with openloc_create_step_definition first. Pass the " +
			"resulting workflow_id to openloc_create_project.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateWorkflow)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetProjectMTRouter,
		title: "Set a project's MT router",
		description: "Point a project at an MT router, so machine translation for the project " +
			"runs through it — the step that makes an engine actually take effect, since " +
			"engines and routers are org-wide and the pointer is per project. The router " +
			"decides which engine each target language gets; pick one with " +
			"openloc_list_mt_routers. A project has exactly one router, so this replaces " +
			"whatever it pointed at.",
		requireManager: true,
		requireWrite:   true,
	}, b.SetProjectMTRouter)
}
