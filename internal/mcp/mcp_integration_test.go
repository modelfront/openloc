// MCP tool surface exercised over the real protocol: a genuine SDK
// client talks Streamable HTTP to the mounted /mcp route, authenticated with a
// real personal access token.
//
// Driving the actual protocol rather than calling backend methods directly is
// deliberate. The things most likely to break — role-filtered tools/list, tenant
// scoping in the direct backend, error mapping to isError, the auth handoff into
// tool context — all live between the transport and the query, and a
// backend-only test would sail straight past them.
package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/testutil"
)

// patTransport injects a personal access token on every MCP request.
type patTransport struct {
	token string
	base  http.RoundTripper
}

func (p *patTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+p.token)
	return p.base.RoundTrip(clone)
}

// connect opens an MCP session against the given test server using a PAT.
func connect(t *testing.T, serverURL, token string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "openloc-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint:   serverURL + "/mcp",
		HTTPClient: &http.Client{Transport: &patTransport{token: token, base: http.DefaultTransport}},
		// Stateless servers return 405 for GET, so the standalone SSE stream
		// (used only for server-initiated messages) must be off.
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect to /mcp: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callTool invokes a tool and returns the decoded structured output plus whether
// the call was reported as a tool error.
func callTool(t *testing.T, s *sdk.ClientSession, name string, args map[string]any, out any) (isErr bool, text string) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) protocol error: %v", name, err)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	if res.IsError {
		return true, text
	}
	if out != nil && res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured content: %v", err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("unmarshal structured content into %T: %v (raw: %s)", out, err, raw)
		}
	}
	return false, text
}

// fixture spins up a router + test server with two tenants of seeded content.
type fixture struct {
	q          db.Querier
	pool       *pgxpool.Pool
	url        string
	tenantA    db.Tenant
	tenantB    db.Tenant
	ownerA     db.User
	linguistA  db.User
	ownerB     db.User
	projectA   db.Project
	projectB   db.Project
	jobA       db.Job
	jobB       db.Job
	tokenA     string
	tokenLingA string
	tokenB     string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithStore(t, nil)
}

// newFixtureWithStore is newFixture with an object store behind uploads, for
// tools that read an assistant attachment or store a project file.
func newFixtureWithStore(t *testing.T, store file.BlobStore) *fixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)

	router := testutil.NewTestRouterWithStorage(pool, nil, store)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	f := &fixture{q: q, pool: pool, url: srv.URL}
	f.tenantA = testutil.SeedTenant(t, q, "McpTenantA")
	f.tenantB = testutil.SeedTenant(t, q, "McpTenantB")
	f.ownerA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "mcp-a@test.com", "pass", "owner")
	f.linguistA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "mcp-l@test.com", "pass", "linguist")
	f.ownerB = testutil.SeedUserWithRole(t, q, f.tenantB.ID, "mcp-b@test.com", "pass", "owner")

	f.projectA = testutil.SeedProject(t, q, f.tenantA.ID, "Alpha Project", "en", []string{"de"})
	f.projectB = testutil.SeedProject(t, q, f.tenantB.ID, "Bravo Project", "en", []string{"fr"})

	f.jobA = seedJobWithSegments(t, q, f.projectA.ID, "alpha.txt", "de", []string{
		"Hello world", "Second segment", "Third segment",
	})
	f.jobB = seedJobWithSegments(t, q, f.projectB.ID, "bravo.txt", "fr", []string{
		"Bonjour le monde",
	})

	f.tokenA = testutil.SeedAPIToken(t, q, f.tenantA.ID, f.ownerA.ID, auth.CapabilityRead, time.Hour)
	f.tokenLingA = testutil.SeedAPIToken(t, q, f.tenantA.ID, f.linguistA.ID, auth.CapabilityRead, time.Hour)
	f.tokenB = testutil.SeedAPIToken(t, q, f.tenantB.ID, f.ownerB.ID, auth.CapabilityRead, time.Hour)
	return f
}

// seedJobWithSegments creates a file with segments + translations and a job.
func seedJobWithSegments(t *testing.T, q db.Querier, projectID int64, fileName, targetLang string, sources []string) db.Job {
	t.Helper()
	ctx := context.Background()
	file := testutil.SeedFile(t, q, projectID, fileName, db.FileFormatTxt)
	for i, src := range sources {
		seg := testutil.SeedSegment(t, q, file.ID, src, int32(i))
		testutil.SeedTranslation(t, q, seg.ID, targetLang, db.TranslationStatusNeedsReview)
	}
	job, err := q.CreateJob(ctx, db.CreateJobParams{
		FileID:         file.ID,
		TargetLanguage: targetLang,
		WordCount:      int32(len(sources) * 2),
	})
	if err != nil {
		t.Fatalf("seed job: %v", err)
	}
	return job
}

// --- tools/list -------------------------------------------------------------

// The tool surface per role tier is pinned in testdata/tools_<tier>.golden:
// one line per tool with its gate (read / write / destructive). Adding,
// removing or re-gating a tool shows up as a readable diff in review.
// Regenerate with: go test ./internal/mcp/ -run TestIntegrationMCPToolListByRole -update
func TestIntegrationMCPToolListByRole(t *testing.T) {
	f := newFixture(t)

	managerTools := listToolNames(t, connect(t, f.url, f.tokenA))
	// Every tool is namespaced: a user may have a dozen servers connected, and
	// bare names collide with the other servers' tools.
	for _, name := range managerTools {
		if !strings.HasPrefix(name, "openloc_") {
			t.Errorf("tool %q is missing the openloc_ namespace", name)
		}
	}
	// The spec asks for deterministic ordering so clients can cache tools/list.
	for i := 1; i < len(managerTools); i++ {
		if managerTools[i-1] > managerTools[i] {
			t.Errorf("tools/list is not deterministically ordered: %v", managerTools)
			break
		}
	}

	// A linguist must not even SEE the enumeration and configuration tools —
	// a hidden tool is never called, which reads better than 403s for the
	// model to reason about. The linguist golden file is the allow-list.
	checkToolGolden(t, "manager", connect(t, f.url, f.tokenA))
	checkToolGolden(t, "linguist", connect(t, f.url, f.tokenLingA))
}

// Pins that every tool's outputSchema is an OBJECT. A bare Go slice infers
// `type: "array"`, which the negotiated protocol version forbids, and a real
// client then rejects the WHOLE tools/list — one bad schema makes no tool
// usable.
//
// This asserts on the emitted schema rather than on a successful round-trip:
// the Go SDK permits slices (SEP-2106 loosened it for 2026-07-28) and its own
// client doesn't validate the constraint, so a round-trip passes locally.
func TestIntegrationMCPOutputSchemasAreObjects(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools returned")
	}

	for _, tool := range res.Tools {
		if tool.OutputSchema == nil {
			t.Errorf("tool %q has no outputSchema", tool.Name)
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal outputSchema for %q: %v", tool.Name, err)
		}
		var schema struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshal outputSchema for %q: %v", tool.Name, err)
		}
		if schema.Type != "object" {
			t.Errorf("tool %q has outputSchema.type=%q, want \"object\" "+
				"— wrap the result in a struct (see the note in backend.go)",
				tool.Name, schema.Type)
		}
		// Same rule applies to inputSchema; the SDK enforces it for AddTool, so
		// this is belt-and-braces against a hand-written schema slipping in.
		if tool.InputSchema != nil {
			inRaw, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatalf("marshal inputSchema for %q: %v", tool.Name, err)
			}
			var inSchema struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(inRaw, &inSchema); err != nil {
				t.Fatalf("unmarshal inputSchema for %q: %v", tool.Name, err)
			}
			if inSchema.Type != "object" {
				t.Errorf("tool %q has inputSchema.type=%q, want \"object\"", tool.Name, inSchema.Type)
			}
		}
	}
}

func listToolNames(t *testing.T, s *sdk.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("tool %q has no description — the model has nothing to choose on", tool.Name)
		}
	}
	return names
}

// --- Happy paths ------------------------------------------------------------

func TestIntegrationMCPReadTools(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	t.Run("list_projects", func(t *testing.T) {
		var out struct {
			Projects []map[string]any `json:"projects"`
			Count    int              `json:"count"`
		}
		isErr, text := callTool(t, s, "openloc_list_projects", nil, &out)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if len(out.Projects) != 1 || out.Projects[0]["name"] != "Alpha Project" {
			t.Fatalf("got %+v, want only Alpha Project", out.Projects)
		}
		if out.Count != 1 {
			t.Errorf("count = %d, want 1", out.Count)
		}
	})

	t.Run("get_project", func(t *testing.T) {
		var out map[string]any
		isErr, text := callTool(t, s, "openloc_get_project",
			map[string]any{"project_id": f.projectA.ID}, &out)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if out["name"] != "Alpha Project" {
			t.Errorf("name = %v, want Alpha Project", out["name"])
		}
	})

	t.Run("list_jobs", func(t *testing.T) {
		var out struct {
			Jobs []map[string]any `json:"jobs"`
		}
		isErr, text := callTool(t, s, "openloc_list_jobs",
			map[string]any{"project_id": f.projectA.ID}, &out)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if len(out.Jobs) != 1 || out.Jobs[0]["file_name"] != "alpha.txt" {
			t.Fatalf("got %+v, want one job for alpha.txt", out.Jobs)
		}
	})

	t.Run("get_job", func(t *testing.T) {
		var out map[string]any
		isErr, text := callTool(t, s, "openloc_get_job",
			map[string]any{"job_id": f.jobA.ID}, &out)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if out["target_language"] != "de" || out["source_language"] != "en" {
			t.Errorf("languages = %v → %v, want en → de", out["source_language"], out["target_language"])
		}
		// The job names its project, as every list_jobs row does.
		if out["project_name"] != "Alpha Project" || out["project_id"] != float64(f.projectA.ID) {
			t.Errorf("project = %v (%v), want Alpha Project (%d)", out["project_name"], out["project_id"], f.projectA.ID)
		}
	})

	t.Run("list_segments", func(t *testing.T) {
		var out struct {
			Segments []struct {
				SourceText string `json:"source_text"`
				Position   int32  `json:"position"`
			} `json:"segments"`
			Total   int64 `json:"total"`
			HasMore bool  `json:"has_more"`
		}
		isErr, text := callTool(t, s, "openloc_list_segments",
			map[string]any{"job_id": f.jobA.ID}, &out)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if out.Total != 3 || len(out.Segments) != 3 {
			t.Fatalf("got %d of %d segments, want 3 of 3", len(out.Segments), out.Total)
		}
		if out.Segments[0].SourceText != "Hello world" {
			t.Errorf("first segment = %q, want %q", out.Segments[0].SourceText, "Hello world")
		}

		// The substring filter is what lets a model narrow instead of paging.
		var filtered struct {
			Segments []map[string]any `json:"segments"`
			Total    int64            `json:"total"`
		}
		callTool(t, s, "openloc_list_segments",
			map[string]any{"job_id": f.jobA.ID, "query": "Second"}, &filtered)
		if filtered.Total != 1 {
			t.Errorf("query filter matched %d, want 1", filtered.Total)
		}
	})

	t.Run("get_job_leverage", func(t *testing.T) {
		var out struct {
			JobID         int64 `json:"job_id"`
			TotalSegments int32 `json:"total_segments"`
			Bands         []struct {
				Band     string `json:"band"`
				Segments int32  `json:"segments"`
			} `json:"bands"`
		}
		isErr, text := callTool(t, s, "openloc_get_job_leverage",
			map[string]any{"job_id": f.jobA.ID}, &out)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		// Bands are derived live from the current translations, so a freshly
		// seeded job lands entirely in "new". total_segments is what lets a model
		// tell that apart from an empty file.
		if out.TotalSegments != 3 {
			t.Errorf("total_segments = %d, want 3", out.TotalSegments)
		}
		if len(out.Bands) != 1 || out.Bands[0].Band != "new" || out.Bands[0].Segments != 3 {
			t.Errorf("bands = %+v, want all 3 segments in 'new'", out.Bands)
		}
	})

	t.Run("get_qa_report", func(t *testing.T) {
		var out struct {
			Summary map[string]int   `json:"summary"`
			Issues  []map[string]any `json:"issues"`
		}
		isErr, text := callTool(t, s, "openloc_get_qa_report",
			map[string]any{"job_id": f.jobA.ID}, &out)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if out.Summary["error"] != 0 {
			t.Errorf("expected a clean QA summary, got %+v", out.Summary)
		}
	})

	t.Run("search_tm", func(t *testing.T) {
		var out struct {
			Matches []map[string]any `json:"matches"`
		}
		isErr, text := callTool(t, s, "openloc_search_tm", map[string]any{
			"project_id":      f.projectA.ID,
			"query":           "hello",
			"target_language": "de",
		}, &out)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
	})

	t.Run("lookup_term", func(t *testing.T) {
		ctx := context.Background()
		base := testutil.SeedTermBase(t, f.q, f.tenantA.ID, "Greetings", "en", "de")
		concept, err := f.q.CreateTermConcept(ctx, db.CreateTermConceptParams{TermBaseID: base.ID})
		if err != nil {
			t.Fatalf("create concept: %v", err)
		}
		for _, term := range []struct{ lang, text string }{{"en", "Hello"}, {"de", "Hallo"}} {
			if _, err := f.q.CreateTerm(ctx, db.CreateTermParams{
				ConceptID: concept.ID, Language: term.lang, Text: term.text, Status: "approved",
			}); err != nil {
				t.Fatalf("create term %q: %v", term.text, err)
			}
		}
		if _, err := f.q.CreateSettingsTermAssignment(ctx, db.CreateSettingsTermAssignmentParams{
			SettingsProfileID: f.projectA.SettingsProfileID, TermBaseID: base.ID,
			SourceLanguage: "en", TargetLanguage: "de", Mode: "read",
		}); err != nil {
			t.Fatalf("assign term base: %v", err)
		}

		var out struct {
			Terms []struct {
				SourceTerm   string `json:"source_term"`
				TargetTerm   string `json:"target_term"`
				TermBaseName string `json:"term_base_name"`
			} `json:"terms"`
		}
		isErr, text := callTool(t, s, "openloc_lookup_term", map[string]any{
			"project_id":      f.projectA.ID,
			"source_text":     "Hello world",
			"target_language": "de",
		}, &out)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if len(out.Terms) != 1 || out.Terms[0].SourceTerm != "Hello" || out.Terms[0].TargetTerm != "Hallo" ||
			out.Terms[0].TermBaseName != "Greetings" {
			t.Fatalf("terms = %+v, want Hello→Hallo from Greetings", out.Terms)
		}
	})

	t.Run("list_my_tasks", func(t *testing.T) {
		var out struct {
			Tasks []map[string]any `json:"tasks"`
		}
		isErr, text := callTool(t, s, "openloc_list_my_tasks", nil, &out)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
	})
}

// --- Tenant isolation -------------------------------------------------------

// This is the test that matters most. The direct backend bypasses the HTTP
// middleware chain that normally enforces access (RequireJobOwnership,
// RequireFileOwnership, RequireManager), so every tool taking an id needs its own
// proof that a foreign id is refused.
func TestIntegrationMCPCrossTenantIsolation(t *testing.T) {
	f := newFixture(t)
	// Tenant B's token, pointed at tenant A's resources.
	s := connect(t, f.url, f.tokenB)

	projectScoped := []string{
		"openloc_get_project",
		"openloc_list_jobs",
	}
	for _, tool := range projectScoped {
		t.Run(tool, func(t *testing.T) {
			isErr, text := callTool(t, s, tool, map[string]any{"project_id": f.projectA.ID}, nil)
			if !isErr {
				t.Fatalf("%s leaked tenant A's project to tenant B", tool)
			}
			assertOpaqueNotFound(t, text)
		})
	}

	jobScoped := []string{
		"openloc_get_job",
		"openloc_get_job_leverage",
		"openloc_get_qa_report",
		"openloc_list_segments",
	}
	for _, tool := range jobScoped {
		t.Run(tool, func(t *testing.T) {
			isErr, text := callTool(t, s, tool, map[string]any{"job_id": f.jobA.ID}, nil)
			if !isErr {
				t.Fatalf("%s leaked tenant A's job to tenant B", tool)
			}
			assertOpaqueNotFound(t, text)
		})
	}

	t.Run("openloc_search_tm", func(t *testing.T) {
		isErr, text := callTool(t, s, "openloc_search_tm", map[string]any{
			"project_id": f.projectA.ID, "query": "hello", "target_language": "de",
		}, nil)
		if !isErr {
			t.Fatal("search_tm accepted tenant A's project id from tenant B")
		}
		assertOpaqueNotFound(t, text)
	})

	t.Run("openloc_lookup_term", func(t *testing.T) {
		isErr, text := callTool(t, s, "openloc_lookup_term", map[string]any{
			"project_id": f.projectA.ID, "source_text": "Hello", "target_language": "de",
		}, nil)
		if !isErr {
			t.Fatal("lookup_term accepted tenant A's project id from tenant B")
		}
		assertOpaqueNotFound(t, text)
	})

	t.Run("list_projects shows only own tenant", func(t *testing.T) {
		var out struct {
			Projects []map[string]any `json:"projects"`
		}
		isErr, _ := callTool(t, s, "openloc_list_projects", nil, &out)
		if isErr {
			t.Fatal("list_projects should succeed for tenant B")
		}
		for _, p := range out.Projects {
			if p["name"] == "Alpha Project" {
				t.Fatal("tenant B enumerated tenant A's project")
			}
		}
	})
}

// assertOpaqueNotFound checks that a rejection reveals nothing about whether the
// resource exists elsewhere, and leaks no internal error detail.
func assertOpaqueNotFound(t *testing.T, text string) {
	t.Helper()
	if !strings.Contains(text, "not found") {
		t.Errorf("expected an opaque not-found message, got %q", text)
	}
	for _, leak := range []string{"tenant", "SELECT", "pgx", "sql:", "no rows"} {
		if strings.Contains(text, leak) {
			t.Errorf("error message leaks internals (%q): %q", leak, text)
		}
	}
}

// --- Role gating ------------------------------------------------------------

func TestIntegrationMCPLinguistDeniedManagerTools(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenLingA)

	// The tool isn't registered on the linguist server, so the SDK itself refuses
	// the call. Either a protocol-level rejection or an isError result is
	// acceptable; silently succeeding is not.
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "openloc_list_projects",
		Arguments: map[string]any{},
	})
	if err != nil {
		return // unknown tool — correctly unavailable
	}
	if !res.IsError {
		t.Fatal("a linguist successfully called the manager-only list_projects tool")
	}
}

// --- Auth boundary ----------------------------------------------------------

func TestIntegrationMCPRequiresAuth(t *testing.T) {
	f := newFixture(t)

	// Unauthenticated must be 401, NOT 500. /mcp is mounted inside the
	// authenticated group precisely because the tool layer reads claims from
	// context and auth.TenantIDFromContext panics when they're missing — a
	// misplaced mount would turn every anonymous probe into a panic.
	for _, tc := range []struct{ name, token string }{
		{"no token", ""},
		{"garbage PAT", auth.PATPrefix + "nope"},
		{"not a token at all", "abc123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("POST", f.url+"/mcp", strings.NewReader(`{}`))
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
}

func TestIntegrationMCPRevokedTokenLosesAccess(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	var out struct {
		Projects []map[string]any `json:"projects"`
	}
	if isErr, text := callTool(t, s, "openloc_list_projects", nil, &out); isErr {
		t.Fatalf("baseline call failed: %s", text)
	}

	// Revoke the token out from under a live session. Because the protocol is
	// stateless, every request re-authenticates — so revocation takes effect
	// immediately rather than at the end of some session lifetime.
	ctx := context.Background()
	tokens, err := f.q.ListAPITokensByTenant(ctx, f.tenantA.ID)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	for _, tok := range tokens {
		if tok.UserID == f.ownerA.ID {
			if err := f.q.RevokeAPIToken(ctx, db.RevokeAPITokenParams{ID: tok.ID, TenantID: f.tenantA.ID}); err != nil {
				t.Fatalf("revoke: %v", err)
			}
		}
	}

	if _, err := s.CallTool(ctx, &sdk.CallToolParams{
		Name: "openloc_list_projects", Arguments: map[string]any{},
	}); err == nil {
		t.Fatal("a revoked token still served a tool call")
	}
}

// --- Pagination -------------------------------------------------------------

func TestIntegrationMCPSegmentPaginationCap(t *testing.T) {
	f := newFixture(t)

	// 250 segments, over the 200 hard cap.
	sources := make([]string, 250)
	for i := range sources {
		sources[i] = "Segment number " + itoa(int64(i))
	}
	job := seedJobWithSegments(t, f.q, f.projectA.ID, "big.txt", "de", sources)

	s := connect(t, f.url, f.tokenA)

	var out struct {
		Segments   []map[string]any `json:"segments"`
		Total      int64            `json:"total"`
		HasMore    bool             `json:"has_more"`
		NextCursor int64            `json:"next_cursor"`
		Hint       string           `json:"hint"`
	}
	// Ask for far more than the cap. The server must clamp rather than reject —
	// a model asking for 5,000 rows should get 200 and a hint, not an error to
	// reason about.
	isErr, text := callTool(t, s, "openloc_list_segments",
		map[string]any{"job_id": job.ID, "limit": 5000}, &out)
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if len(out.Segments) != 200 {
		t.Errorf("returned %d segments, want the 200 cap", len(out.Segments))
	}
	if out.Total != 250 {
		t.Errorf("total = %d, want 250 — the model must know what it's missing", out.Total)
	}
	if !out.HasMore || out.NextCursor == 0 {
		t.Error("has_more/next_cursor must let the caller continue")
	}
	if !strings.Contains(out.Hint, "narrow") {
		t.Errorf("hint should tell the model how to narrow, got %q", out.Hint)
	}

	// The default (no limit) must be the smaller page size.
	var deflt struct {
		Segments []map[string]any `json:"segments"`
	}
	callTool(t, s, "openloc_list_segments", map[string]any{"job_id": job.ID}, &deflt)
	if len(deflt.Segments) != 50 {
		t.Errorf("default page = %d segments, want 50", len(deflt.Segments))
	}

	// Paging with the cursor must advance rather than repeat page one.
	var page2 struct {
		Segments []struct {
			Position int32 `json:"position"`
		} `json:"segments"`
	}
	callTool(t, s, "openloc_list_segments",
		map[string]any{"job_id": job.ID, "cursor": out.NextCursor, "limit": 10}, &page2)
	if len(page2.Segments) == 0 {
		t.Fatal("second page is empty")
	}
	// Page 2 must start exactly at next_cursor — no gap (a skipped segment) and
	// no overlap (a re-served one). Both are silent data-integrity bugs for a
	// model paging through a file.
	if int64(page2.Segments[0].Position) != out.NextCursor {
		t.Errorf("page 2 starts at position %d, want exactly next_cursor %d",
			page2.Segments[0].Position, out.NextCursor)
	}
}

// --- Error mapping ----------------------------------------------------------

func TestIntegrationMCPMissingResourceIsToolError(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	// A missing job must come back as a TOOL error (isError), not a protocol
	// error. That distinction matters: a tool error is something the model reads
	// and adapts to; a protocol error is a transport failure it can't act on.
	isErr, text := callTool(t, s, "openloc_get_job", map[string]any{"job_id": int64(999999)}, nil)
	if !isErr {
		t.Fatal("expected a tool error for a nonexistent job")
	}
	assertOpaqueNotFound(t, text)
}

// --- Audit ------------------------------------------------------------------

func TestIntegrationMCPAuditTrail(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	var out struct {
		Projects []map[string]any `json:"projects"`
	}
	callTool(t, s, "openloc_list_projects", nil, &out)
	// A denied/failed call must be audited too, not just successes.
	callTool(t, s, "openloc_get_job", map[string]any{"job_id": int64(999999)}, nil)

	// The audit write is deliberately fire-and-forget (it must not add a round
	// trip to every tool call), so poll briefly rather than assuming it landed.
	ctx := context.Background()
	var rows []db.ListMCPToolCallsRow
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		rows, err = f.q.ListMCPToolCalls(ctx, db.ListMCPToolCallsParams{
			TenantID: f.tenantA.ID, Cursor: 0, RowLimit: 50,
		})
		if err != nil {
			t.Fatalf("list audit rows: %v", err)
		}
		if len(rows) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 audit rows, got %d", len(rows))
	}

	byTool := map[string]db.ListMCPToolCallsRow{}
	for _, r := range rows {
		byTool[r.Tool] = r
		// The digest must never be the arguments themselves — tool args carry
		// source content, and this table must not become a second copy of it.
		if strings.Contains(r.ArgsDigest, "999999") || strings.Contains(r.ArgsDigest, "project") {
			t.Errorf("audit row stored raw arguments: %q", r.ArgsDigest)
		}
		if r.TokenID == nil {
			t.Error("audit row is missing the token id — attribution is the point")
		}
	}
	if ok := byTool["openloc_list_projects"]; ok.Outcome != "ok" {
		t.Errorf("successful call recorded as %q, want ok", ok.Outcome)
	}
	if bad := byTool["openloc_get_job"]; bad.Outcome != "error" {
		t.Errorf("failed call recorded as %q, want error", bad.Outcome)
	}
	if code := byTool["openloc_get_job"].ErrorCode; code == nil || *code != "not_found" {
		t.Errorf("expected error_code=not_found, got %v", code)
	}
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b[i:])
}

// --- Write tools --------------------------------------------------------------

// seedWriteFixture adds write-capability tokens to the standard fixture.
func writeToken(t *testing.T, f *fixture, tenantID, userID int64) string {
	t.Helper()
	return testutil.SeedAPIToken(t, f.q, tenantID, userID, auth.CapabilityWrite, time.Hour)
}

// firstTranslationID returns a translation_id from job A via the read tool, so
// the write tests exercise the same id path a model would actually follow.
func firstTranslation(t *testing.T, s *sdk.ClientSession, jobID int64) (id int64, updatedAt string) {
	t.Helper()
	var out struct {
		Segments []struct {
			TranslationID int64  `json:"translation_id"`
			UpdatedAt     string `json:"updated_at"`
		} `json:"segments"`
	}
	if isErr, text := callTool(t, s, "openloc_list_segments",
		map[string]any{"job_id": jobID, "limit": 1}, &out); isErr {
		t.Fatalf("list_segments failed: %s", text)
	}
	if len(out.Segments) == 0 {
		t.Fatal("no segments returned")
	}
	return out.Segments[0].TranslationID, out.Segments[0].UpdatedAt
}

// THE write-safety test: a read-only token must not be able to mutate anything.
//
// Without this, api_tokens.capability is decoration — the column exists, the UI
// offers the choice, and every write tool would accept a token created as "read".
func TestIntegrationMCPReadOnlyTokenCannotWrite(t *testing.T) {
	f := newFixture(t)
	readSession := connect(t, f.url, f.tokenA) // f.tokenA is capability=read
	translationID, _ := firstTranslation(t, readSession, f.jobA.ID)

	writes := []struct {
		tool string
		args map[string]any
	}{
		{"openloc_add_comment", map[string]any{"translation_id": translationID, "content": "should not land"}},
		{"openloc_lock_segment", map[string]any{"translation_id": translationID}},
		{"openloc_save_draft", map[string]any{"translation_id": translationID, "text": "should not land"}},
		{"openloc_run_qa", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_ignore_qa_finding", map[string]any{"translation_id": translationID, "check_type": "punctuation"}},
	}
	for _, w := range writes {
		t.Run(w.tool, func(t *testing.T) {
			isErr, text := callTool(t, readSession, w.tool, w.args, nil)
			if !isErr {
				t.Fatalf("%s succeeded with a READ-ONLY token", w.tool)
			}
			if !strings.Contains(text, "write access") {
				t.Errorf("expected a read-only refusal, got %q", text)
			}
		})
	}

	// Prove the refusals were real, not a 200 with a scary message: the segment
	// must be untouched and uncommented.
	after, err := f.q.GetTranslationTenant(context.Background(), db.GetTranslationTenantParams{
		ID: translationID, TenantID: f.tenantA.ID,
	})
	if err != nil {
		t.Fatalf("re-read translation: %v", err)
	}
	if after.Locked {
		t.Error("read-only token managed to lock the segment")
	}
	if after.ActiveText != nil && *after.ActiveText != "" {
		t.Errorf("read-only token managed to write text: %q", *after.ActiveText)
	}
	comments, err := f.q.ListCommentsByTranslation(context.Background(), translationID)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("read-only token managed to add %d comment(s)", len(comments))
	}
}

func TestIntegrationMCPWriteTools(t *testing.T) {
	f := newFixture(t)
	token := writeToken(t, f, f.tenantA.ID, f.ownerA.ID)
	s := connect(t, f.url, token)
	translationID, updatedAt := firstTranslation(t, s, f.jobA.ID)
	ctx := context.Background()

	t.Run("add_comment", func(t *testing.T) {
		var out struct {
			CommentID int64 `json:"comment_id"`
		}
		if isErr, text := callTool(t, s, "openloc_add_comment",
			map[string]any{"translation_id": translationID, "content": "check terminology here"}, &out); isErr {
			t.Fatalf("unexpected error: %s", text)
		}
		if out.CommentID == 0 {
			t.Fatal("no comment_id returned")
		}
		// Assert on DB state, not just the response.
		comments, err := f.q.ListCommentsByTranslation(ctx, translationID)
		if err != nil {
			t.Fatalf("list comments: %v", err)
		}
		if len(comments) != 1 || comments[0].Content != "check terminology here" {
			t.Fatalf("comment not persisted: %+v", comments)
		}
	})

	t.Run("add_comment rejects empty content", func(t *testing.T) {
		isErr, text := callTool(t, s, "openloc_add_comment",
			map[string]any{"translation_id": translationID, "content": "   "}, nil)
		if !isErr {
			t.Fatal("blank comment was accepted")
		}
		if !strings.Contains(text, "required") {
			t.Errorf("expected an actionable message, got %q", text)
		}
	})

	t.Run("save_draft writes text but does NOT approve", func(t *testing.T) {
		var out struct {
			Status           string `json:"status"`
			UpdatedAt        string `json:"updated_at"`
			TMEntriesWritten int    `json:"tm_entries_written"`
		}
		if isErr, text := callTool(t, s, "openloc_save_draft", map[string]any{
			"translation_id":      translationID,
			"text":                "Hallo Welt",
			"expected_updated_at": updatedAt,
		}, &out); isErr {
			t.Fatalf("unexpected error: %s", text)
		}
		// The load-bearing assertion of the write tools: an agent's draft
		// must not become approved, and must not reach translation memory.
		if out.Status == string(db.TranslationStatusApproved) {
			t.Error("save_draft approved the segment — drafts must stay in review")
		}
		if out.TMEntriesWritten != 0 {
			t.Errorf("save_draft wrote %d TM entries, want 0", out.TMEntriesWritten)
		}
		got, err := f.q.GetTranslationTenant(ctx, db.GetTranslationTenantParams{
			ID: translationID, TenantID: f.tenantA.ID,
		})
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if got.ActiveText == nil || *got.ActiveText != "Hallo Welt" {
			t.Errorf("text not persisted, got %v", got.ActiveText)
		}
		if got.Status == db.TranslationStatusApproved {
			t.Error("DB shows approved after a draft")
		}
	})

	t.Run("save_draft detects a concurrent edit", func(t *testing.T) {
		// Deliberately stale version — the previous subtest already moved
		// updated_at forward.
		isErr, text := callTool(t, s, "openloc_save_draft", map[string]any{
			"translation_id":      translationID,
			"text":                "should be refused",
			"expected_updated_at": updatedAt,
		}, nil)
		if !isErr {
			t.Fatal("stale expected_updated_at was accepted — lost update")
		}
		if !strings.Contains(text, "changed this segment") {
			t.Errorf("expected a conflict message, got %q", text)
		}
	})

	t.Run("save_draft rejects a malformed version", func(t *testing.T) {
		// A typo must NOT silently disable optimistic locking.
		isErr, text := callTool(t, s, "openloc_save_draft", map[string]any{
			"translation_id":      translationID,
			"text":                "x",
			"expected_updated_at": "yesterday",
		}, nil)
		if !isErr {
			t.Fatal("unparseable expected_updated_at was silently ignored")
		}
		if !strings.Contains(text, "RFC3339") {
			t.Errorf("expected a format hint, got %q", text)
		}
	})

	t.Run("lock then draft is refused", func(t *testing.T) {
		if isErr, text := callTool(t, s, "openloc_lock_segment",
			map[string]any{"translation_id": translationID}, nil); isErr {
			t.Fatalf("lock failed: %s", text)
		}
		isErr, text := callTool(t, s, "openloc_save_draft",
			map[string]any{"translation_id": translationID, "text": "nope"}, nil)
		if !isErr {
			t.Fatal("wrote to a locked segment")
		}
		if !strings.Contains(text, "locked") {
			t.Errorf("expected a locked message, got %q", text)
		}
	})

	t.Run("owner can unlock", func(t *testing.T) {
		var out struct {
			Locked bool `json:"locked"`
		}
		if isErr, text := callTool(t, s, "openloc_lock_segment",
			map[string]any{"translation_id": translationID, "unlock": true}, &out); isErr {
			t.Fatalf("unlock failed: %s", text)
		}
		if out.Locked {
			t.Error("still locked after unlock")
		}
	})
}

// Locking approves the segment, so only managers lock; anyone who may edit
// the segment unlocks it. The assistant's lock is the assistant's approval and
// stays out of TM, like its confirms.
func TestIntegrationMCPLockRoles(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ownerSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	translationID, _ := firstTranslation(t, ownerSession, f.jobA.ID)
	if _, err := f.pool.Exec(ctx, `UPDATE translations SET active_text = 'Hallo' WHERE id = $1`, translationID); err != nil {
		t.Fatalf("set text: %v", err)
	}
	read := func() db.Translation {
		t.Helper()
		got, err := f.q.GetTranslationTenant(ctx, db.GetTranslationTenantParams{ID: translationID, TenantID: f.tenantA.ID})
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		return got
	}

	// The linguist is assigned, so only the role can refuse the lock.
	testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID, &f.linguistA.ID)
	lingSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.linguistA.ID))
	isErr, text := callTool(t, lingSession, "openloc_lock_segment", map[string]any{"translation_id": translationID}, nil)
	if !isErr || !strings.Contains(text, "project manager") {
		t.Fatalf("a linguist's lock should be refused with a role message, got isErr=%v %q", isErr, text)
	}
	if read().Locked {
		t.Fatal("the segment got locked despite the refusal")
	}

	if isErr, text := callTool(t, ownerSession, "openloc_lock_segment", map[string]any{"translation_id": translationID}, nil); isErr {
		t.Fatalf("owner lock failed: %s", text)
	}
	if got := read(); !got.Locked || got.Status != db.TranslationStatusApproved || got.ConfirmedVia != "assistant" {
		t.Errorf("after the assistant's lock: locked=%v status=%s confirmed_via=%s, want locked approved assistant",
			got.Locked, got.Status, got.ConfirmedVia)
	}

	if isErr, text := callTool(t, lingSession, "openloc_lock_segment",
		map[string]any{"translation_id": translationID, "unlock": true}, nil); isErr {
		t.Fatalf("assigned linguist unlock failed: %s", text)
	}
	if got := read(); got.Locked || got.Status != db.TranslationStatusApproved {
		t.Errorf("after unlock: locked=%v status=%s, want unlocked, still approved", got.Locked, got.Status)
	}
}

// Cross-tenant proof for every write tool. directBackend bypasses the ownership
// middleware, so each write needs its own evidence that a foreign id is refused.
func TestIntegrationMCPWriteToolsCrossTenant(t *testing.T) {
	f := newFixture(t)
	// Tenant A's segment...
	aSession := connect(t, f.url, f.tokenA)
	translationID, updatedAt := firstTranslation(t, aSession, f.jobA.ID)

	// ...targeted by tenant B's WRITE token, so the capability gate can't be
	// what stops it — only tenant scoping can.
	bToken := writeToken(t, f, f.tenantB.ID, f.ownerB.ID)
	bSession := connect(t, f.url, bToken)

	writes := []struct {
		tool string
		args map[string]any
	}{
		{"openloc_add_comment", map[string]any{"translation_id": translationID, "content": "cross tenant"}},
		{"openloc_lock_segment", map[string]any{"translation_id": translationID}},
		{"openloc_save_draft", map[string]any{"translation_id": translationID, "text": "cross tenant", "expected_updated_at": updatedAt}},
		{"openloc_run_qa", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_ignore_qa_finding", map[string]any{"translation_id": translationID, "check_type": "punctuation"}},
	}
	for _, w := range writes {
		t.Run(w.tool, func(t *testing.T) {
			isErr, text := callTool(t, bSession, w.tool, w.args, nil)
			if !isErr {
				t.Fatalf("%s mutated another tenant's segment", w.tool)
			}
			assertOpaqueNotFound(t, text)
		})
	}

	after, err := f.q.GetTranslationTenant(context.Background(), db.GetTranslationTenantParams{
		ID: translationID, TenantID: f.tenantA.ID,
	})
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if after.Locked || (after.ActiveText != nil && *after.ActiveText != "") {
		t.Error("tenant B's writes actually landed on tenant A's segment")
	}
}

// list_members must never leak email addresses.
func TestIntegrationMCPListMembersOmitsEmail(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	var out struct {
		Members []map[string]any `json:"members"`
		Count   int              `json:"count"`
	}
	isErr, text := callTool(t, s, "openloc_list_members", nil, &out)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if out.Count == 0 {
		t.Fatal("no members returned")
	}
	// Assert on the exact key set rather than substring-matching for "@": seeded
	// users (and Google sign-ins with no display name) legitimately have
	// name == email, so a substring check would fail for the wrong reason. What
	// must hold is that we expose no email FIELD and nothing beyond the three
	// fields assignment needs — that's what stops the payload growing into an
	// address book by accident.
	allowed := map[string]bool{
		"user_id": true, "name": true, "role": true,
		// Vendor questions ("who translates en→es?",
		// "who's busy right now?") need these on the enumeration.
		"language_pairs": true, "active_jobs": true,
	}
	for _, m := range out.Members {
		for k := range m {
			if !allowed[k] {
				t.Errorf("list_members exposes unexpected field %q: %+v", k, m)
			}
		}
		if m["name"] == nil || m["role"] == nil || m["user_id"] == nil {
			t.Errorf("member missing a field needed for assignment: %+v", m)
		}
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), `"email"`) {
		t.Errorf("list_members exposes an email field: %s", raw)
	}
}

// list_files must surface files that have no jobs — the gap list_jobs can't fill.
func TestIntegrationMCPListFilesIncludesJoblessFiles(t *testing.T) {
	f := newFixture(t)
	// A file with segments but NO job.
	orphan := testutil.SeedFile(t, f.q, f.projectA.ID, "no-jobs.txt", db.FileFormatTxt)
	testutil.SeedSegment(t, f.q, orphan.ID, "orphan segment", 0)

	s := connect(t, f.url, f.tokenA)
	var out struct {
		Files []struct {
			ID       int64  `json:"id"`
			Name     string `json:"name"`
			JobCount int    `json:"job_count"`
		} `json:"files"`
	}
	if isErr, text := callTool(t, s, "openloc_list_files",
		map[string]any{"project_id": f.projectA.ID}, &out); isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	byName := map[string]int{}
	for _, file := range out.Files {
		byName[file.Name] = file.JobCount
	}
	if _, ok := byName["no-jobs.txt"]; !ok {
		t.Fatalf("jobless file missing from list_files: %+v", out.Files)
	}
	if byName["no-jobs.txt"] != 0 {
		t.Errorf("job_count = %d for a file with no jobs, want 0", byName["no-jobs.txt"])
	}
	if byName["alpha.txt"] != 1 {
		t.Errorf("job_count = %d for alpha.txt, want 1", byName["alpha.txt"])
	}
}

// --- Deadline provenance ------------------------------------------------------

// A job's deadline must be READ from its ACTIVE STEP ASSIGNMENT, never
// substituted from its project at read time (the jobs table has no
// deadline column). Materialization may well have seeded that
// assignment from the project deadline — that is a write
// the PM can see and change; reading around the column is not.
//
// A project-sourced `deadline` makes every job appear to inherit its project's
// due date, which is worse than omitting the field: a model has no way to
// detect a confidently wrong value, and "is anything overdue?" gets fabricated
// answers.
//
// The test sets the project and the step to DIFFERENT dates precisely so a
// project-sourced value can't accidentally pass.
func TestIntegrationMCPJobDeadlineComesFromStepNotProject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	projectDue := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	stepDue := time.Date(2026, 9, 15, 17, 0, 0, 0, time.UTC)

	// Project deadline — must NOT surface as the job's.
	if _, err := f.q.UpdateProjectMetadata(ctx, db.UpdateProjectMetadataParams{
		ID:       f.projectA.ID,
		TenantID: f.tenantA.ID,
		Deadline: pgtype.Timestamptz{Time: projectDue, Valid: true},
	}); err != nil {
		t.Fatalf("set project deadline: %v", err)
	}

	// Active step assignment with its own, different deadline.
	assignmentID := testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID, &f.linguistA.ID)
	if err := f.q.UpdateStepAssignmentDeadline(ctx, db.UpdateStepAssignmentDeadlineParams{
		ID:       assignmentID,
		Deadline: pgtype.Timestamptz{Time: stepDue, Valid: true},
	}); err != nil {
		t.Fatalf("set step deadline: %v", err)
	}

	s := connect(t, f.url, f.tokenA)

	t.Run("get_job", func(t *testing.T) {
		var out struct {
			Deadline        string `json:"deadline"`
			ProjectDeadline string `json:"project_deadline"`
		}
		if isErr, text := callTool(t, s, "openloc_get_job",
			map[string]any{"job_id": f.jobA.ID}, &out); isErr {
			t.Fatalf("unexpected error: %s", text)
		}
		if !strings.HasPrefix(out.Deadline, "2026-09-15") {
			t.Errorf("job deadline = %q, want the STEP deadline (2026-09-15…)", out.Deadline)
		}
		// The specific failure mode being guarded: the project's date leaking
		// into the job's deadline field.
		if strings.HasPrefix(out.Deadline, "2027-01-01") {
			t.Error("job deadline is the PROJECT's date, read around the step assignment")
		}
		// The project's deadline is still available, just not mislabelled.
		if !strings.HasPrefix(out.ProjectDeadline, "2027-01-01") {
			t.Errorf("project_deadline = %q, want 2027-01-01…", out.ProjectDeadline)
		}
	})

	t.Run("list_jobs carries the deadline", func(t *testing.T) {
		// This is what makes "any overdue jobs?" answerable in one call.
		var out struct {
			Jobs []struct {
				ID       int64  `json:"id"`
				Deadline string `json:"deadline"`
			} `json:"jobs"`
		}
		if isErr, text := callTool(t, s, "openloc_list_jobs",
			map[string]any{"project_id": f.projectA.ID}, &out); isErr {
			t.Fatalf("unexpected error: %s", text)
		}
		var found bool
		for _, j := range out.Jobs {
			if j.ID != f.jobA.ID {
				continue
			}
			found = true
			if !strings.HasPrefix(j.Deadline, "2026-09-15") {
				t.Errorf("list_jobs deadline = %q, want the step deadline", j.Deadline)
			}
		}
		if !found {
			t.Fatalf("job %d missing from list_jobs: %+v", f.jobA.ID, out.Jobs)
		}
	})

	t.Run("a job with no step assignment reports no deadline", func(t *testing.T) {
		// jobB is in tenant B; use a fresh jobless-step job in tenant A instead.
		bare := seedJobWithSegments(t, f.q, f.projectA.ID, "bare.txt", "de", []string{"x"})
		var out struct {
			Deadline string `json:"deadline"`
		}
		if isErr, text := callTool(t, s, "openloc_get_job",
			map[string]any{"job_id": bare.ID}, &out); isErr {
			t.Fatalf("unexpected error: %s", text)
		}
		// Empty, NOT the project's 2027 date — the absence of a fallback is the
		// behaviour being pinned.
		if out.Deadline != "" {
			t.Errorf("deadline = %q for a job with no step assignment, want empty", out.Deadline)
		}
	})
}

// --- Customer resolution ------------------------------------------------------

// "any overdue jobs in ANY project?" must not need
// list_projects + one list_jobs PER project. list_jobs must work org-wide with a
// server-side overdue filter — client-side filtering breaks silently once the
// org-wide listing exceeds the row cap.
func TestIntegrationMCPListJobsOrgWideOverdue(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// A second project in tenant A, so "org-wide" genuinely crosses projects.
	projectA2 := testutil.SeedProject(t, q(f), f.tenantA.ID, "Second Alpha", "en", []string{"fr"})
	jobA2 := seedJobWithSegments(t, f.q, projectA2.ID, "second.txt", "fr", []string{"One"})

	// jobA2 (project 2): OVERDUE — current step assigned, in progress, deadline past.
	// jobA (project 1): has a deadline in the future — must NOT match.
	// jobB (tenant B): overdue — must NEVER appear for tenant A.
	assignOverdueStep := func(projectID, jobID int64, deadline time.Time, status string) {
		t.Helper()
		project, err := f.q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: f.tenantA.ID})
		if err != nil {
			t.Fatalf("load project: %v", err)
		}
		testutil.SeedWorkflow(t, f.q, project.SettingsProfileID, jobID, testutil.WorkflowStepSpec{
			Name: "Translate", Assignee: &f.linguistA.ID, Status: status, Deadline: deadline,
		})
	}
	assignOverdueStep(projectA2.ID, jobA2.ID, time.Now().Add(-48*time.Hour), "in_progress")
	assignOverdueStep(f.projectA.ID, f.jobA.ID, time.Now().Add(72*time.Hour), "in_progress")

	s := connect(t, f.url, f.tokenA)

	type jobRow struct {
		ID          int64  `json:"id"`
		ProjectID   int64  `json:"project_id"`
		ProjectName string `json:"project_name"`
		Deadline    string `json:"deadline"`
	}
	var out struct {
		Jobs []jobRow `json:"jobs"`
	}

	// One call, no project_id, overdue filter on.
	if isErr, text := callTool(t, s, "openloc_list_jobs", map[string]any{"overdue_only": true}, &out); isErr {
		t.Fatalf("org-wide overdue call errored: %s", text)
	}
	if len(out.Jobs) != 1 {
		t.Fatalf("overdue jobs = %+v, want exactly jobA2", out.Jobs)
	}
	got := out.Jobs[0]
	if got.ID != jobA2.ID {
		t.Fatalf("wrong job flagged overdue: %+v", got)
	}
	// Org-wide rows must SAY which project they belong to — otherwise the
	// model re-enters the per-project N+1 just to label its answer.
	if got.ProjectID != projectA2.ID || got.ProjectName != "Second Alpha" {
		t.Fatalf("overdue row not labeled with its project: %+v", got)
	}
	if got.Deadline == "" {
		t.Fatal("overdue row missing its deadline")
	}

	// Without the filter, org-wide returns BOTH tenant-A jobs and still no
	// tenant-B leakage.
	if isErr, text := callTool(t, s, "openloc_list_jobs", map[string]any{}, &out); isErr {
		t.Fatalf("org-wide unfiltered call errored: %s", text)
	}
	ids := map[int64]bool{}
	for _, j := range out.Jobs {
		ids[j.ID] = true
	}
	if !ids[f.jobA.ID] || !ids[jobA2.ID] {
		t.Fatalf("org-wide listing missing tenant A jobs: %+v", out.Jobs)
	}
	if ids[f.jobB.ID] {
		t.Fatal("org-wide listing leaked tenant B's job")
	}

	// Per-project behavior unchanged: explicit project_id still scopes.
	if isErr, text := callTool(t, s, "openloc_list_jobs", map[string]any{"project_id": f.projectA.ID}, &out); isErr {
		t.Fatalf("per-project call errored: %s", text)
	}
	for _, j := range out.Jobs {
		if j.ProjectID != f.projectA.ID {
			t.Fatalf("project-scoped call returned foreign job: %+v", j)
		}
	}
}

// q adapts the fixture for helpers written against db.Querier.
func q(f *fixture) db.Querier { return f.q }

// "which vendors translate en→es?" and "who's currently
// working?" must be answerable from list_members, so it carries language pairs
// and active assignments, not only name+role: the enumeration must carry
// what the questions need.
func TestIntegrationMCPMembersCarryPairsAndActiveJobs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// linguistA translates en→es and is actively assigned to jobA's current step.
	if err := f.q.InsertUserLanguagePair(ctx, db.InsertUserLanguagePairParams{
		UserID: f.linguistA.ID, SourceLang: "en", TargetLang: "es",
		RowGroup: 0, TenantID: f.tenantA.ID,
	}); err != nil {
		t.Fatalf("seed language pair: %v", err)
	}
	project, err := f.q.GetProject(ctx, db.GetProjectParams{ID: f.projectA.ID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	testutil.SeedWorkflow(t, f.q, project.SettingsProfileID, f.jobA.ID, testutil.WorkflowStepSpec{
		Name: "Translate", Assignee: &f.linguistA.ID, Status: "in_progress",
	})

	s := connect(t, f.url, f.tokenA)
	var out struct {
		Members []struct {
			UserID        int64    `json:"user_id"`
			Role          string   `json:"role"`
			LanguagePairs []string `json:"language_pairs"`
			ActiveJobs    int64    `json:"active_jobs"`
		} `json:"members"`
	}
	if isErr, text := callTool(t, s, "openloc_list_members", nil, &out); isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var linguist, owner *struct {
		UserID        int64    `json:"user_id"`
		Role          string   `json:"role"`
		LanguagePairs []string `json:"language_pairs"`
		ActiveJobs    int64    `json:"active_jobs"`
	}
	for i := range out.Members {
		switch out.Members[i].UserID {
		case f.linguistA.ID:
			linguist = &out.Members[i]
		case f.ownerA.ID:
			owner = &out.Members[i]
		}
	}
	if linguist == nil || owner == nil {
		t.Fatalf("seeded members missing from list: %+v", out.Members)
	}

	if len(linguist.LanguagePairs) != 1 || linguist.LanguagePairs[0] != "en→es" {
		t.Fatalf("linguist language_pairs = %v, want [en→es]", linguist.LanguagePairs)
	}
	if linguist.ActiveJobs != 1 {
		t.Fatalf("linguist active_jobs = %d, want 1 (assigned to jobA's current step)", linguist.ActiveJobs)
	}

	// A member with no pairs and no assignments carries EMPTY values, present —
	// "no pairs recorded" must be distinguishable from "field doesn't exist".
	if owner.LanguagePairs == nil || len(owner.LanguagePairs) != 0 {
		t.Fatalf("owner language_pairs = %v, want []", owner.LanguagePairs)
	}
	if owner.ActiveJobs != 0 {
		t.Fatalf("owner active_jobs = %d, want 0", owner.ActiveJobs)
	}
}

// "which projects are overdue?" must be one list_projects
// call, not one get_project PER PROJECT, so the list rows carry the deadline.
// Same rule as the customer name: data a model needs to answer the
// question must ride the enumeration, not hide behind per-row lookups.
func TestIntegrationMCPProjectListCarriesDeadline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	deadline := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	if _, err := f.q.UpdateProjectMetadata(ctx, db.UpdateProjectMetadataParams{
		ID: f.projectA.ID, TenantID: f.tenantA.ID,
		Deadline: pgtype.Timestamptz{Time: deadline, Valid: true},
	}); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	s := connect(t, f.url, f.tokenA)
	var out struct {
		Projects []struct {
			ID       int64   `json:"id"`
			Deadline *string `json:"deadline"`
		} `json:"projects"`
	}
	if isErr, text := callTool(t, s, "openloc_list_projects", nil, &out); isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	raw, _ := json.Marshal(out) // presence check below is on the decoded struct; this is for failure messages
	var withDeadline, checked bool
	for _, p := range out.Projects {
		if p.ID == f.projectA.ID {
			checked = true
			if p.Deadline == nil || *p.Deadline == "" {
				t.Fatalf("project %d deadline missing from list_projects: %s", p.ID, raw)
			}
			if !strings.HasPrefix(*p.Deadline, deadline.Format("2006-01-02")) {
				t.Fatalf("deadline = %q, want prefix %q", *p.Deadline, deadline.Format("2006-01-02"))
			}
			withDeadline = true
		}
	}
	if !checked {
		t.Fatalf("seeded project %d not in list: %s", f.projectA.ID, raw)
	}
	_ = withDeadline

	// A project WITHOUT a deadline must still carry the key
	// (empty), so a model reads "no deadline set" instead of inferring the
	// capability doesn't exist. Assert on the raw wire payload — the struct
	// decode above can't distinguish absent from empty.
	noDeadline := testutil.SeedProject(t, f.q, f.tenantA.ID, "No Deadline", "en", []string{"de"})
	res, err := s.CallTool(ctx, &sdk.CallToolParams{Name: "openloc_list_projects", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	wire, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var generic struct {
		Projects []map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(wire, &generic); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}
	var sawNoDeadline bool
	for _, p := range generic.Projects {
		if _, present := p["deadline"]; !present {
			t.Fatalf("deadline key absent on project %v — omitempty regression", p["id"])
		}
		if p["id"] == float64(noDeadline.ID) {
			sawNoDeadline = true
		}
	}
	if !sawNoDeadline {
		t.Fatalf("project %d without a deadline not in list: %s", noDeadline.ID, wire)
	}
}

// list_customers lists the customers TMs are tagged with (a project's customer
// is its Customer label pick; see project_list_tools_integration_test.go).
func TestIntegrationMCPListCustomers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	cust, err := f.q.CreateCustomer(ctx, db.CreateCustomerParams{
		TenantID: f.tenantA.ID, Name: "Fake Customer",
	})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}

	s := connect(t, f.url, f.tokenA)

	t.Run("list_customers", func(t *testing.T) {
		var out struct {
			Customers []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"customers"`
		}
		if isErr, text := callTool(t, s, "openloc_list_customers", nil, &out); isErr {
			t.Fatalf("unexpected error: %s", text)
		}
		var found bool
		for _, c := range out.Customers {
			if c.Name == "Fake Customer" && c.ID == cust.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("customer missing from list_customers: %+v", out.Customers)
		}
	})

	t.Run("customers do not leak across tenants", func(t *testing.T) {
		bSession := connect(t, f.url, f.tokenB)
		var out struct {
			Customers []struct {
				Name string `json:"name"`
			} `json:"customers"`
		}
		if isErr, text := callTool(t, bSession, "openloc_list_customers", nil, &out); isErr {
			t.Fatalf("unexpected error: %s", text)
		}
		for _, c := range out.Customers {
			if c.Name == "Fake Customer" {
				t.Fatal("tenant B saw tenant A's customer")
			}
		}
	})
}

// --- Payload/schema conformance ------------------------------------------------

// Every tool result must VALIDATE against the outputSchema the server advertises.
//
// Real clients validate the structured content against our advertised schema
// and reject a mismatch outright:
//
//	Structured content does not match the tool's output schema:
//	data must NOT have additional properties
//
// The Go SDK's client does NOT perform this check, so asserting "the call
// succeeded" is not enough. This test pulls the
// schema from tools/list over the real protocol, calls each tool, and validates
// the structured content against it — the same contract a real client enforces.
func TestIntegrationMCPResultsValidateAgainstAdvertisedSchema(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Give the fixture enough shape that optional fields are actually populated;
	// a zero-valued result can satisfy a schema that a real one would violate.
	cust, err := f.q.CreateCustomer(ctx, db.CreateCustomerParams{
		TenantID: f.tenantA.ID, Name: "Schema Customer",
	})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, err := f.q.UpdateProjectMetadata(ctx, db.UpdateProjectMetadataParams{
		ID: f.projectA.ID, TenantID: f.tenantA.ID, CustomerID: &cust.ID,
		Deadline: pgtype.Timestamptz{Time: time.Now().Add(72 * time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("attach customer: %v", err)
	}
	assignmentID := testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID, &f.linguistA.ID)
	if err := f.q.UpdateStepAssignmentDeadline(ctx, db.UpdateStepAssignmentDeadlineParams{
		ID:       assignmentID,
		Deadline: pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("set step deadline: %v", err)
	}

	token := testutil.SeedAPIToken(t, f.q, f.tenantA.ID, f.ownerA.ID, auth.CapabilityWrite, time.Hour)
	s := connect(t, f.url, token)

	// Schemas exactly as advertised to clients.
	listed, err := s.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	schemas := map[string]*jsonschema.Schema{}
	for _, tool := range listed.Tools {
		if tool.OutputSchema == nil {
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal schema for %s: %v", tool.Name, err)
		}
		var sch jsonschema.Schema
		if err := json.Unmarshal(raw, &sch); err != nil {
			t.Fatalf("parse schema for %s: %v", tool.Name, err)
		}
		schemas[tool.Name] = &sch
	}

	translationID, _ := firstTranslation(t, s, f.jobA.ID)

	calls := []struct {
		tool string
		args map[string]any
	}{
		{"openloc_list_projects", nil},
		{"openloc_get_project", map[string]any{"project_id": f.projectA.ID}},
		{"openloc_list_customers", nil},
		{"openloc_list_files", map[string]any{"project_id": f.projectA.ID}},
		{"openloc_list_members", nil},
		{"openloc_list_jobs", map[string]any{"project_id": f.projectA.ID}},
		{"openloc_get_job", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_get_job_leverage", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_get_qa_report", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_list_segments", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_list_my_tasks", nil},
		{"openloc_search_tm", map[string]any{
			"project_id": f.projectA.ID, "query": "hello", "target_language": "de"}},
		{"openloc_lookup_term", map[string]any{
			"project_id": f.projectA.ID, "source_text": "Hello world", "target_language": "de"}},
		{"openloc_add_comment", map[string]any{
			"translation_id": translationID, "content": "schema check"}},
		{"openloc_save_draft", map[string]any{
			"translation_id": translationID, "text": "Hallo"}},
		{"openloc_lock_segment", map[string]any{"translation_id": translationID}},
	}

	for _, c := range calls {
		t.Run(c.tool, func(t *testing.T) {
			schema, ok := schemas[c.tool]
			if !ok {
				t.Fatalf("%s advertises no outputSchema", c.tool)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatalf("resolve schema: %v", err)
			}
			res, err := s.CallTool(ctx, &sdk.CallToolParams{Name: c.tool, Arguments: c.args})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if res.IsError {
				var text string
				for _, ct := range res.Content {
					if tc, ok := ct.(*sdk.TextContent); ok {
						text += tc.Text
					}
				}
				t.Fatalf("tool returned an error: %s", text)
			}
			if res.StructuredContent == nil {
				t.Fatal("no structured content")
			}
			// Round-trip through JSON so we validate exactly the bytes a client
			// would see, not the in-process Go value.
			raw, err := json.Marshal(res.StructuredContent)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			var generic any
			if err := json.Unmarshal(raw, &generic); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}
			if err := resolved.Validate(generic); err != nil {
				t.Errorf("result does NOT satisfy the advertised outputSchema: %v\npayload: %s", err, raw)
			}
		})
	}
}

// --- Field presence + forward compatibility ---------------------------

// Semantically meaningful fields must ALWAYS be present, even when empty.
//
// `omitempty` drops the key entirely when a value is unset, and to a model an
// absent key is indistinguishable from "this system has no such concept": if no
// job has a step deadline, `deadline` never appears in any payload and a model
// concludes jobs have no deadlines at all.
//
// A human reading a UI sees an empty column. A model sees nothing at all.
func TestIntegrationMCPUnsetFieldsAreStillPresent(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	// jobA deliberately has NO step assignment and its project has NO deadline —
	// the exact shape that made the feature look missing.
	t.Run("list_jobs emits deadline even when unset", func(t *testing.T) {
		res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      "openloc_list_jobs",
			Arguments: map[string]any{"project_id": f.projectA.ID},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var out struct {
			Jobs []map[string]json.RawMessage `json:"jobs"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(out.Jobs) == 0 {
			t.Fatal("no jobs returned")
		}
		if _, ok := out.Jobs[0]["deadline"]; !ok {
			t.Errorf("`deadline` key absent for a job with no deadline — a model reads "+
				"that as \"jobs have no deadlines\". Payload: %s", raw)
		}
	})

	t.Run("get_job emits both deadline fields even when unset", func(t *testing.T) {
		res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      "openloc_get_job",
			Arguments: map[string]any{"job_id": f.jobA.ID},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("get_job errored: %+v", res.Content)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var out map[string]json.RawMessage
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, key := range []string{"deadline", "project_deadline"} {
			if _, ok := out[key]; !ok {
				t.Errorf("`%s` key absent when unset. Payload: %s", key, raw)
			}
		}
	})

	t.Run("list_projects emits customer_name even when unset", func(t *testing.T) {
		res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
			Name: "openloc_list_projects",
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var out struct {
			Projects []map[string]json.RawMessage `json:"projects"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(out.Projects) == 0 {
			t.Fatal("no projects returned")
		}
		if _, ok := out.Projects[0]["customer_name"]; !ok {
			t.Errorf("`customer_name` key absent for a project with no customer. Payload: %s", raw)
		}
	})
}

// Output schemas must NOT be closed objects.
//
// `additionalProperties: false` (which Go-struct inference emits by default)
// makes any added field a breaking change for clients holding a cached
// tools/list: they validate the new payload against the old schema and reject
// the entire response.
//
// Input schemas are intentionally exempt — there, rejecting an unknown argument
// catches a model's typo and is worth keeping.
func TestIntegrationMCPOutputSchemasAreForwardCompatible(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.OutputSchema == nil {
			t.Errorf("%s has no outputSchema", tool.Name)
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal %s: %v", tool.Name, err)
		}
		// A literal `false` closes the object; nested occurrences matter too,
		// because a field added to a nested type breaks clients identically.
		if strings.Contains(string(raw), `"additionalProperties":false`) {
			t.Errorf("%s advertises a CLOSED output schema — adding any field later "+
				"will break clients with a cached schema. Schema: %s", tool.Name, raw)
		}
	}
}

// Every tool must carry a non-empty annotations title — MCP connector
// directories flag untitled tools (addTool also panics at registration; this
// pins the requirement on the WIRE, where reviewers see it).
func TestIntegrationMCPEveryToolHasTitle(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	// Guard against the vacuous pass: an empty list satisfies the range below
	// while meaning tool registration is entirely broken.
	if len(res.Tools) == 0 {
		t.Fatal("ListTools returned no tools — registration is broken")
	}
	for _, tool := range res.Tools {
		if tool.Annotations == nil || tool.Annotations.Title == "" {
			t.Errorf("tool %q has no annotations title (directory requirement)", tool.Name)
		}
	}
}

// openloc_search_tm scopes by the project's settings row; when that row
// follows a TM profile the hits must come from the profile's TMs.
func TestIntegrationMCPSearchTMLinkedProfile(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	profTM := testutil.SeedTM(t, f.q, f.tenantA.ID, "Profile TM")
	tp := testutil.SeedTMProfile(t, f.q, f.tenantA.ID, "Shared", 0.75)
	if _, err := f.q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: tp.SettingsProfileID, TmID: profTM.ID, Mode: "read", Priority: 0,
	}); err != nil {
		t.Fatalf("assign profile TM: %v", err)
	}
	if _, err := f.q.InsertTM(ctx, db.InsertTMParams{
		TmID: profTM.ID, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Hello profile", TargetText: "Hallo Profil", CreatedBy: "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}
	testutil.LinkTMProfile(t, f.q, f.projectA.SettingsProfileID, tp.ID)

	s := connect(t, f.url, f.tokenA)
	var out struct {
		Matches []map[string]any `json:"matches"`
	}
	isErr, text := callTool(t, s, "openloc_search_tm", map[string]any{
		"project_id": f.projectA.ID, "query": "profile", "target_language": "de",
	}, &out)
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if len(out.Matches) != 1 || out.Matches[0]["target_text"] != "Hallo Profil" {
		t.Fatalf("want the profile TM's hit, got %+v", out.Matches)
	}
}
