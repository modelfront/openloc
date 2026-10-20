// Package mcp exposes OpenLoc as a Model Context Protocol server.
//
// Served over Streamable HTTP at /mcp, authenticated with personal access tokens
// (auth.PATPrefix).
//
// The tool surface per role is listed in testdata/tools_<tier>.golden. Every
// write is gated by requireWrite, so a read-only token cannot reach one.
// Deletes and edits to shared configuration are guarded: a preview first,
// then a confirm token (guard.go). confirm_translation
// marks the segment confirmed_via='assistant', which keeps it out of
// translation memory until a person confirms it.
//
// Architecture note — the Backend seam. Tool definitions depend on the Backend
// interface, never on a concrete data source. Today the only implementation is
// directBackend (in-process service/query calls). A future stdio binary running
// on a user's laptop cannot reach Postgres and will need an HTTP implementation
// instead; keeping that seam means the swap is a constructor argument rather
// than a rewrite of every tool body. Tool definitions also deliberately live
// here rather than under cmd/, because code under cmd/ cannot be imported.
//
// The three documentation tools are the only reads that are
// not tenant-scoped — docs pages are global content — and they are filtered by
// the caller's role the same way GET /api/docs is.
package mcp

import (
	"context"
	"errors"
	"fmt"

	"openloc/internal/metrics"
	"openloc/internal/productdocs"
)

// Backend is everything the MCP tools need from OpenLoc.
//
// Every method is implicitly scoped to one tenant and one user: implementations
// read them from the auth claims in ctx. No method takes a tenantID parameter,
// and no tool argument carries one, so there is no path by which a model can
// name a tenant it doesn't already hold a token for.
type Backend interface {
	projectOpsTools
	jobOpsTools
	resourceTools
	configTools
	insightTools

	ListProjects(ctx context.Context, in ListProjectsIn) (ProjectList, error)
	GetProject(ctx context.Context, in GetProjectIn) (ProjectDetail, error)
	ListJobs(ctx context.Context, in ListJobsIn) (JobList, error)
	GetJob(ctx context.Context, in GetJobIn) (JobDetail, error)
	GetJobLeverage(ctx context.Context, in GetJobIn) (Leverage, error)
	ListSegments(ctx context.Context, in ListSegmentsIn) (SegmentPage, error)
	SearchTM(ctx context.Context, in SearchTMIn) (TMHitList, error)
	LookupTerm(ctx context.Context, in LookupTermIn) (TermHitList, error)
	LookupConcept(ctx context.Context, in LookupConceptIn) (ConceptMatchList, error)
	GetQAReport(ctx context.Context, in GetJobIn) (QAReport, error)
	ListMyTasks(ctx context.Context, in ListMyTasksIn) (TaskList, error)
	ListFiles(ctx context.Context, in ListFilesIn) (FileList, error)
	ListMembers(ctx context.Context, in ListMembersIn) (MemberList, error)
	ListCustomers(ctx context.Context, in ListCustomersIn) (CustomerList, error)
	QueryMetrics(ctx context.Context, in QueryMetricsIn) (metrics.Result, error)

	// --- Writes ---
	//
	// Every one of these is gated by requireWrite in server.go, so a read-only
	// token cannot reach them. Ordered by blast radius: comments and locks touch
	// no translation content, and SaveDraft is
	// the one that writes translation text — deliberately WITHOUT approving it, so
	// a human still confirms before anything reaches translation memory.
	AddComment(ctx context.Context, in AddCommentIn) (CommentResult, error)
	LockSegment(ctx context.Context, in LockSegmentIn) (LockResult, error)
	SaveDraft(ctx context.Context, in SaveDraftIn) (SegmentWriteResult, error)
	// QA writes. RunQA recomputes a job's findings (preserving per-finding
	// ignores whose message is unchanged); IgnoreQAFinding flips the ignore flag
	// on one stored finding. Both run the same qa.Service code as the REST
	// handlers — no drifting copy.
	RunQA(ctx context.Context, in GetJobIn) (RunQAResult, error)
	IgnoreQAFinding(ctx context.Context, in IgnoreQAFindingIn) (QAFindingList, error)

	// Creation tools. All additive: worst case is an empty shell
	// the user deletes. create_project runs the SAME service as the REST
	// handler (project.Create / project.CreateWithTemplate) — no drifting copy.
	CreateProject(ctx context.Context, in CreateProjectIn) (CreateProjectResult, error)
	CreateCustomer(ctx context.Context, in CreateCustomerIn) (CreateCustomerResult, error)
	CreateTermBase(ctx context.Context, in CreateTermBaseIn) (CreateTermBaseResult, error)
	AddTerm(ctx context.Context, in AddTermIn) (AddTermResult, error)
	// Concept-native writes: create_term_base holds concepts (one meaning), each
	// with terms across languages. AddTerm is the bilingual shortcut; these two
	// are the full model — a multilingual concept, and a term appended to one.
	CreateConcept(ctx context.Context, in CreateConceptIn) (CreateConceptResult, error)
	AddTermToConcept(ctx context.Context, in AddTermToConceptIn) (AddTermToConceptResult, error)
	UpdateConcept(ctx context.Context, in UpdateConceptIn) (UpdateConceptResult, error)
	UpdateTerm(ctx context.Context, in UpdateTermIn) (UpdateTermResult, error)

	// AI configuration: the pieces a QE+APE or AI-as-MT engine is assembled
	// from, plus the workflow that runs it. Each delegates to the same service
	// the REST handler calls (aiprompt.CreateTx, aiengine.Create,
	// workflowlib.CreateValidated), so validation cannot drift.
	//
	// Creating the provider CONNECTION is deliberately absent: it takes an API
	// key, and a credential does not belong in a tool call. ListAIConnections
	// surfaces the connections that already exist so an engine can reference one.
	ListAIPrompts(ctx context.Context, in ListAIPromptsIn) (AIPromptList, error)
	ListAIConnections(ctx context.Context, in ListAIConnectionsIn) (AIConnectionList, error)
	ListAIEngines(ctx context.Context, in ListAIEnginesIn) (AIEngineList, error)
	// Routers are the indirection between an engine and the thing that runs it:
	// an AI router is what a workflow step points at, an MT router is what a
	// project points at. Both map a target language to an engine. Read-only
	// here — a router is composed in the UI.
	ListAIRouters(ctx context.Context, in ListAIRoutersIn) (AIRouterList, error)
	ListMTRouters(ctx context.Context, in ListMTRoutersIn) (MTRouterList, error)
	ListStepDefinitions(ctx context.Context, in ListStepDefinitionsIn) (StepDefinitionList, error)
	ListWorkflows(ctx context.Context, in ListWorkflowsIn) (WorkflowList, error)
	CreateAIPrompt(ctx context.Context, in CreateAIPromptIn) (CreateAIPromptResult, error)
	UpdateAIPrompt(ctx context.Context, in UpdateAIPromptIn) (UpdateAIPromptResult, error)
	CreateAIEngine(ctx context.Context, in CreateAIEngineIn) (CreateAIEngineResult, error)
	// The two router creates close the loop an engine opens: an engine is only
	// reachable through a router, and a router is what a step (AI) or a project
	// (MT) actually points at.
	CreateAIRouter(ctx context.Context, in CreateAIRouterIn) (CreateAIRouterResult, error)
	CreateMTRouter(ctx context.Context, in CreateMTRouterIn) (CreateMTRouterResult, error)
	CreateStepDefinition(ctx context.Context, in CreateStepDefinitionIn) (CreateStepDefinitionResult, error)
	CreateWorkflow(ctx context.Context, in CreateWorkflowIn) (CreateWorkflowResult, error)
	// SetProjectMTRouter points a project at the MT router that translates it.
	// Separate from CreateAIEngine because creating an engine and choosing
	// where it runs are different decisions — an engine and a router are
	// org-wide, the pointer is per project.
	SetProjectMTRouter(ctx context.Context, in SetProjectMTRouterIn) (SetProjectMTRouterResult, error)

	// ListProjectWorkflowSteps + SetWorkflowStepDeadlines are the deadline
	// pair: a project's jobs are file × target language, so a step can be due
	// on a different date per language. The read exists because the write
	// needs step ids, which nothing else exposes.
	ListProjectWorkflowSteps(ctx context.Context, in ListProjectWorkflowStepsIn) (ProjectWorkflowStepList, error)
	SetWorkflowStepDeadlines(ctx context.Context, in SetWorkflowStepDeadlinesIn) (SetWorkflowStepDeadlinesResult, error)

	// CreateChart persists a metrics definition on an
	// org-wide dashboard (found or created by name). Additive like the other
	// creation tools: worst case is a chart the user deletes.
	CreateChart(ctx context.Context, in CreateChartIn) (CreateChartResult, error)

	// SetDashboardFilters configures the dashboard-level control row.
	// Owner/admin: the config decides what every member of
	// the org sees on that dashboard.
	SetDashboardFilters(ctx context.Context, in SetDashboardFiltersIn) (SetDashboardFiltersResult, error)

	// SetChartFilters configures ONE chart's own control row, on top of the
	// dashboard's. For a control that is meaningful on one card and dead on the
	// others, or to let one chart use a different interval from its neighbours.
	SetChartFilters(ctx context.Context, in SetChartFiltersIn) (SetChartFiltersResult, error)

	// --- Theme / appearance ---
	//
	// USER-scoped, not tenant-scoped — the only tools here that are. That is not
	// an oversight: user_appearance is keyed by user_id by design so a
	// chosen palette follows the person across every org they belong to. The
	// caller's id comes from the token's claims, so a caller can only ever read
	// and write their own theme; there is no id parameter to tamper with.
	//
	// Not manager-gated either: restyling your own UI is a personal preference,
	// not an org operation. All three mutations carry requireWrite, so a
	// read-only token can't touch them and the chat asks before applying.
	GetAppearance(ctx context.Context, in GetAppearanceIn) (AppearanceResult, error)
	UpsertCustomPalette(ctx context.Context, in UpsertCustomPaletteIn) (AppearanceResult, error)
	SetActivePalette(ctx context.Context, in SetActivePaletteIn) (AppearanceResult, error)
	PreviewDeleteCustomPalette(ctx context.Context, in DeleteCustomPaletteIn) (Preview, error)
	DeleteCustomPalette(ctx context.Context, in DeleteCustomPaletteIn) (AppearanceResult, error)

	// --- Issue reports ---
	//
	// Any role, and allowed in read-only mode: a report to the platform
	// operators changes none of the org's data. Guarded, so the user confirms.
	PreviewReportIssue(ctx context.Context, in ReportIssueIn) (Preview, error)
	ReportIssue(ctx context.Context, in ReportIssueIn) (ReportIssueResult, error)

	// --- User documentation ---
	//
	// Read-only. Global product content, so NOT tenant-scoped — the only such
	// reads here. Role-filtered from the token's claims exactly like
	// GET /api/docs, so an agent answers "how do I…" with the pages its user
	// could open themselves.
	ListDocs(ctx context.Context, in ListDocsIn) (DocTreeResult, error)
	SearchDocs(ctx context.Context, in SearchDocsIn) (DocSearchResult, error)
	GetDocPage(ctx context.Context, in GetDocPageIn) (productdocs.DocPage, error)
}

// errConflict signals a concurrent-modification clash — the caller's
// expected_updated_at didn't match the row's current value.
//
// Distinct from errNotFound because the model should react differently: re-read
// the segment and reconsider, rather than conclude the segment doesn't exist.
var errConflict = errors.New("conflict")

// errDenied is a per-RESOURCE authorization refusal, decided in the backend
// where the row is known — distinct from the tool-level role gate in server.go,
// which can only see the caller's role. Configuring a shared dashboard is the
// first of these: the tool is manager-tier, but the resource needs owner/admin.
var errDenied = errors.New("denied")

// errInvalid signals a caller mistake the model can fix by changing arguments
// (empty text, no QA profile configured, bad enum). Its message IS surfaced, so
// it must never carry internal detail — construct it with a sentence written for
// the model.
type errInvalid struct{ msg string }

func (e errInvalid) Error() string { return e.msg }

func invalidf(format string, args ...any) error {
	return errInvalid{msg: fmt.Sprintf(format, args...)}
}

// errLimited is a rate-limit refusal: the call was fine but comes too soon.
// Like errInvalid, its message is written for the model and surfaced verbatim;
// it says when to try again.
type errLimited struct{ msg string }

func (e errLimited) Error() string { return e.msg }

// Every tool's output type MUST be a struct, never a bare slice.
//
// The SDK infers a tool's outputSchema from its Out type, and a slice infers
// `type: "array"`. The protocol version the stable SDK negotiates requires
// outputSchema to be an OBJECT, so an array-returning tool is rejected by real
// clients with `expected "object" (at tools.N.outputSchema.type)` — and the
// whole tools/list call fails, taking every other tool down with it.
//
// The SDK itself permits slices (SEP-2106 loosened this for 2026-07-28) and the
// SDK's own test client doesn't enforce the constraint, so this cannot be caught
// by round-tripping through the Go client — see the outputSchema assertion in
// mcp_integration_test.go, which checks the emitted schema directly.
//
// Wrapping is also the better long-term shape: a struct leaves room to add
// paging or totals later without changing a tool's result type.

// --- Pagination -------------------------------------------------------------
//
// Every list tool is paginated from day one. An unbounded result is the fastest
// way to destroy a model's context window: a single job can hold tens of
// thousands of segments, and one uncapped call ends the conversation.
const (
	// DefaultPageSize is what a tool returns when the caller doesn't ask.
	DefaultPageSize = 50
	// MaxPageSize is the hard server-side ceiling, applied regardless of what
	// the model requests. Clamped, not rejected — a model asking for 5,000 rows
	// should get 200 and a hint, not an error it has to reason about.
	MaxPageSize = 200
)

// clampLimit applies the page-size policy.
func clampLimit(requested int) int {
	if requested <= 0 {
		return DefaultPageSize
	}
	if requested > MaxPageSize {
		return MaxPageSize
	}
	return requested
}

// --- Tool payloads (types_<domain>.go) ---------------------------------------
//
// Input structs double as the tools' JSON Schemas: the SDK infers the schema
// from the Go type and reads property descriptions from `jsonschema` tags. The
// tag text is prompt surface, written for a reader who has never seen OpenLoc.
//
// Outputs are compact by design. A tool result is read by a model paying per
// token, so it carries the fields a model can act on and nothing else.
