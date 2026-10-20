package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/signedtoken"
	"openloc/internal/taskedit"
)

// Deps is what the MCP server needs from the application.
type Deps struct {
	// Backend serves the tools. Required.
	Backend Backend
	// Audit receives one row per tool call. Optional — nil disables the audit
	// trail, which is appropriate only in tests.
	Audit auditWriter
	// Version is reported to clients as the server version.
	Version string
	// ConfirmKey signs guarded-tool confirm tokens (guard.go). Must be the
	// same on every replica; build it with DeriveConfirmKey. Empty means a
	// random per-process key, which is fine for tests only.
	ConfirmKey []byte
	// StepUpChallenge is the WWW-Authenticate value returned in a tool result
	// when a read-scoped OAuth grant calls a write tool. Empty sends none.
	StepUpChallenge string
	// IssueReports lists openloc_report_issue: true when the deployment has
	// platform operators to read the reports.
	IssueReports bool
}

// Server holds the pre-built per-role SDK servers and serves them over HTTP.
type Server struct {
	deps Deps
	// One SDK server per role tier, built once at construction.
	//
	// Building per-request would mean re-running JSON Schema inference for every
	// tool on every call. Building one shared server would mean a linguist sees
	// manager tools in tools/list and gets a 403 from each. Two pre-built tiers
	// give correctly filtered listings at zero per-request cost.
	manager  *sdk.Server
	linguist *sdk.Server

	confirmKey []byte
	now        func() time.Time
}

// NewServer builds the MCP servers. Returns an error if Deps is incomplete.
func NewServer(deps Deps) (*Server, error) {
	if deps.Backend == nil {
		return nil, errors.New("mcp: Backend is required")
	}
	if deps.Version == "" {
		deps.Version = serverVersionUnset
	}
	s := &Server{deps: deps, confirmKey: deps.ConfirmKey, now: time.Now}
	if len(s.confirmKey) == 0 {
		s.confirmKey = signedtoken.RandomKey()
	}
	s.manager = s.build(true)
	s.linguist = s.build(false)
	return s, nil
}

// Handler returns the Streamable HTTP handler to mount at /mcp.
//
// Stateless mode (SEP-2567 / the 2026-07-28 spec direction): no Mcp-Session-Id,
// no session store, no sticky sessions. That means /mcp is an ordinary stateless
// route which scales across replicas on the existing Deployment with no extra
// infrastructure — the reason this endpoint is cheap to operate.
func (s *Server) Handler() http.Handler {
	return sdk.NewStreamableHTTPHandler(s.serverFor, &sdk.StreamableHTTPOptions{
		Stateless: true,
	})
}

// serverFor picks the role-appropriate server for this request.
//
// Claims are already in the request context because auth.Middleware ran first
// (the /mcp route is mounted inside the authenticated group). Returning nil
// makes the SDK serve 400; that should be unreachable, but it fails closed.
func (s *Server) serverFor(r *http.Request) *sdk.Server {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		logging.Error(r.Context(), nil, "MCP request reached the endpoint without auth claims; refused", "mcp-no-claims")
		return nil
	}
	if isManagerRole(claims.Role) {
		return s.manager
	}
	return s.linguist
}

// isManagerRole mirrors auth.IsManager, which operates on *http.Request. The MCP
// tool layer only ever has claims, so the role tiers are compared directly
// against the same constants — one source of truth for the role names.
func isManagerRole(role string) bool {
	return role == auth.RoleOwner || role == auth.RoleAdmin || role == auth.RoleProjectManager
}

// isOwnerAdminRole mirrors auth.RequireRole("owner", "admin") — the gate the
// REST routes for AI prompts and AI engines use, and the one writes to a SHARED
// dashboard follow. A project manager reads those but does not create them, and
// MCP must not become the way around that.
func isOwnerAdminRole(role string) bool {
	return role == auth.RoleOwner || role == auth.RoleAdmin
}

// build registers the tool set for one role tier.
//
// managerTier=false yields the linguist surface: their own tasks, and the
// job-scoped editor tools they legitimately need (segments, QA, TM, terms).
// The project/job *enumeration* tools are manager-only, matching the
// auth.RequireManager gate on the equivalent REST routes — a linguist must not
// be able to enumerate the org's projects through a side door.
func (s *Server) build(managerTier bool) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{
		Name:    serverName,
		Version: s.deps.Version,
	}, nil)
	if s.deps.StepUpChallenge != "" {
		srv.AddReceivingMiddleware(stepUpMiddleware(s.deps.StepUpChallenge))
	}
	b := s.deps.Backend

	// tools/list order does not depend on call order: the SDK sorts by name.
	registerDocs(s, srv, managerTier, b)
	registerProjects(s, srv, managerTier, b)
	registerDeadlines(s, srv, managerTier, b)
	registerJobs(s, srv, managerTier, b)
	registerTerminology(s, srv, managerTier, b)
	registerMetrics(s, srv, managerTier, b)
	registerTheme(s, srv, managerTier, b)
	registerAIConfig(s, srv, managerTier, b)
	registerProjectOps(s, srv, managerTier, b)
	registerJobOps(s, srv, managerTier, b)
	registerResources(s, srv, managerTier, b)
	registerConfig(s, srv, managerTier, b)
	registerInsights(s, srv, managerTier, b)
	registerIssues(s, srv, managerTier, b)

	return srv
}

// openOutputSchema infers the output schema for Out and then REMOVES the
// `additionalProperties: false` that Go-struct inference adds by default.
//
// Why: that flag turns every additive change into a breaking change. A client
// caches tools/list; we deploy a version that adds one field to a result; the
// client validates the new payload against its older schema and rejects the
// WHOLE response with "must NOT have additional properties" — intermittently,
// when the new field is omitempty.
//
// Closed output schemas buy us nothing: the server is the sole producer, and its
// payloads are generated from these very types. Input schemas stay strict —
// there, rejecting an unknown argument catches a model's typo, which is worth
// having.
//
// Returns nil on inference failure so AddTool falls back to its own inference;
// a schema we couldn't relax is better than no tool.
func openOutputSchema[Out any](tool string) any {
	schema, err := jsonschema.For[Out](nil)
	if err != nil {
		logging.Warn(context.Background(), err, "MCP output schema could not be inferred", "mcp-output-schema-failed", logging.Label("tool", tool))
		return nil
	}
	relaxAdditionalProperties(schema)
	return schema
}

// relaxAdditionalProperties clears the closed-object constraint throughout a
// schema — nested objects and array items included, since a field added to a
// nested type breaks clients the same way a top-level one does.
func relaxAdditionalProperties(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	s.AdditionalProperties = nil
	for _, prop := range s.Properties {
		relaxAdditionalProperties(prop)
	}
	relaxAdditionalProperties(s.Items)
	for _, sub := range s.Defs {
		relaxAdditionalProperties(sub)
	}
}

// toolSpec declares a tool's identity and its gates.
//
// A struct rather than positional booleans: with read and write tools plus role
// and capability gates, a call like addTool(s, srv, name, true, false, true, …)
// is unreadable and one transposed argument silently mis-authorises a tool.
type toolSpec struct {
	name string
	// title is the human-readable label clients show in tool listings and
	// confirmation dialogs. REQUIRED by MCP connector directories, which flag
	// untitled tools; addTool panics on an
	// empty one so a new tool can't ship without it.
	title       string
	description string

	// requireManager restricts the tool to owner/admin/project_manager. Such
	// tools are simply not registered on the linguist server, so the gate is
	// structural and tools/list is filtered correctly.
	requireManager bool

	// requireOwnerAdmin narrows requireManager further, to owner/admin only —
	// the gate the REST routes for AI prompts and AI engines carry. Registered
	// on the manager tier like any manager tool and refused at call time for a
	// project manager, exactly as the REST route registers and then refuses.
	requireOwnerAdmin bool

	// requireWrite demands a token created with capability="write".
	//
	// This is the gate that makes read-only tokens meaningful. Without it the
	// capability column is decoration: a token minted as "read" would be able to
	// mutate translation content the moment write tools exist.
	requireWrite bool

	// allowReadOnly marks a write that a read-only caller may still make,
	// because it changes none of the org's data: reporting an issue to the
	// platform operators. It skips the write-capability gate and stays in the
	// read-only chat (readOnlyModeMeta), but it is still annotated as a write,
	// gets the durable audit, and is guarded, so a person confirms it.
	allowReadOnly bool

	// destructive marks a write whose effect cannot be undone from the app, so
	// clients should confirm harder. Most writes here are additive or
	// reversible.
	destructive bool

	// openWorld marks a tool that sends content outside our own systems: to an
	// MT or AI vendor, to an extension, or as an email to a person. That
	// includes work the tool queues, not just what it does before returning.
	openWorld bool

	// meta is merged into the tool's _meta, for client-specific markers.
	meta map[string]any
}

// addTool registers one tool, wrapping the backend call with the cross-cutting
// concerns every tool needs: role gate, capability gate, error sanitisation, and
// the audit row.
//
// The runtime role check duplicates the structural tier filtering on purpose: if
// a future refactor registers a manager tool on the linguist tier, the request is
// denied and audited rather than silently authorised.
func addTool[In, Out any](
	s *Server,
	srv *sdk.Server,
	managerTier bool,
	spec toolSpec,
	fn func(context.Context, In) (Out, error),
) {
	if spec.requireManager && !managerTier {
		return
	}
	if spec.title == "" {
		panic("mcp: tool " + spec.name + " has no title (directory requirement)")
	}
	name := spec.name
	// Annotations are the protocol-native read/write signal: the
	// built-in chat keys its confirmation flow off them, and Desktop clients
	// use the same hints for their own approval UX. readOnly and the security
	// scheme's scope are derived from requireWrite so the gate and the hints
	// can never disagree; destructive and openWorld are per-tool. All hints
	// are explicit, because unset ones default to the cautious value.
	readOnly := !spec.requireWrite && !spec.allowReadOnly
	destructive := spec.destructive
	openWorld := spec.openWorld
	scope := auth.CapabilityRead
	if spec.requireWrite {
		scope = auth.CapabilityWrite
	}
	// securitySchemes goes in _meta, the mirror OpenAI documents for it: the
	// SDK's Tool has no top-level field for it.
	meta := sdk.Meta{"securitySchemes": []map[string]any{{"type": "oauth2", "scopes": []string{scope}}}}
	for k, v := range spec.meta {
		meta[k] = v
	}
	if spec.allowReadOnly {
		meta[readOnlyModeMeta] = true
	}
	sdk.AddTool(srv, &sdk.Tool{
		Meta:         meta,
		Name:         name,
		Description:  spec.description,
		OutputSchema: openOutputSchema[Out](name),
		Annotations: &sdk.ToolAnnotations{
			Title:           spec.title,
			ReadOnlyHint:    readOnly,
			DestructiveHint: &destructive,
			OpenWorldHint:   &openWorld,
		},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error) {
		started := time.Now()
		var zero Out

		if spec.requireOwnerAdmin {
			claims, ok := auth.ClaimsFromContext(ctx)
			if !ok || !isOwnerAdminRole(claims.Role) {
				record(ctx, s.deps.Audit, name, in, outcomeDenied, toolErrCodeDenied, started)
				return nil, zero, errors.New("only an owner or admin can do this")
			}
		}
		if spec.requireManager {
			claims, ok := auth.ClaimsFromContext(ctx)
			if !ok || !isManagerRole(claims.Role) {
				record(ctx, s.deps.Audit, name, in, outcomeDenied, toolErrCodeDenied, started)
				return nil, zero, errors.New("your role does not permit this operation")
			}
		}

		if spec.requireWrite {
			// Fails CLOSED when there is no PAT metadata at all. A caller on
			// /mcp is always a PAT in practice; a nil here means either a browser
			// cookie reached this route or a future credential type didn't
			// populate PATInfo. Neither should be able to mutate content, and
			// "unknown capability" must never be read as "allowed".
			// Two ways in: a write-capability PAT, or a human-confirmed write
			// grant (set only by the chat confirm executor for calls
			// a user just approved). The chat's normal loop has neither, so an
			// unconfirmed model write still fails closed.
			info := auth.PATInfoFromContext(ctx)
			patWrite := info != nil && info.Capability == auth.CapabilityWrite
			if !patWrite && !auth.HasWriteGrant(ctx) {
				record(ctx, s.deps.Audit, name, in, outcomeDenied, toolErrCodeReadOnly, started)
				if info != nil && info.Kind == auth.CredentialOAuth {
					return nil, zero, errInsufficientScope
				}
				return nil, zero, errors.New(writeRefused)
			}
		}

		// A tool never reopens a completed workflow task; a person does that
		// in the editor.
		out, err := fn(taskedit.WithoutReopen(ctx), in)
		if err != nil {
			// errNotFound is deliberately identical for "absent" and "belongs to
			// another tenant" so a caller cannot probe for other tenants' data.
			if errors.Is(err, errNotFound) {
				record(ctx, s.deps.Audit, name, in, outcomeError, toolErrCodeNotFound, started)
				return nil, zero, errors.New(
					"not found, or not accessible with your current access")
			}
			if errors.Is(err, errDenied) {
				record(ctx, s.deps.Audit, name, in, outcomeDenied, toolErrCodeDenied, started)
				return nil, zero, errors.New(strings.TrimPrefix(err.Error(), "denied: "))
			}
			// A concurrent edit. Told apart from not-found on purpose: the model
			// should re-read the segment and reconsider, not conclude it's gone.
			if errors.Is(err, errConflict) {
				record(ctx, s.deps.Audit, name, in, outcomeError, toolErrCodeConflict, started)
				return nil, zero, errors.New(
					"someone else changed this segment since you read it — " +
						"re-read it with openloc_list_segments and decide again " +
						"before overwriting their work")
			}
			// Caller-fixable mistakes carry their own message. These are authored
			// for the model, so unlike internal errors they are safe to surface
			// verbatim — that's why errInvalid is a distinct type rather than a
			// plain fmt.Errorf.
			var inv errInvalid
			if errors.As(err, &inv) {
				record(ctx, s.deps.Audit, name, in, outcomeError, toolErrCodeInvalid, started)
				return nil, zero, inv
			}
			var lim errLimited
			if errors.As(err, &lim) {
				record(ctx, s.deps.Audit, name, in, outcomeError, toolErrCodeRateLimited, started)
				return nil, zero, lim
			}
			// Everything else is logged in full server-side and reduced to a
			// generic message: raw error text can carry query fragments, schema
			// names, and connection details, and this response crosses a trust
			// boundary to a third-party client.
			logging.Error(ctx, err, "MCP tool failed", "mcp-tool-failed", logging.Label("tool", name))
			record(ctx, s.deps.Audit, name, in, outcomeError, toolErrCodeInternal, started)
			return nil, zero, fmt.Errorf("%s failed; the error has been logged", name)
		}

		// Mutations get the durable audit: the row is written
		// synchronously, and losing it is a reportable event, not a log line.
		// The write itself has already happened — say so honestly instead of
		// failing as if it hadn't.
		if spec.requireWrite || spec.allowReadOnly {
			if err := recordDurable(ctx, s.deps.Audit, name, in, outcomeOK, "", started); err != nil {
				logging.Error(ctx, err, "MCP durable write-audit failed", "mcp-audit-durable-failed", logging.Label("tool", name))
				return nil, zero, fmt.Errorf(
					"the change was applied, but could not be recorded in the audit log — report this to an administrator")
			}
			return nil, out, nil
		}
		record(ctx, s.deps.Audit, name, in, outcomeOK, "", started)
		return nil, out, nil
	})
}

// readOnlyModeMeta is the _meta key of a write tool the read-only chat keeps
// (toolSpec.allowReadOnly).
const readOnlyModeMeta = "openloc/allowedInReadOnly"

// AllowedInReadOnly reports whether a listed tool may be offered in the
// read-only chat: a read tool, or a write marked allowReadOnly.
func AllowedInReadOnly(t *sdk.Tool) bool {
	if t.Annotations != nil && t.Annotations.ReadOnlyHint {
		return true
	}
	allowed, _ := t.Meta[readOnlyModeMeta].(bool)
	return allowed
}

// AuditQuerier is the db.Querier slice used for the audit trail, re-exported so
// callers can wire a *db.Queries without importing the unexported interface.
type AuditQuerier = auditWriter

var _ AuditQuerier = (db.Querier)(nil)
