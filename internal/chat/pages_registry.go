package chat

import "regexp"

// Page and anchor catalog. Every route in web/src/App.tsx that a signed-in
// user can open has an entry (pinned by TestRegistryCoversAppRoutes); every
// anchor id exists as a data-guide attribute in web/src (pinned by
// TestAnchorsExistInFrontend).
//
// An anchor is a named UI element the assistant may point at with the
// highlight and start_tour tools. Ids are "<area>.<element>". Explains is
// what the model tells the user the element is for.

// navAnchors are on every page: the left rail items (the editor's collapsed
// rail still shows them as icons) and the assistant panel. The panel anchors
// other than chat.launcher sit inside the panel, which the tour runner opens
// first (web/src/lib/guide.ts).
var navAnchors = []Anchor{
	{"nav.projects", "Projects in the left menu", "all translation projects"},
	{"nav.tasks", "Tasks in the left menu", "the user's own assigned work"},
	{"nav.memories", "Translation memories in the left menu", "TM collections and TM profiles"},
	{"nav.terminology", "Terminology in the left menu", "term bases (glossaries)"},
	{"nav.labels", "Labels in the left menu", "project labels such as customer and domain"},
	{"nav.templates", "Templates in the left menu", "project templates with preset settings"},
	{"nav.workflows", "Workflows in the left menu", "workflow and step libraries"},
	{"nav.mt", "MT in the left menu", "machine translation routers, engines and prompts"},
	{"nav.ai", "AI in the left menu", "AI review (QE+APE) routers, engines and prompts"},
	{"nav.integrations", "Integrations in the left menu", "CMS connectors"},
	{"nav.quality", "QA profiles in the left menu", "QA check profiles"},
	{"nav.dashboards", "Dashboards in the left menu", "saved metric charts"},
	{"chat.launcher", "the assistant button", "opens the AI assistant, bottom right"},
	{"chat.messages", "the assistant's conversation", "answers with charts, highlights on the page, tours and confirm cards"},
	{"chat.mode", "the assistant's mode", "read-only by default; write mode may change things, and deletes ask first"},
}

// demoAnchors exist only in the demo organization, in the example
// conversation its assistant panel shows (ChatDemoPanel). The live assistant
// never runs there, so it cannot point at them, and a saved tour outside the
// demo leaves their steps out.
var demoAnchors = []Anchor{
	{"chat.example.project", "the example: a project created and run", "one exchange of the example conversation"},
	{"chat.example.knowledge", "the example: an answer from help and memories", "one exchange of the example conversation"},
	{"chat.example.dashboard", "the example: a dashboard built in the chat", "one exchange of the example conversation"},
	{"chat.example.tour", "the example: a guided tour made on request", "one exchange of the example conversation"},
	{"chat.example.confirm", "the example: a delete that waits for a click", "one exchange of the example conversation"},
}

var pages = []pageDef{
	{
		name:     "job_editor",
		pattern:  regexp.MustCompile(`^/projects/(\d+)/jobs/(\d+)/edit$`),
		idParams: []string{"project_id", "job_id"},
		describe: "the CAT editor for job %[2]s in project %[1]s (segments, translations, QA)",
		path:     "/projects/%d/jobs/%d/edit",
		anchors: []Anchor{
			{"editor.segments", "the segment grid", "source on the left, translation on the right; click a target cell to edit"},
			{"editor.toolbar", "the editor toolbar", "save, confirm, find and replace, QA and spell check"},
			{"editor.filters", "the segment filters", "show segments by status, origin, QA result or text"},
			{"editor.sidebar", "the Resources panel", "TM matches, terminology, comments, QA and history for the active segment"},
			{"editor.complete", "the Complete button", "finish this workflow step and hand the job to the next step"},
		},
	},
	{
		name:     "project_create",
		pattern:  regexp.MustCompile(`^/projects/new$`),
		describe: "the New project form",
		path:     "/projects/new",
		anchors: []Anchor{
			{"project_create.start", "the From template / From scratch choice", "start with a template's settings or configure everything yourself"},
			{"project_create.files", "the file upload area", "add the documents to translate"},
			{"project_create.submit", "the Create project button", "create the project and start its workflow"},
		},
	},
	{
		name:     "project",
		pattern:  regexp.MustCompile(`^/projects/(\d+)$`),
		idParams: []string{"project_id"},
		describe: "the Tasks tab of project %s: each job per target language and who works on which step",
		path:     "/projects/%d",
		anchors: []Anchor{
			{"project.tabs", "the project tabs", "files, tasks, analyses, workflow, settings and history of the project"},
		},
	},
	{
		name:     "project_files",
		pattern:  regexp.MustCompile(`^/projects/(\d+)/files$`),
		idParams: []string{"project_id"},
		describe: "the Files tab of project %s (upload, download and remove files)",
		path:     "/projects/%d/files",
		anchors: []Anchor{
			{"project.files", "the files list", "the project's files; their jobs per target language are on the Tasks tab"},
		},
	},
	{
		name:     "project_analyses",
		pattern:  regexp.MustCompile(`^/projects/(\d+)/analyses$`),
		idParams: []string{"project_id"},
		describe: "the Analyses tab of project %s (word counts and TM leverage)",
		path:     "/projects/%d/analyses",
		anchors: []Anchor{
			{"project_analyses.list", "the saved analyses", "word counts and translation memory leverage per job; open one to see its breakdown"},
			{"project_analyses.new", "the New analysis button", "compute a fresh snapshot of TM and MT leverage for the project's jobs"},
		},
	},
	{
		name:     "project_workflow",
		pattern:  regexp.MustCompile(`^/projects/(\d+)/workflow$`),
		idParams: []string{"project_id"},
		describe: "the Workflow tab of project %s (steps and assignments)",
		path:     "/projects/%d/workflow",
		anchors: []Anchor{
			{"project_workflow.picker", "the workflow picker and steps", "choose a library workflow for the project and see its steps"},
		},
	},
	{
		name:     "project_settings",
		pattern:  regexp.MustCompile(`^/projects/(\d+)/settings$`),
		idParams: []string{"project_id"},
		describe: "the Settings tab of project %s (TM, terminology, MT, QA)",
		path:     "/projects/%d/settings",
		anchors: []Anchor{
			{"project_settings.editor", "the project settings", "translation memories, term bases, MT router, QA profile and TM profile this project uses"},
		},
	},
	{
		name:     "project_history",
		pattern:  regexp.MustCompile(`^/projects/(\d+)/history$`),
		idParams: []string{"project_id"},
		describe: "the History tab of project %s",
		path:     "/projects/%d/history",
		anchors: []Anchor{
			{"project_history.list", "the project history", "every change to the project: who did what, and when"},
			{"project_history.filters", "the history filters", "show only one type of change, or one person's changes"},
		},
	},
	{
		name:     "projects",
		pattern:  regexp.MustCompile(`^/(projects)?$`),
		describe: "the projects list",
		path:     "/projects",
		anchors: []Anchor{
			{"projects.list", "the first project in the list", "each row is one project, with its languages, status, customer and deadline"},
			{"projects.new", "the New project button", "start a new translation project"},
			{"projects.filters", "the project filters", "narrow the list by status, customer, creator or text"},
		},
	},
	{
		name:     "tasks",
		pattern:  regexp.MustCompile(`^/tasks$`),
		describe: "their Tasks page (their own assigned work; owners and admins see every assignee's)",
		path:     "/tasks",
		anchors: []Anchor{
			{"tasks.list", "the task list", "one card per job on its current step, with deadline, step and progress; filters for search, step and deadline"},
		},
	},
	{
		name:     "learn",
		pattern:  regexp.MustCompile(`^/learn$`),
		describe: "the Learn page (the guided tours the user can take, and the way into the Playground)",
		path:     "/learn",
		anchors: []Anchor{
			{"learn.tours", "the tour list", "one card per tour with a Start tour button; tours run in the Playground"},
		},
	},
	{
		name:     "reported_issues",
		pattern:  regexp.MustCompile(`^/issues$`),
		describe: "Reported issues (bugs, enhancements and feature requests sent to the support team, with their status: a manager sees the whole organization's, anyone else their own)",
		path:     "/issues",
	},
	{
		name:     "issue",
		pattern:  regexp.MustCompile(`^/issues/(\d+)$`),
		idParams: []string{"issue_id"},
		describe: "reported issue %s, with its conversation with the support team",
		path:     "/issues/%d",
	},
	{
		name:     "tm_profile",
		pattern:  regexp.MustCompile(`^/memories/profiles/(\d+)$`),
		idParams: []string{"tm_profile_id"},
		describe: "TM profile %s",
		path:     "/memories/profiles/%d",
		anchors: []Anchor{
			{"tm_profile.settings", "the profile's settings", "fuzzy threshold, match penalties, and the memories it reads and writes with their priority"},
			{"tm_profile.usage", "the usage line", "how many projects and templates follow this profile; edits apply to all of them"},
		},
	},
	{
		name:     "tm_profiles",
		pattern:  regexp.MustCompile(`^/memories/profiles$`),
		describe: "the TM profiles list",
		path:     "/memories/profiles",
		anchors: []Anchor{
			{"tm_profiles.list", "the TM profiles", "reusable bundles of memories and match settings that projects and templates follow"},
			{"tm_profiles.new", "the New TM profile button", "create a profile"},
		},
	},
	{
		name:     "tm",
		pattern:  regexp.MustCompile(`^/memories/(\d+)$`),
		idParams: []string{"tm_id"},
		describe: "the browser of translation memory %s",
		path:     "/memories/%d",
		anchors: []Anchor{
			{"tm.entries", "the memory's entries", "source and target pairs, searchable, editable in place"},
			{"tm.import", "the Import TMX button", "load translation units from a TMX file"},
		},
	},
	{
		name:     "tms",
		pattern:  regexp.MustCompile(`^/memories$`),
		describe: "the translation memories list",
		path:     "/memories",
		anchors: []Anchor{
			{"tms.new", "the New translation memory button", "create an empty TM"},
			{"tms.list", "the translation memories", "every TM with its languages and size; open one to browse its entries"},
		},
	},
	{
		name:     "term_base",
		pattern:  regexp.MustCompile(`^/terminology/(\d+)$`),
		idParams: []string{"term_base_id"},
		describe: "the browser of term base %s",
		path:     "/terminology/%d",
		anchors: []Anchor{
			{"term_base.concepts", "the concepts", "each concept with its approved terms per language"},
			{"term_base.import", "the Import button", "load terms from CSV or TBX"},
		},
	},
	{
		name:     "terminology",
		pattern:  regexp.MustCompile(`^/terminology$`),
		describe: "the term bases list",
		path:     "/terminology",
		anchors: []Anchor{
			{"terminology.new", "the New term base button", "create a glossary"},
		},
	},
	{
		name:     "mt_routers",
		pattern:  regexp.MustCompile(`^/mt$`),
		describe: "the MT routers page (which MT engine translates which language)",
		path:     "/mt",
		anchors: []Anchor{
			{"mt_routers.list", "the MT routers", "which machine translation engine translates each target language"},
		},
	},
	{
		name:     "mt_engines",
		pattern:  regexp.MustCompile(`^/mt/engines$`),
		describe: "the MT engines page",
		path:     "/mt/engines",
		anchors: []Anchor{
			{"mt_engines.providers", "the provider engines", "machine translation engines from a provider key in AI settings, with that provider's options"},
			{"mt_engines.llm", "the LLM engines", "engines that translate with a large language model and an MT prompt"},
		},
	},
	{
		name:     "mt_prompts",
		pattern:  regexp.MustCompile(`^/mt/prompts$`),
		describe: "the MT prompts page (LLM translation prompts)",
		path:     "/mt/prompts",
		anchors: []Anchor{
			{"mt_prompts.list", "the first MT prompt in the list", "one of the instructions the LLM translation engines follow; built-ins can be duplicated"},
		},
	},
	{
		name:     "mt_prompt",
		pattern:  regexp.MustCompile(`^/mt/prompts/(\d+)$`),
		idParams: []string{"prompt_id"},
		describe: "the editor of MT prompt %s",
		path:     "/mt/prompts/%d",
		anchors: []Anchor{
			{"mt_prompt.form", "the prompt editor", "the base prompt and extra prompts per target language"},
			{"mt_prompt.instructions", "the base prompt", "the full instructions, in plain words, that the model follows"},
			{"mt_prompt.languages", "the language rules", "extra instructions for single target languages, such as a formal register"},
			{"mt_prompt.context", "the segment context", "what each segment brings to the model, such as TM matches and glossary terms"},
			{"mt_prompt.schema", "the output format", "the reply format and the JSON schema the provider holds every reply to"},
		},
	},
	{
		name:     "ai_routers",
		pattern:  regexp.MustCompile(`^/ai$`),
		describe: "the AI routers page (which AI review engine runs for which language)",
		path:     "/ai",
		anchors: []Anchor{
			{"ai_routers.list", "the AI routers", "which AI review engine checks and post-edits each target language"},
		},
	},
	{
		name:     "ai_engines",
		pattern:  regexp.MustCompile(`^/ai/engines$`),
		describe: "the AI engines page (QE+APE review engines)",
		path:     "/ai/engines",
		anchors: []Anchor{
			{"ai_engines.list", "the AI engines", "each review engine: its provider, model, prompt and mode (QE, QE+APE or APE)"},
		},
	},
	{
		name:     "ai_prompts",
		pattern:  regexp.MustCompile(`^/ai/prompts$`),
		describe: "the AI prompts page (review prompts)",
		path:     "/ai/prompts",
		anchors: []Anchor{
			{"ai_prompts.list", "the first review prompt in the list", "one of the instructions an AI engine follows, with the context each segment carries"},
		},
	},
	{
		name:     "ai_prompt",
		pattern:  regexp.MustCompile(`^/ai/prompts/(\d+)$`),
		idParams: []string{"prompt_id"},
		describe: "the editor of AI prompt %s",
		path:     "/ai/prompts/%d",
		anchors: []Anchor{
			{"ai_prompt.form", "the prompt editor", "what the prompt does (QE, QE+APE or APE), its base prompt and extra prompts per target language"},
			{"ai_prompt.instructions", "the base prompt", "the full instructions, in plain words, that the model follows"},
			{"ai_prompt.languages", "the language rules", "extra instructions for single target languages, such as a formal register"},
			{"ai_prompt.context", "the segment context", "what each segment brings to the model, such as TM matches and glossary terms"},
			{"ai_prompt.schema", "the output format", "the reply format and the JSON schema the provider holds every reply to"},
		},
	},
	{
		name:     "templates",
		pattern:  regexp.MustCompile(`^/templates$`),
		describe: "the project templates list",
		path:     "/templates",
		anchors: []Anchor{
			{"templates.new", "the New template button", "create a template that presets project settings"},
			{"templates.list", "the template list", "every project template; open one to change its settings and workflow"},
		},
	},
	{
		name:     "template",
		pattern:  regexp.MustCompile(`^/templates/(\d+)$`),
		idParams: []string{"template_id"},
		describe: "template %s",
		path:     "/templates/%d",
		anchors: []Anchor{
			{"template.tabs", "the template tabs", "the template's settings and its workflow"},
			{"template.settings", "the template settings", "languages, memories, term bases, MT, QA and labels a new project copies"},
		},
	},
	{
		name:     "quality",
		pattern:  regexp.MustCompile(`^/quality$`),
		describe: "the QA profiles page",
		path:     "/quality",
		anchors: []Anchor{
			{"quality.list", "the QA profiles", "sets of QA checks; open one to switch checks on or off"},
			{"quality.new", "the New QA profile button", "create a QA profile"},
		},
	},
	{
		name:     "labels",
		pattern:  regexp.MustCompile(`^/(labels|customers)$`),
		describe: "the labels page (customer, domain and custom project labels)",
		path:     "/labels",
		anchors: []Anchor{
			{"labels.list", "the labels", "customer, domain and custom labels with their values"},
		},
	},
	{
		name:     "integrations",
		pattern:  regexp.MustCompile(`^/integrations$`),
		describe: "the integrations page (CMS connectors)",
		path:     "/integrations",
		anchors: []Anchor{
			{"integrations.list", "the CMS connectors", "content systems that can send content in and receive translations back"},
		},
	},
	{
		name:     "dashboard",
		pattern:  regexp.MustCompile(`^/dashboards/(\d+)$`),
		idParams: []string{"dashboard_id"},
		describe: "dashboard %s (saved metric charts)",
		path:     "/dashboards/%d",
		anchors: []Anchor{
			{"dashboard.charts", "the first chart", "one of the dashboard's charts; open any on its own page"},
			{"dashboard.filters", "the dashboard filters", "time window and filters that apply to every chart"},
			{"dashboard.new_chart", "the New chart button", "add a chart to this dashboard"},
			{"chart_builder.metrics", "the metric list of the New chart dialog", "every measure a chart can show, such as words, AI review results, editing effort, QA issues and AI tokens; the step opens the dialog"},
			{"chart_builder.options", "the chart options of the New chart dialog", "chart type, axis, breakdown, units and the filters on the card; the step opens the dialog"},
		},
	},
	{
		name:     "dashboard_chart",
		pattern:  regexp.MustCompile(`^/dashboards/(\d+)/charts/(\d+)$`),
		idParams: []string{"dashboard_id", "chart_id"},
		describe: "dashboard %s, chart %s on its own page (the view people share by link)",
		path:     "/dashboards/%d/charts/%d",
		anchors: []Anchor{
			{"dashboard_chart.chart", "the chart", "one chart with the definition of its measure"},
			{"dashboard_chart.actions", "the chart actions", "download, share by link and edit the chart"},
		},
	},
	{
		name:     "dashboards",
		pattern:  regexp.MustCompile(`^/dashboards/organization$`),
		describe: "the Organization dashboards tab of the dashboards list (saved metric charts the whole organization sees)",
		path:     "/dashboards/organization",
		anchors: []Anchor{
			{"dashboards.grid", "the first dashboard", "one of the dashboards of saved charts of volume, quality and speed"},
		},
	},
	{
		name:     "my_dashboards",
		pattern:  regexp.MustCompile(`^/dashboards/mine$`),
		describe: "the My dashboards tab of the dashboards list (the user's own saved metric charts, visible only to them)",
		path:     "/dashboards/mine",
	},
	{
		name:     "pinned_dashboards",
		pattern:  regexp.MustCompile(`^/dashboards/pinned$`),
		describe: "the Pinned tab of the dashboards list (the user's own collection of charts pinned from other dashboards, visible only to them)",
		path:     "/dashboards/pinned",
	},
	{
		name:     "pinned_dashboard",
		pattern:  regexp.MustCompile(`^/dashboards/pinned/(\d+)$`),
		idParams: []string{"pinned_dashboard_id"},
		describe: "pinned dashboard %s (the user's own arrangement of charts pinned from other dashboards; the charts themselves live on their original dashboards)",
		path:     "/dashboards/pinned/%d",
	},
	{
		name:     "vendors",
		pattern:  regexp.MustCompile(`^/org/vendors$`),
		describe: "the Vendors tab of the Organization page (linguist members and invites; owners, admins and project managers)",
		path:     "/org/vendors",
		anchors: []Anchor{
			{"vendors.list", "the vendor list", "linguists of the organization and pending invites"},
			{"vendors.invite", "the invite form", "invite an external translator by email"},
		},
	},
	{
		name:     "workflows",
		pattern:  regexp.MustCompile(`^/workflows$`),
		describe: "the workflow library",
		path:     "/workflows",
		anchors: []Anchor{
			{"workflows.new", "the New template button", "build a workflow: an ordered list of library steps"},
			{"workflows.list", "the workflow library", "every reusable workflow and its steps"},
		},
	},
	{
		name:     "workflow_steps",
		pattern:  regexp.MustCompile(`^/workflows/steps$`),
		describe: "the step library (reusable workflow steps)",
		path:     "/workflows/steps",
		anchors: []Anchor{
			{"workflow_steps.new", "the New step button", "create a step: human, hybrid (AI then a person) or AI (AI completes it)"},
			{"workflow_steps.list", "the step library", "reusable steps; hybrid and AI steps name the AI router they run"},
		},
	},
	{
		name:     "org",
		pattern:  regexp.MustCompile(`^/org$`),
		describe: "the Members tab of the Organization page (members, roles and invites; owner/admin only)",
		path:     "/org",
		anchors: []Anchor{
			{"org.tabs", "the tabs of the Organization page", "Members, Settings and Vendors"},
			{"org.members", "the members list", "everyone in the organization and their role"},
			{"org.invite", "the invite form", "invite a member with a role"},
		},
	},
	{
		name:     "org_settings",
		pattern:  regexp.MustCompile(`^/org/settings$`),
		describe: "the Settings tab of the Organization page (registration, notification rules and analysis weights; owner/admin only)",
		path:     "/org/settings",
	},
	{
		name:     "ai_settings",
		pattern:  regexp.MustCompile(`^/ai-settings$`),
		describe: "the AI settings page (AI provider keys and connections, connecting an AI tool, personal access keys)",
		path:     "/ai-settings",
		anchors: []Anchor{
			{"ai_settings.providers", "the AI providers", "the organization's provider keys that AI and LLM engines run with (owner/admin)"},
			{"ai_settings.connect", "connecting an AI tool", "the MCP server address and how to connect Claude or ChatGPT by signing in"},
			{"ai_settings.tokens", "the personal access keys", "the user's own keys for tools that cannot sign in, and for scripts"},
		},
	},
	{
		name:     "settings",
		pattern:  regexp.MustCompile(`^/settings$`),
		describe: "the personal settings page",
		path:     "/settings",
		anchors: []Anchor{
			{"settings.profile", "the profile", "the user's name and email"},
			{"settings.notifications", "the email notifications", "which email notifications the user gets"},
			{"settings.connected_apps", "the connected apps", "Claude, ChatGPT and other MCP clients the user connected, and how to disconnect them"},
		},
	},
	{
		name:      "docs",
		pattern:   regexp.MustCompile(`^/docs(?:/([a-z0-9-]+(?:/[a-z0-9-]+)*))?$`),
		slugParam: "doc_slug",
		describe:  "the docs pages",
		path:      "/docs",
	},
	{
		name:      "api_docs",
		pattern:   regexp.MustCompile(`^/api-docs(?:/([A-Za-z0-9-]+(?:/[A-Za-z0-9-]+)*))?$`),
		slugParam: "doc_slug",
		describe:  "the API docs (guides for calling OpenLoc's API with an access key, and every endpoint with its roles, parameters and responses)",
		path:      "/api-docs",
	},
}

// routesWithoutPage are App.tsx routes deliberately absent from the registry:
// public auth pages, redirects, create-mode aliases of a listed page, and
// internal tools.
var routesWithoutPage = map[string]string{
	"/login":                          "public",
	"/invite/:token":                  "public",
	"/forgot-password":                "public",
	"/reset-password/:token":          "public",
	"/select-org":                     "public",
	"/awaiting-approval":              "public",
	"/shared/charts/:token":           "public (a chart share link)",
	"/tour/:name/:step?":              "entry address of a saved tour link; opens the step's own page",
	"/*":                              "layout wrapper",
	"*":                               "signed out: anything but the help redirects to /login",
	"/engines":                        "redirects to /mt/engines",
	"/vendors":                        "redirects to /org/vendors",
	"/dashboards":                     "redirects to /dashboards/pinned or /dashboards/organization",
	"/ai/engines/new":                 "create mode of ai_engines",
	"/ai/new":                         "create mode of ai_routers",
	"/workflows/steps/new":            "create mode of workflow_steps",
	"/workflows/:workflowId":          "redirects into the workflow editor",
	"/templates/:templateId/workflow": "tab of template",
	"/debug/*":                        "developer gallery",
	"/debug/llm":                      "developer tool",
}
