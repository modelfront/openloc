package mcp

type ProjectList struct {
	Projects []ProjectSummary `json:"projects"`
	Count    int              `json:"count"`
	// NextCursor is set when more projects exist: the list is NOT complete
	// until a page comes back without it.
	NextCursor int64 `json:"next_cursor,omitempty"`
}

type ListProjectsIn struct {
	Status string `json:"status,omitempty" jsonschema:"filter by project status; one of draft, in_progress, completed, on_hold. Omit for all statuses."`
	// Each project already carries customer_name, so filtering client-side works
	// for small orgs; this exists so large orgs don't have to page through
	// everything to find one customer's work.
	CustomerID     int64  `json:"customer_id,omitempty" jsonschema:"only projects for this customer: a value id of the Customer label, from openloc_list_labels. Omit for all customers."`
	SourceLanguage string `json:"source_language,omitempty" jsonschema:"only projects with this source language code, e.g. en"`
	TargetLanguage string `json:"target_language,omitempty" jsonschema:"only projects that have this target language code, e.g. de"`
	Query          string `json:"query,omitempty" jsonschema:"case-insensitive text search in the project name"`
	Cursor         int64  `json:"cursor,omitempty" jsonschema:"pagination cursor: pass the next_cursor from a previous response to continue. Omit for the first page."`
	Limit          int    `json:"limit,omitempty" jsonschema:"max projects per page (default 50, max 200)"`
}

type ListCustomersIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"max customers to return (default 50, max 200)"`
}

type GetProjectIn struct {
	ProjectID int64 `json:"project_id" jsonschema:"the project's numeric id, as returned by openloc_list_projects"`
}

type ListFilesIn struct {
	ProjectID int64 `json:"project_id" jsonschema:"the project whose files to list"`
	Cursor    int64 `json:"cursor,omitempty" jsonschema:"pagination cursor: pass the next_cursor from a previous response to continue. Omit for the first page."`
	Limit     int   `json:"limit,omitempty" jsonschema:"max files to return (default 50, max 200)"`
}

type ListMembersIn struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"pagination cursor: pass the next_cursor from a previous response to continue. Omit for the first page."`
	Limit  int    `json:"limit,omitempty" jsonschema:"max people to return (default 50, max 200)"`
}

type CreateProjectIn struct {
	Name            string   `json:"name" jsonschema:"the project name"`
	SourceLanguage  string   `json:"source_language" jsonschema:"source language code, e.g. en or en-us"`
	TargetLanguages []string `json:"target_languages" jsonschema:"one or more target language codes"`
	TemplateID      int64    `json:"template_id,omitempty" jsonschema:"optional: a template whose settings (TM/MT/terminology/workflow/QA) are applied to the new project. Prefer this over manual configuration when the org has templates — list them via the app's Templates page or ask the user."`
	WorkflowID      int64    `json:"workflow_id,omitempty" jsonschema:"optional: a library workflow whose steps are copied onto the new project (overrides the template's steps). List them via the app's Workflows page or ask the user."`
	CustomerID      int64    `json:"customer_id,omitempty" jsonschema:"optional customer: a value id of the Customer label, from openloc_list_labels"`
	Deadline        string   `json:"deadline,omitempty" jsonschema:"optional ISO 8601 project deadline, e.g. 2026-09-01T17:00:00Z"`
	Description     string   `json:"description,omitempty"`
}

type CreateProjectResult struct {
	ProjectID       int64    `json:"project_id"`
	Name            string   `json:"name"`
	SourceLanguage  string   `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	TemplateApplied bool     `json:"template_applied"`
	WorkflowApplied bool     `json:"workflow_applied"`
	// Note tells the model what to do next (navigate + upload handoff).
	Note string `json:"note"`
}

type CreateCustomerIn struct {
	Name string `json:"name" jsonschema:"the customer's name"`
}

type CreateCustomerResult struct {
	CustomerID int64  `json:"customer_id"`
	Name       string `json:"name"`
}

type ProjectSummary struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	SourceLanguage  string   `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	// Deadline is the project's own due date, empty when none is set. It rides
	// the LIST on purpose so "which projects are overdue?" is one call, not one
	// get_project per project. Enumeration answers must
	// carry what the question needs.
	Deadline  string `json:"deadline"`
	CreatedAt string `json:"created_at"`
	// CustomerName is resolved server-side and sits next to CustomerID on
	// purpose. A bare foreign key is unusable to a model — it cannot join — and
	// makes "list this customer's projects" a series of probing calls.
	// Both come from the project's Customer label pick; empty when it has none.
	CustomerName string `json:"customer_name"`
	CustomerID   *int64 `json:"customer_id"`
	// WorkflowSteps are the project's step names in order, so "which projects
	// have several steps?" is answered from the list. Never null.
	WorkflowSteps []string `json:"workflow_steps"`
	StepCount     int      `json:"step_count"`
	// MTRouterName is the MT router the project translates through; empty
	// when it has none. MTEngines groups the target languages by the engine
	// the router sends them to (its rule for the language, else its default),
	// so "which engines do my projects use?" is answered from the list. Never
	// null. MTNoEngine lists the target languages no engine translates.
	MTRouterName string        `json:"mt_router_name"`
	MTEngines    []MTEngineUse `json:"mt_engines"`
	MTNoEngine   []string      `json:"mt_no_engine,omitempty"`
}

// MTEngineUse is one machine translation engine and the project's target
// languages it translates.
type MTEngineUse struct {
	Engine    string   `json:"engine"`
	Languages []string `json:"languages"`
}

type ProjectDetail struct {
	ProjectSummary
	Description string `json:"description"`
	// The PROJECT's own deadline. It does not cascade to the project's jobs —
	// see JobSummary.Deadline.
	Deadline string `json:"deadline"`
}

type Customer struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type CustomerList struct {
	Customers []Customer `json:"customers"`
	Count     int        `json:"count"`
}

type FileSummary struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Format       string `json:"format"`
	SegmentCount int32  `json:"segment_count"`
	CreatedAt    string `json:"created_at"`
	// JobCount surfaces the gap list_jobs can't: a file with 0 jobs exists but
	// has no target language configured yet, so it is invisible via jobs alone.
	JobCount int `json:"job_count"`
}

type FileList struct {
	Files      []FileSummary `json:"files"`
	Count      int           `json:"count"`
	NextCursor int64         `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}

// Member deliberately carries NO email field.
//
// The only agent use case is "pick an assignee", which needs an id, a name and a
// role. Emails are personal data, and shipping the org's address book into a
// third-party model's context on every call is a cost with no matching benefit.
//
// Caveat worth knowing: `Name` can still CONTAIN an email address, because both
// the seed data and the Google sign-in path fall back to name = email when no
// display name is available (see auth.HandleGoogleCallback). That is the name the
// app shows everywhere, so it is not treated as a leak — but it does mean
// omitting the field is not the same as guaranteeing no email appears.
type Member struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	// LanguagePairs are "source→target" strings (e.g. "en→es"). The arrow
	// separator is deliberate: locale codes contain hyphens ("es-ar"), so
	// "en-es-ar" would be ambiguous where "en→es-ar" is not. Empty slice when
	// the user has no pairs recorded — never omitted.
	LanguagePairs []string `json:"language_pairs"`
	// ActiveJobs counts jobs whose CURRENT workflow step is assigned to this
	// user and in progress — "working right now", not "ever assigned"
	// (vendor availability questions need this on the list).
	ActiveJobs int64 `json:"active_jobs"`
}

type MemberList struct {
	Members    []Member `json:"members"`
	Count      int      `json:"count"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
}
