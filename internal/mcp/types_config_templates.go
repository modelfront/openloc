package mcp

// Template and QA profile payloads for the config tools (types_config.go).

type ListTemplatesIn struct {
	Query  string `json:"query,omitempty" jsonschema:"optional text the template name must contain"`
	Cursor int64  `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page; omit for the first page"`
	Limit  int    `json:"limit,omitempty" jsonschema:"page size, default 50, max 200"`
}

// TemplateSummary is what a project made from the template starts with.
type TemplateSummary struct {
	TemplateID      int64    `json:"template_id"`
	Name            string   `json:"name"`
	ProjectName     string   `json:"project_name,omitempty"`
	SourceLanguage  string   `json:"source_language,omitempty"`
	TargetLanguages []string `json:"target_languages"`
	CustomerID      int64    `json:"customer_id,omitempty"`
	Domain          string   `json:"domain,omitempty"`
}

type TemplateList struct {
	Templates  []TemplateSummary `json:"templates"`
	NextCursor int64             `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

type GetTemplateIn struct {
	TemplateID int64 `json:"template_id" jsonschema:"the template, from openloc_list_templates"`
}

// TemplateDetail is a template with the settings a project copies from it.
type TemplateDetail struct {
	TemplateSummary
	FuzzyThreshold        float32  `json:"fuzzy_threshold"`
	MtOnUpload            bool     `json:"mt_on_upload"`
	TmOnUpload            bool     `json:"tm_on_upload"`
	AutoComplete          bool     `json:"auto_complete"`
	InlineHTML            bool     `json:"inline_html"`
	SublanguagePenaltyPct int32    `json:"sublanguage_penalty_pct"`
	NearExactPenaltyPct   int32    `json:"near_exact_penalty_pct"`
	QAProfileID           int64    `json:"qa_profile_id,omitempty"`
	MTRouterID            int64    `json:"mt_router_id,omitempty"`
	MTRouterName          string   `json:"mt_router_name,omitempty"`
	WorkflowID            int64    `json:"workflow_id,omitempty"`
	WorkflowName          string   `json:"workflow_name,omitempty"`
	TMProfileID           int64    `json:"tm_profile_id,omitempty"`
	TMProfileName         string   `json:"tm_profile_name,omitempty"`
	WorkflowSteps         []string `json:"workflow_steps"`
	// ProjectsCreated counts projects made from this template. They keep their
	// own copy of its settings.
	ProjectsCreated int64 `json:"projects_created"`
}

type CreateTemplateIn struct {
	Name            string   `json:"name" jsonschema:"the template's name"`
	ProjectName     string   `json:"project_name,omitempty" jsonschema:"the name a new project starts with; default the template name"`
	SourceLanguage  string   `json:"source_language,omitempty" jsonschema:"optional source language code"`
	TargetLanguages []string `json:"target_languages,omitempty" jsonschema:"optional target language codes"`
	CustomerID      int64    `json:"customer_id,omitempty" jsonschema:"optional customer, from openloc_list_customers"`
	Domain          string   `json:"domain,omitempty" jsonschema:"optional subject domain"`
	WorkflowID      int64    `json:"workflow_id,omitempty" jsonschema:"library workflow to copy, from openloc_list_workflows; default Translation then Review"`
	MTRouterID      int64    `json:"mt_router_id,omitempty" jsonschema:"MT router, from openloc_list_mt_routers"`
	QAProfileID     int64    `json:"qa_profile_id,omitempty" jsonschema:"QA profile, from openloc_list_qa_profiles"`
	TMProfileID     int64    `json:"tm_profile_id,omitempty" jsonschema:"TM profile to follow"`
	MtOnUpload      *bool    `json:"mt_on_upload,omitempty" jsonschema:"machine-translate files on upload; default true"`
	TmOnUpload      *bool    `json:"tm_on_upload,omitempty" jsonschema:"fill translation memory matches on upload; default true"`
	AutoComplete    *bool    `json:"auto_complete,omitempty" jsonschema:"complete a job's step when every segment is confirmed; default true"`
	InlineHTML      *bool    `json:"inline_html,omitempty" jsonschema:"strings in JSON, PO, TXT and Excel files are HTML: their tags become protected tags; default true"`
	FuzzyThreshold  *float32 `json:"fuzzy_threshold,omitempty" jsonschema:"lowest TM match score used, 0 to 1; default 0.75"`
}

type DuplicateTemplateIn struct {
	TemplateID int64 `json:"template_id" jsonschema:"the template to copy, from openloc_list_templates"`
}

// UpdateTemplateIn is a partial edit: an omitted field keeps its value. For
// the id fields, 0 clears.
type UpdateTemplateIn struct {
	TemplateID            int64    `json:"template_id" jsonschema:"the template to change, from openloc_list_templates"`
	Name                  *string  `json:"name,omitempty" jsonschema:"a new name"`
	ProjectName           *string  `json:"project_name,omitempty" jsonschema:"the name a new project starts with; empty clears"`
	SourceLanguage        *string  `json:"source_language,omitempty" jsonschema:"source language code; empty clears"`
	TargetLanguages       []string `json:"target_languages,omitempty" jsonschema:"the full list of target language codes; replaces the stored one"`
	CustomerID            *int64   `json:"customer_id,omitempty" jsonschema:"customer from openloc_list_customers; 0 clears"`
	Domain                *string  `json:"domain,omitempty" jsonschema:"subject domain; empty clears"`
	MTRouterID            *int64   `json:"mt_router_id,omitempty" jsonschema:"MT router from openloc_list_mt_routers; 0 clears"`
	QAProfileID           *int64   `json:"qa_profile_id,omitempty" jsonschema:"QA profile from openloc_list_qa_profiles; 0 clears"`
	TMProfileID           *int64   `json:"tm_profile_id,omitempty" jsonschema:"TM profile to follow from now on"`
	MtOnUpload            *bool    `json:"mt_on_upload,omitempty"`
	TmOnUpload            *bool    `json:"tm_on_upload,omitempty"`
	AutoComplete          *bool    `json:"auto_complete,omitempty"`
	InlineHTML            *bool    `json:"inline_html,omitempty"`
	FuzzyThreshold        *float32 `json:"fuzzy_threshold,omitempty" jsonschema:"0 to 1; refused while the template follows a TM profile"`
	SublanguagePenaltyPct *int32   `json:"sublanguage_penalty_pct,omitempty" jsonschema:"refused while the template follows a TM profile"`
	NearExactPenaltyPct   *int32   `json:"near_exact_penalty_pct,omitempty" jsonschema:"refused while the template follows a TM profile"`
	Confirm
}

// --- QA profiles --------------------------------------------------------------

type ListQAProfilesIn struct{}

type QAProfileSummary struct {
	QAProfileID int64  `json:"qa_profile_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type QAProfileList struct {
	Profiles []QAProfileSummary `json:"profiles"`
}

type GetQAProfileIn struct {
	QAProfileID int64 `json:"qa_profile_id" jsonschema:"the profile, from openloc_list_qa_profiles"`
}

type QARule struct {
	CheckType string         `json:"check_type"`
	Enabled   bool           `json:"enabled"`
	Severity  string         `json:"severity"`
	Config    map[string]any `json:"config"`
}

type QAProfileDetail struct {
	QAProfileSummary
	Rules []QARule `json:"rules"`
	// UsedBy counts the projects and templates that run this profile.
	UsedBy int64 `json:"used_by"`
}

// QARuleIn is one check of a profile.
type QARuleIn struct {
	CheckType string         `json:"check_type" jsonschema:"one of: consistency, empty_target, forbidden_terms, number_consistency, punctuation, tag_checks, target_identical_to_source, terminology"`
	Enabled   *bool          `json:"enabled,omitempty" jsonschema:"default true"`
	Severity  string         `json:"severity,omitempty" jsonschema:"error (default), warning or info"`
	Config    map[string]any `json:"config,omitempty" jsonschema:"optional check options, as returned by openloc_get_qa_profile"`
}

type CreateQAProfileIn struct {
	Name        string     `json:"name" jsonschema:"the profile's name"`
	Description string     `json:"description,omitempty"`
	Rules       []QARuleIn `json:"rules,omitempty" jsonschema:"optional first rules; a profile with no enabled rule reports nothing"`
}

type UpdateQAProfileIn struct {
	QAProfileID int64  `json:"qa_profile_id" jsonschema:"the profile to change, from openloc_list_qa_profiles"`
	Name        string `json:"name" jsonschema:"the profile's name (send the current one to keep it)"`
	Description string `json:"description,omitempty" jsonschema:"replaces the stored description"`
	Confirm
}

type UpsertQARulesIn struct {
	QAProfileID int64      `json:"qa_profile_id" jsonschema:"the profile, from openloc_list_qa_profiles"`
	Rules       []QARuleIn `json:"rules" jsonschema:"rules to create or replace, one per check type; rules not listed stay as they are"`
	Confirm
}

type DeleteQAProfileIn struct {
	QAProfileID int64 `json:"qa_profile_id" jsonschema:"the profile to delete, from openloc_list_qa_profiles"`
	Confirm
}
