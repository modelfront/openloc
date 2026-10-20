package mcp

import (
	"context"

	"openloc/internal/ai"
	"openloc/internal/mt"
)

// configTools is the Backend slice for the MT engines, AI and MT routers, step and workflow libraries, templates, QA profiles tools.
type configTools interface {
	// --- MT engines (manager) ---
	ListMTEngines(ctx context.Context, in ListMTEnginesIn) (MTEngineList, error)
	GetMTEngineCatalog(ctx context.Context, in GetMTEngineCatalogIn) (MTEngineCatalog, error)
	ListMTLanguages(ctx context.Context, in ListMTLanguagesIn) (MTLanguageList, error)
	CreateMTEngine(ctx context.Context, in CreateMTEngineIn) (MTEngine, error)
	PreviewUpdateMTEngine(ctx context.Context, in UpdateMTEngineIn) (Preview, error)
	UpdateMTEngine(ctx context.Context, in UpdateMTEngineIn) (MTEngine, error)
	PreviewDeleteMTEngine(ctx context.Context, in DeleteMTEngineIn) (Preview, error)
	DeleteMTEngine(ctx context.Context, in DeleteMTEngineIn) (DeletedConfig, error)

	// --- AI prompts, engines, routers; step and workflow libraries ---
	GetAIRouter(ctx context.Context, in GetAIRouterIn) (AIRouterDetail, error)
	GetMTRouter(ctx context.Context, in GetMTRouterIn) (MTRouterDetail, error)
	GetStepDefinition(ctx context.Context, in GetStepDefinitionIn) (StepDefinitionDetail, error)
	GetWorkflow(ctx context.Context, in GetWorkflowIn) (WorkflowDetail, error)
	PreviewAIPrompt(ctx context.Context, in PreviewAIPromptIn) (AIPromptPreview, error)
	PreviewUpdateAIEngine(ctx context.Context, in UpdateAIEngineIn) (Preview, error)
	UpdateAIEngine(ctx context.Context, in UpdateAIEngineIn) (AIEngine, error)
	PreviewUpdateAIRouter(ctx context.Context, in UpdateAIRouterIn) (Preview, error)
	UpdateAIRouter(ctx context.Context, in UpdateAIRouterIn) (AIRouterDetail, error)
	PreviewUpdateMTRouter(ctx context.Context, in UpdateMTRouterIn) (Preview, error)
	UpdateMTRouter(ctx context.Context, in UpdateMTRouterIn) (MTRouterDetail, error)
	PreviewUpdateStepDefinition(ctx context.Context, in UpdateStepDefinitionIn) (Preview, error)
	UpdateStepDefinition(ctx context.Context, in UpdateStepDefinitionIn) (StepDefinitionDetail, error)
	PreviewUpdateWorkflow(ctx context.Context, in UpdateWorkflowIn) (Preview, error)
	UpdateWorkflow(ctx context.Context, in UpdateWorkflowIn) (WorkflowDetail, error)
	PreviewDeleteAIEngine(ctx context.Context, in DeleteAIEngineIn) (Preview, error)
	DeleteAIEngine(ctx context.Context, in DeleteAIEngineIn) (DeletedConfig, error)
	PreviewDeleteAIRouter(ctx context.Context, in DeleteAIRouterIn) (Preview, error)
	DeleteAIRouter(ctx context.Context, in DeleteAIRouterIn) (DeletedConfig, error)
	PreviewDeleteMTRouter(ctx context.Context, in DeleteMTRouterIn) (Preview, error)
	DeleteMTRouter(ctx context.Context, in DeleteMTRouterIn) (DeletedConfig, error)
	PreviewDeleteStepDefinition(ctx context.Context, in DeleteStepDefinitionIn) (Preview, error)
	DeleteStepDefinition(ctx context.Context, in DeleteStepDefinitionIn) (DeletedConfig, error)
	PreviewDeleteWorkflow(ctx context.Context, in DeleteWorkflowIn) (Preview, error)
	DeleteWorkflow(ctx context.Context, in DeleteWorkflowIn) (DeletedConfig, error)

	// --- Templates (manager) ---
	ListTemplates(ctx context.Context, in ListTemplatesIn) (TemplateList, error)
	GetTemplate(ctx context.Context, in GetTemplateIn) (TemplateDetail, error)
	CreateTemplate(ctx context.Context, in CreateTemplateIn) (TemplateDetail, error)
	DuplicateTemplate(ctx context.Context, in DuplicateTemplateIn) (TemplateDetail, error)
	PreviewUpdateTemplate(ctx context.Context, in UpdateTemplateIn) (Preview, error)
	UpdateTemplate(ctx context.Context, in UpdateTemplateIn) (TemplateDetail, error)

	// --- QA profiles (manager) ---
	ListQAProfiles(ctx context.Context, in ListQAProfilesIn) (QAProfileList, error)
	GetQAProfile(ctx context.Context, in GetQAProfileIn) (QAProfileDetail, error)
	CreateQAProfile(ctx context.Context, in CreateQAProfileIn) (QAProfileDetail, error)
	PreviewUpdateQAProfile(ctx context.Context, in UpdateQAProfileIn) (Preview, error)
	UpdateQAProfile(ctx context.Context, in UpdateQAProfileIn) (QAProfileDetail, error)
	PreviewUpsertQARules(ctx context.Context, in UpsertQARulesIn) (Preview, error)
	UpsertQARules(ctx context.Context, in UpsertQARulesIn) (QAProfileDetail, error)
	PreviewDeleteQAProfile(ctx context.Context, in DeleteQAProfileIn) (Preview, error)
	DeleteQAProfile(ctx context.Context, in DeleteQAProfileIn) (DeletedConfig, error)
}

// DeletedConfig is the result of every configuration delete.
type DeletedConfig struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Note string `json:"note,omitempty"`
}

// --- MT engines ---------------------------------------------------------------

type ListMTEnginesIn struct{}

// MTEngine is a classic machine-translation engine. It never carries the API
// key or the endpoint: a provider engine spends a key from AI settings (only
// its id is shown), and a deployment engine uses what the host supplies.
type MTEngine struct {
	MTEngineID int64  `json:"mt_engine_id"`
	Name       string `json:"name"`
	EngineType string `json:"engine_type"`
	TypeName   string `json:"type_name"`
	Enabled    bool   `json:"enabled"`
	// KeyTokenID is the AI settings key a DeepL or Google engine spends; 0 for
	// a deployment engine, which needs none.
	KeyTokenID int64          `json:"key_token_id,omitempty"`
	Settings   map[string]any `json:"settings"`
	// Unusable says why the engine cannot translate ("" when it can).
	Unusable string `json:"unusable"`
	// ProvisionedBy is "deployment" for an engine the org was given at signup.
	ProvisionedBy string `json:"provisioned_by"`
}

type MTEngineList struct {
	Engines []MTEngine `json:"engines"`
}

type GetMTEngineCatalogIn struct{}

// MTEngineType is one engine type an organization may build, with the options
// it takes.
type MTEngineType struct {
	EngineType string `json:"engine_type"`
	Name       string `json:"name"`
	// NeedsKey types (DeepL, Google) are created in AI settings, not over MCP.
	NeedsKey bool          `json:"needs_key"`
	Params   []ai.ParamDef `json:"params"`
}

type MTEngineCatalog struct {
	Types []MTEngineType `json:"types"`
}

type ListMTLanguagesIn struct {
	MTEngineID int64 `json:"mt_engine_id" jsonschema:"the engine to ask, from openloc_list_mt_engines"`
}

// MTLanguageList is the engine's catalog: the languages it reads (sources)
// and writes (targets), the same shape as GET /api/mt-engines/{id}/languages.
type MTLanguageList struct {
	MTEngineID int64 `json:"mt_engine_id"`
	mt.Catalog
}

type CreateMTEngineIn struct {
	Name       string         `json:"name" jsonschema:"a short name for the engine"`
	EngineType string         `json:"engine_type" jsonschema:"an engine type from openloc_get_mt_engine_catalog whose needs_key is false. Engines that need a provider key (DeepL, Google) are created in AI settings."`
	Enabled    *bool          `json:"enabled,omitempty" jsonschema:"whether routers may use it; default true"`
	Settings   map[string]any `json:"settings,omitempty" jsonschema:"provider options keyed by the params listed in the catalog for this type"`
}

type UpdateMTEngineIn struct {
	MTEngineID int64          `json:"mt_engine_id" jsonschema:"the engine to change, from openloc_list_mt_engines"`
	Name       *string        `json:"name,omitempty" jsonschema:"a new name; omit to keep"`
	Enabled    *bool          `json:"enabled,omitempty" jsonschema:"switch the engine on or off; omit to keep. A switched-off engine translates nothing for any router that uses it."`
	Settings   map[string]any `json:"settings,omitempty" jsonschema:"the full set of provider options; replaces the stored ones. Omit to keep."`
	Confirm
}

type DeleteMTEngineIn struct {
	MTEngineID int64 `json:"mt_engine_id" jsonschema:"the engine to delete, from openloc_list_mt_engines"`
	Confirm
}

// --- AI and MT routers, engines, prompts --------------------------------------

type GetAIRouterIn struct {
	AIRouterID int64 `json:"ai_router_id" jsonschema:"the router, from openloc_list_ai_routers"`
}

// RouterRuleDetail is one route with the engine's id and name, so a caller can
// send the rules back to an update tool.
type RouterRuleDetail struct {
	TargetLanguage string `json:"target_language"`
	MTEngineID     int64  `json:"mt_engine_id,omitempty"`
	AIEngineID     int64  `json:"ai_engine_id,omitempty"`
	EngineName     string `json:"engine_name"`
}

type AIRouterDetail struct {
	AIRouterID        int64              `json:"ai_router_id"`
	Name              string             `json:"name"`
	DefaultAIEngineID int64              `json:"default_ai_engine_id,omitempty"`
	DefaultEngineName string             `json:"default_engine_name,omitempty"`
	Rules             []RouterRuleDetail `json:"rules"`
	// StepsUsing names the library steps that run this router.
	StepsUsing []string `json:"steps_using"`
}

type GetMTRouterIn struct {
	MTRouterID int64 `json:"mt_router_id" jsonschema:"the router, from openloc_list_mt_routers"`
}

type MTRouterDetail struct {
	MTRouterID        int64              `json:"mt_router_id"`
	Name              string             `json:"name"`
	DefaultMTEngineID int64              `json:"default_mt_engine_id,omitempty"`
	DefaultAIEngineID int64              `json:"default_ai_engine_id,omitempty"`
	DefaultEngineName string             `json:"default_engine_name,omitempty"`
	Rules             []RouterRuleDetail `json:"rules"`
	// UsedBy counts the projects and templates that point at this router.
	UsedBy int64 `json:"used_by"`
}

type GetStepDefinitionIn struct {
	StepDefinitionID int64 `json:"step_definition_id" jsonschema:"the step, from openloc_list_step_definitions"`
}

type StepDefinitionDetail struct {
	StepDefinitionID int64  `json:"step_definition_id"`
	Name             string `json:"name"`
	StepType         string `json:"step_type"`
	AIRouterID       int64  `json:"ai_router_id,omitempty"`
	// WorkflowsUsing names the library workflows that compose this step.
	WorkflowsUsing []string `json:"workflows_using"`
}

type GetWorkflowIn struct {
	WorkflowID int64 `json:"workflow_id" jsonschema:"the workflow, from openloc_list_workflows"`
}

type WorkflowStepDetail struct {
	StepDefinitionID  int64  `json:"step_definition_id"`
	Name              string `json:"name"`
	StepType          string `json:"step_type"`
	DefaultAssigneeID int64  `json:"default_assignee_id,omitempty"`
	AssigneeName      string `json:"assignee_name,omitempty"`
	// DefinitionDeleted marks a step whose definition was removed from the
	// library; an update must replace it.
	DefinitionDeleted bool `json:"definition_deleted,omitempty"`
}

type WorkflowDetail struct {
	WorkflowID int64                `json:"workflow_id"`
	Name       string               `json:"name"`
	Steps      []WorkflowStepDetail `json:"steps"`
	// Projects and templates whose steps were copied from this workflow. They
	// keep their copy when the workflow changes.
	CopiedByProjects  int64 `json:"copied_by_projects"`
	CopiedByTemplates int64 `json:"copied_by_templates"`
}

type PreviewAIPromptIn struct {
	PromptID       int64    `json:"prompt_id,omitempty" jsonschema:"preview a stored prompt, from openloc_list_ai_prompts. Or omit it and give mode and text for a draft."`
	Mode           string   `json:"mode,omitempty" jsonschema:"for a draft: mt, qe, ape or qe_ape"`
	Text           string   `json:"text,omitempty" jsonschema:"for a draft: the instruction text"`
	SegmentContext []string `json:"segment_context,omitempty" jsonschema:"for a draft: tm_match and/or terms"`
	SourceLanguage string   `json:"source_language,omitempty" jsonschema:"optional sample source language code; default a placeholder"`
	TargetLanguage string   `json:"target_language,omitempty" jsonschema:"optional sample target language code; default a placeholder"`
	Model          string   `json:"model,omitempty" jsonschema:"optional model id, from openloc_list_ai_connections; decides whether the reply shape is enforced by the provider"`
}

// AIPromptPreview is the system message exactly as an engine would send it.
type AIPromptPreview struct {
	Mode string `json:"mode"`
	// Instruction is the organization's own text; Appended is what the
	// platform adds; SystemPrompt is both, as sent.
	Instruction    string         `json:"instruction"`
	Appended       string         `json:"appended"`
	SystemPrompt   string         `json:"system_prompt"`
	ContextPreview string         `json:"context_preview"`
	SchemaEnforced bool           `json:"schema_enforced"`
	OutputSchema   map[string]any `json:"output_schema,omitempty"`
}

type UpdateAIEngineIn struct {
	AIEngineID      int64   `json:"ai_engine_id" jsonschema:"the engine to change, from openloc_list_ai_engines"`
	Name            *string `json:"name,omitempty" jsonschema:"a new name; omit to keep"`
	Enabled         *bool   `json:"enabled,omitempty" jsonschema:"switch the engine on or off; omit to keep"`
	TokenID         int64   `json:"token_id,omitempty" jsonschema:"another provider connection, from openloc_list_ai_connections; omit to keep"`
	Model           string  `json:"model,omitempty" jsonschema:"another model id of that connection; omit to keep"`
	SystemPromptID  int64   `json:"system_prompt_id,omitempty" jsonschema:"another system prompt of a matching mode; omit to keep"`
	BadOutputAction string  `json:"bad_output_action,omitempty" jsonschema:"reject, comment or ignore; omit to keep"`
	Confirm
}

type UpdateAIRouterIn struct {
	AIRouterID int64 `json:"ai_router_id" jsonschema:"the router to change, from openloc_list_ai_routers"`
	CreateAIRouterIn
	Confirm
}

type UpdateMTRouterIn struct {
	MTRouterID int64 `json:"mt_router_id" jsonschema:"the router to change, from openloc_list_mt_routers"`
	CreateMTRouterIn
	Confirm
}

type UpdateStepDefinitionIn struct {
	StepDefinitionID int64 `json:"step_definition_id" jsonschema:"the step to change, from openloc_list_step_definitions"`
	CreateStepDefinitionIn
	Confirm
}

// LibraryWorkflowStepIn is one position in a workflow.
type LibraryWorkflowStepIn struct {
	StepDefinitionID  int64 `json:"step_definition_id" jsonschema:"a step from openloc_list_step_definitions"`
	DefaultAssigneeID int64 `json:"default_assignee_id,omitempty" jsonschema:"optional member who gets this step by default, from openloc_list_members"`
}

type UpdateWorkflowIn struct {
	WorkflowID int64                   `json:"workflow_id" jsonschema:"the workflow to change, from openloc_list_workflows"`
	Name       string                  `json:"name" jsonschema:"the workflow's name (send the current one to keep it)"`
	Steps      []LibraryWorkflowStepIn `json:"steps" jsonschema:"the full ordered step list; replaces the stored one. Read it with openloc_get_workflow first and keep each default_assignee_id you do not mean to drop."`
	Confirm
}

type DeleteAIEngineIn struct {
	AIEngineID int64 `json:"ai_engine_id" jsonschema:"the engine to delete, from openloc_list_ai_engines"`
	Confirm
}

type DeleteAIRouterIn struct {
	AIRouterID int64 `json:"ai_router_id" jsonschema:"the router to delete, from openloc_list_ai_routers"`
	Confirm
}

type DeleteMTRouterIn struct {
	MTRouterID int64 `json:"mt_router_id" jsonschema:"the router to delete, from openloc_list_mt_routers"`
	Confirm
}

type DeleteStepDefinitionIn struct {
	StepDefinitionID int64 `json:"step_definition_id" jsonschema:"the step to delete, from openloc_list_step_definitions"`
	Confirm
}

type DeleteWorkflowIn struct {
	WorkflowID int64 `json:"workflow_id" jsonschema:"the workflow to delete, from openloc_list_workflows"`
	Confirm
}
