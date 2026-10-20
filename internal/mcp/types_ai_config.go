package mcp

// AI configuration payloads. Assembly order: a system prompt, then an AI
// engine that binds it to a provider connection, then a step definition that
// runs the engine, then a workflow that orders the steps.

type ListAIPromptsIn struct {
	Purpose string `json:"purpose,omitempty" jsonschema:"optional filter: mt for machine-translation prompts, qe_ape for quality-estimation and post-editing prompts"`
}

// AIPrompt is one system prompt. PromptText is truncated — a caller picking a
// prompt needs to recognise it, not to read the whole instruction.
type AIPrompt struct {
	PromptID int64  `json:"prompt_id"`
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Preview  string `json:"preview"`
	// IsDefault marks a built-in template: usable, but read-only.
	IsDefault bool `json:"is_default"`
	// SegmentContext is the optional per-segment context this prompt sends.
	SegmentContext []string `json:"segment_context"`
	// LanguageAdditions is extra instruction text applied by target language.
	LanguageAdditions []MCPLanguageAddition `json:"language_additions"`
}

// MCPLanguageAddition is extra instruction text that applies only when a job's
// target language matches. A rule naming no target language would match every
// job, which is what the prompt's own text is for, and is refused by validation.
type MCPLanguageAddition struct {
	TargetLanguages []string `json:"target_languages" jsonschema:"language codes this rule applies to on the target side; at least one is required"`
	Text            string   `json:"text" jsonschema:"the extra instruction text, appended when a job matches"`
}

type AIPromptList struct {
	Prompts []AIPrompt `json:"prompts"`
}

type ListAIConnectionsIn struct{}

// AIConnection is a stored provider credential, WITHOUT the credential. The
// token id is what an AI engine row references; the key itself never leaves
// the server.
type AIConnection struct {
	TokenID  int64  `json:"token_id"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
	// Models the provider offers, for the create_ai_engine `model` field. Empty
	// for a provider whose models are not enumerable.
	Models []string `json:"models,omitempty"`
}

type AIConnectionList struct {
	Connections []AIConnection `json:"connections"`
}

type ListAIEnginesIn struct{}

type AIEngine struct {
	AIEngineID int64  `json:"ai_engine_id"`
	Name       string `json:"name"`
	Purpose    string `json:"purpose"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
}

type AIEngineList struct {
	Systems []AIEngine `json:"systems"`
}

// RouterRoute is one target language and the engine the router sends it to.
// Both router families list the same shape; only the engines they choose from
// differ.
type RouterRoute struct {
	TargetLanguage string `json:"target_language"`
	EngineName     string `json:"engine_name"`
}

type ListAIRoutersIn struct{}

// AIRouter is a target-language → QE+APE engine map that a hybrid or ai step
// points at. A language with no route falls back to the default engine; with
// neither, the step runs nothing for that language.
type AIRouter struct {
	AIRouterID        int64         `json:"ai_router_id"`
	Name              string        `json:"name"`
	DefaultEngineName string        `json:"default_engine_name,omitempty"`
	Routes            []RouterRoute `json:"routes,omitempty"`
}

type AIRouterList struct {
	Routers []AIRouter `json:"routers"`
}

type ListMTRoutersIn struct{}

// MTRouter is a target-language → MT engine map that a project points at. The
// engine behind a route is either a classic MT engine or an mt-purpose AI
// engine; the router hides which.
type MTRouter struct {
	MTRouterID        int64         `json:"mt_router_id"`
	Name              string        `json:"name"`
	DefaultEngineName string        `json:"default_engine_name,omitempty"`
	Routes            []RouterRoute `json:"routes,omitempty"`
}

type MTRouterList struct {
	Routers []MTRouter `json:"routers"`
}

type ListWorkflowsIn struct{}

type WorkflowSummary struct {
	WorkflowID int64    `json:"workflow_id"`
	Name       string   `json:"name"`
	Steps      []string `json:"steps"`
}

type WorkflowList struct {
	Workflows []WorkflowSummary `json:"workflows"`
}

type CreateAIPromptIn struct {
	Name string `json:"name" jsonschema:"a short name for the prompt, unique in the organization"`
	Mode string `json:"mode" jsonschema:"what the model returns: mt (a translation), qe (a verdict and score), ape (a corrected translation), or qe_ape (both)"`
	// The shape of the reply is the platform's and is appended at send time; what
	// each field MEANS is instruction text, so it belongs here. A prompt written
	// without it still parses — the field names reach the model from the schema —
	// but the model is told nothing about what to put in them.
	Text              string                `json:"text" jsonschema:"the instruction text, including what each reply field should contain. Do NOT restate the JSON structure itself — the field names, types and the results envelope are appended automatically at send time."`
	SegmentContext    []string              `json:"segment_context,omitempty" jsonschema:"optional per-segment context to send: tm_match (the closest translation memory entry) and/or terms (the glossary entries found in the segment). The source text, and the translation for a reviewing mode, always travel."`
	LanguageAdditions []MCPLanguageAddition `json:"language_additions,omitempty" jsonschema:"extra instruction text that applies only to jobs into some target languages"`
}

// UpdateAIPromptIn replaces a prompt's editable fields. The mode is absent on
// purpose: a prompt's type is fixed once engines point at it.
type UpdateAIPromptIn struct {
	PromptID          int64                 `json:"prompt_id" jsonschema:"the prompt to update, from openloc_list_ai_prompts"`
	Name              string                `json:"name" jsonschema:"a short name for the prompt, unique in the organization"`
	Text              string                `json:"text" jsonschema:"the instruction text, including what each reply field should contain"`
	SegmentContext    []string              `json:"segment_context,omitempty" jsonschema:"optional per-segment context: tm_match and/or terms. Omit to send neither."`
	LanguageAdditions []MCPLanguageAddition `json:"language_additions,omitempty" jsonschema:"the full set of language rules — this replaces the stored ones rather than adding to them"`
}

type UpdateAIPromptResult struct {
	PromptID int64  `json:"prompt_id"`
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Note     string `json:"note"`
}

type CreateAIPromptResult struct {
	PromptID int64  `json:"prompt_id"`
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Note     string `json:"note"`
}

type CreateAIEngineIn struct {
	Name    string `json:"name" jsonschema:"a short name for the engine"`
	Purpose string `json:"purpose" jsonschema:"mt for an engine that translates, qe_ape for one that reviews or post-edits. Must match the system prompt's mode: an mt prompt makes an mt engine, anything else makes a qe_ape engine."`
	TokenID int64  `json:"token_id" jsonschema:"the provider connection to run on, from openloc_list_ai_connections"`
	Model   string `json:"model" jsonschema:"the provider model id, from that connection's models list"`
	// The system prompt is required for an LLM connection and ignored for an
	// extension one, which runs its own quality prediction.
	SystemPromptID  int64  `json:"system_prompt_id,omitempty" jsonschema:"the system prompt, from openloc_list_ai_prompts. Required unless the connection is an extension (provider ext:<id>)."`
	BadOutputAction string `json:"bad_output_action,omitempty" jsonschema:"what to do when the model returns output that does not parse: reject (default, drops the segment to a human), comment (reject and attach the raw output as a comment), or ignore (leave the segment untouched)"`
}

type CreateAIEngineResult struct {
	AIEngineID int64  `json:"ai_engine_id"`
	Name       string `json:"name"`
	Purpose    string `json:"purpose"`
	Note       string `json:"note"`
}

type ListStepDefinitionsIn struct{}

// StepDefinition is one entry in the org's step library — the reusable unit a
// workflow is composed from. Every tenant starts with Translation and Review.
type StepDefinition struct {
	StepDefinitionID int64  `json:"step_definition_id"`
	Name             string `json:"name"`
	StepType         string `json:"step_type"`
	AIRouterID       int64  `json:"ai_router_id,omitempty"`
	AIRouterName     string `json:"ai_router_name,omitempty"`
}

type StepDefinitionList struct {
	Steps []StepDefinition `json:"steps"`
}

// AIRouterRuleIn is one target language → qe_ape engine mapping.
type AIRouterRuleIn struct {
	TargetLanguage string `json:"target_language" jsonschema:"the target language this rule covers, e.g. de or pt-br"`
	AIEngineID     int64  `json:"ai_engine_id" jsonschema:"the qe_ape engine that reviews that language, from openloc_list_ai_engines"`
}

type CreateAIRouterIn struct {
	Name string `json:"name" jsonschema:"a short name for the router, unique in the organization"`
	// A router with neither a default nor a rule would route nothing.
	DefaultAIEngineID int64            `json:"default_ai_engine_id,omitempty" jsonschema:"the qe_ape engine every language falls back to, from openloc_list_ai_engines. Give this, a rule, or both."`
	Rules             []AIRouterRuleIn `json:"rules,omitempty" jsonschema:"optional per-target-language overrides of the default"`
}

type CreateAIRouterResult struct {
	AIRouterID int64  `json:"ai_router_id"`
	Name       string `json:"name"`
	Routes     int    `json:"routes"`
	Note       string `json:"note"`
}

// MTRouterRuleIn is one target language → translation engine mapping. Exactly
// one of the two engine ids is set: a classic MT engine or an LLM one.
type MTRouterRuleIn struct {
	TargetLanguage string `json:"target_language" jsonschema:"the target language this rule covers, e.g. de or pt-br"`
	MTEngineID     int64  `json:"mt_engine_id,omitempty" jsonschema:"a classic MT engine for that language. Give this or ai_engine_id, not both."`
	AIEngineID     int64  `json:"ai_engine_id,omitempty" jsonschema:"an LLM translation engine (purpose mt) for that language, from openloc_list_ai_engines. Give this or mt_engine_id, not both."`
}

type CreateMTRouterIn struct {
	Name string `json:"name" jsonschema:"a short name for the router, unique in the organization"`
	// The default takes one engine of either kind; so does each rule.
	DefaultMTEngineID int64            `json:"default_mt_engine_id,omitempty" jsonschema:"a classic MT engine every language falls back to. Give a default, a rule, or both."`
	DefaultAIEngineID int64            `json:"default_ai_engine_id,omitempty" jsonschema:"an LLM translation engine (purpose mt) every language falls back to, from openloc_list_ai_engines. Mutually exclusive with default_mt_engine_id."`
	Rules             []MTRouterRuleIn `json:"rules,omitempty" jsonschema:"optional per-target-language overrides of the default"`
}

type CreateMTRouterResult struct {
	MTRouterID int64  `json:"mt_router_id"`
	Name       string `json:"name"`
	Routes     int    `json:"routes"`
	Note       string `json:"note"`
}

type CreateStepDefinitionIn struct {
	Name string `json:"name" jsonschema:"the step's name, e.g. Review"`
	// StepType decides whether the step runs its engine. human = no engine;
	// hybrid = a person works the step and the engine reviews it on start;
	// ai = the engine runs and a person confirms.
	StepType string `json:"step_type" jsonschema:"human (no AI), hybrid (a person works it and the AI reviews on start), or ai (the AI runs it)"`
	// A hybrid or ai step requires an AI router; a human step must not have
	// one. MT is not attached here — it runs from the project's MT router.
	AIRouterID int64 `json:"ai_router_id,omitempty" jsonschema:"the AI router this step runs, from openloc_list_ai_routers. The router picks the qe_ape engine for the job's target language. Required for hybrid and ai steps, forbidden for human ones."`
}

type CreateStepDefinitionResult struct {
	StepDefinitionID int64  `json:"step_definition_id"`
	Name             string `json:"name"`
	StepType         string `json:"step_type"`
	Note             string `json:"note"`
}

type CreateWorkflowIn struct {
	Name string `json:"name" jsonschema:"a short name for the workflow"`
	// A workflow is an ORDER over the step library, not a place to define
	// steps: each entry names an existing definition.
	StepDefinitionIDs []int64 `json:"step_definition_ids" jsonschema:"the step definitions to run, in order, from openloc_list_step_definitions. Create any missing one with openloc_create_step_definition first."`
}

type CreateWorkflowResult struct {
	WorkflowID int64  `json:"workflow_id"`
	Name       string `json:"name"`
	Steps      int    `json:"steps"`
	Note       string `json:"note"`
}

type SetProjectMTRouterIn struct {
	ProjectID int64 `json:"project_id" jsonschema:"the project to configure, from openloc_list_projects"`
	// A project has exactly one router, so this replaces whatever it pointed
	// at. The router itself decides which engine each target language gets.
	MTRouterID int64 `json:"mt_router_id" jsonschema:"the MT router to translate this project with, from openloc_list_mt_routers"`
}

type SetProjectMTRouterResult struct {
	ProjectID    int64  `json:"project_id"`
	MTRouterID   int64  `json:"mt_router_id"`
	MTRouterName string `json:"mt_router_name"`
	Note         string `json:"note"`
}
