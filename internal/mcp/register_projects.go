package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

// registerProjects registers the projects tools.
func registerProjects(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetProject,
		title: "Get project details",
		description: "Get one project's details: name, status, source and target languages, " +
			"customer name, MT router and the engine each target language goes to " +
			"(mt_engines; mt_no_engine for languages no engine translates), and the " +
			"project's own deadline. That date is not a job's own: " +
			"a job carries whatever its current workflow step resolved to, which falls " +
			"back to the project date only when nothing more specific is set. " +
			"For job due dates use openloc_list_jobs, which also lists its work items.",
		requireManager: true,
	}, b.GetProject)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListCustomers,
		title: "List customers",
		description: "List the customers translation memories can be tagged with, with their " +
			"ids, for openloc_create_tm. A PROJECT's customer is a value of the built-in " +
			"Customer label instead: list those with openloc_list_labels, and use that " +
			"value id with openloc_list_projects and openloc_create_project.",
		requireManager: true,
	}, b.ListCustomers)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListFiles,
		title: "List project files",
		description: "List the files in a project, newest first, with format and segment count. " +
			"Unlike openloc_list_jobs this shows files that have no jobs yet " +
			"(uploaded but not yet configured for a target language). " +
			"When has_more is true, pass next_cursor as cursor for the next page.",
		requireManager: true,
	}, b.ListFiles)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListMembers,
		title: "List organization members",
		description: "List the people in your organization who can be assigned work — " +
			"vendors/linguists, project managers, admins — with their role, their " +
			"language pairs (as \"source→target\", e.g. \"en→es\"), and active_jobs " +
			"(jobs they are working on RIGHT NOW). One call answers \"which vendors " +
			"translate English into Spanish?\" (filter language_pairs) and \"who is " +
			"currently busy?\" (active_jobs > 0). There is no separate vendor role: " +
			"vendors are members, usually with the linguist role. An empty " +
			"language_pairs means none recorded, not none supported. Email addresses " +
			"are deliberately not included. Sorted by name; when has_more is true, " +
			"pass next_cursor as cursor for the next page.",
		requireManager: true,
	}, b.ListMembers)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListProjects,
		title: "List projects",
		description: "List translation projects in your organization, newest first, each with " +
			"its customer_name, its own deadline (empty when none is set), its workflow " +
			"steps in order (workflow_steps, step_count), and its machine translation: " +
			"mt_router_name and mt_engines, the target languages grouped by the engine the " +
			"router sends them to (mt_no_engine lists languages no engine translates). Start " +
			"here when you need a project_id, or to answer \"what work do we have for " +
			"customer X?\", \"which projects are overdue?\", \"which projects have more than " +
			"one step?\" or \"which MT engines do my projects use?\" — all " +
			"of it is on every row, so no per-project lookups are needed. Optionally filter " +
			"by status, customer_id, source_language, target_language or a name query. " +
			"Paginated: while a response has next_cursor the list is NOT complete — call " +
			"again with cursor set to it before counting or filtering. A project's deadline " +
			"is not a job's own — see openloc_get_project for how a job's date is resolved.",
		requireManager: true,
	}, b.ListProjects)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateProject,
		title: "Create project",
		description: "Create a translation project. If the name, source language, or target " +
			"languages are missing or ambiguous, ASK the user before calling — and restate " +
			"what you'll create in one line for confirmation. Prefer template_id when the " +
			"org has templates: it copies TM/MT/terminology/workflow/QA settings; without " +
			"one the project gets sensible defaults the user can change later. After " +
			"creating, add a document the user attached in the chat with " +
			"openloc_attach_uploaded_file, a text file you can read with openloc_upload_text_file, " +
			"or open the project with navigate_to so they can upload there.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateProject)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateCustomer,
		title: "Create customer",
		description: "Create a customer that translation memories can be tagged with. Just a " +
			"name. Check openloc_list_customers first so you don't create a duplicate. This " +
			"is not a project's customer: for that, add a value to the Customer label with " +
			"openloc_add_label_value.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateCustomer)
}
