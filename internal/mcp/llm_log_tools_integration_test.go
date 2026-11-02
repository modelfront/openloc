package mcp_test

import (
	"context"
	"slices"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/issues"
	"openloc/internal/llmdebug"
	"openloc/internal/mcp"
	"openloc/internal/testutil"
)

type llmCallsOut struct {
	Calls []struct {
		JobID    int64  `json:"job_id"`
		FileID   int64  `json:"file_id"`
		User     string `json:"user"`
		Response string `json:"response"`
		Request  string `json:"request_body"`
	} `json:"calls"`
	Groups []struct {
		JobID  int64 `json:"job_id"`
		FileID int64 `json:"file_id"`
		Calls  int   `json:"calls"`
		Errors int   `json:"errors"`
	} `json:"groups"`
	Count      int    `json:"count"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
	Oldest     string `json:"oldest"`
	Partial    bool   `json:"partial"`
}

func (o llmCallsOut) users() []string {
	var out []string
	for _, c := range o.Calls {
		out = append(out, c.User)
	}
	slices.Sort(out)
	return out
}

// llmWorld is the support world with an LLM call log holding two calls for
// one org A job and one for a second org A job in its own project, one for a
// job of an org A project that was deleted, one for org B's job and one
// extension call with no file.
type llmWorld struct {
	*supportWorld
	ring                    *llmdebug.Ring
	projA, projA2, projB    db.Project
	jobOfA, jobOfA2, jobOfB db.Job
}

func newLLMWorld(t *testing.T) *llmWorld {
	t.Helper()
	w := &llmWorld{supportWorld: newSupportWorld(t), ring: llmdebug.NewRing(50)}
	w.projA = testutil.SeedProject(t, w.q, w.tenantA.ID, "Alpha Project", "en", []string{"de"})
	w.projA2 = testutil.SeedProject(t, w.q, w.tenantA.ID, "Alpha Two", "en", []string{"de"})
	projGone := testutil.SeedProject(t, w.q, w.tenantA.ID, "Alpha Gone", "en", []string{"de"})
	w.projB = testutil.SeedProject(t, w.q, w.bTenant.ID, "Bravo Project", "en", []string{"fr"})
	w.jobOfA = seedJobWithSegments(t, w.q, w.projA.ID, "alpha.txt", "de", []string{"Hello"})
	w.jobOfA2 = seedJobWithSegments(t, w.q, w.projA2.ID, "alpha2.txt", "de", []string{"Hello"})
	jobGone := seedJobWithSegments(t, w.q, projGone.ID, "gone.txt", "de", []string{"Hello"})
	w.jobOfB = seedJobWithSegments(t, w.q, w.projB.ID, "bravo.txt", "fr", []string{"Bonjour"})
	record := func(jobID, fileID int64, user string, status int) {
		ctx := llmdebug.WithMeta(context.Background(), llmdebug.Meta{JobID: jobID, FileID: fileID})
		w.ring.Record(ctx, llmdebug.Entry{Kind: llmdebug.KindMT, Provider: "openai", User: user, ResponseBody: "reply to " + user, Status: status})
	}
	record(w.jobOfA2.ID, w.jobOfA2.FileID, "A other", 200)
	record(jobGone.ID, jobGone.FileID, "A deleted", 200)
	record(w.jobOfA.ID, w.jobOfA.FileID, "A one", 200)
	record(w.jobOfB.ID, w.jobOfB.FileID, "B secret", 200)
	record(w.jobOfA.ID, w.jobOfA.FileID, "A two", 500)
	w.ring.Record(context.Background(), llmdebug.Entry{Kind: llmdebug.KindExtension, Provider: "ext:qe", RequestBody: "no file", Status: 200})
	if _, err := w.q.MarkProjectDeleted(context.Background(), db.MarkProjectDeletedParams{ID: projGone.ID, TenantID: w.tenantA.ID}); err != nil {
		t.Fatal(err)
	}
	return w
}

// server is an MCP server over the world's data, with the ring as its log when
// log is set.
func (w *llmWorld) server(t *testing.T, log bool) *mcp.Server {
	t.Helper()
	if !log {
		return w.serverOver(t, nil)
	}
	return w.serverOver(t, w.ring)
}

// serverOver is an MCP server over the world's data reading src as its log;
// nil means the server keeps none.
func (w *llmWorld) serverOver(t *testing.T, src llmdebug.Snapshotter) *mcp.Server {
	t.Helper()
	b := mcp.NewDirectBackend(w.q)
	mcp.WireIssues(b, issues.NewService(w.pool, file.NewMemoryStore(), nil, nil, []string{issueOperator}, ""))
	if src != nil {
		mcp.WireLLMLog(b, src)
	}
	srv, err := mcp.NewServer(mcp.Deps{Backend: b, Audit: w.q, Version: "test", IssueReports: true, LLMLog: src != nil})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// session opens the tier's server as the caller in claims.
func (w *llmWorld) session(t *testing.T, srv *mcp.Server, claims *auth.Claims, manager, operator bool) *sdk.ClientSession {
	t.Helper()
	cs, err := srv.ConnectTierForTest(auth.WithClaims(context.Background(), claims), manager, operator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func llmCalls(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any) llmCallsOut {
	t.Helper()
	var out llmCallsOut
	if isErr, text := callTool(t, s, tool, args, &out); isErr {
		t.Fatalf("%s %v: %s", tool, args, text)
	}
	return out
}

// A manager reads only their own org's calls: never another org's, never a
// call without a file, and another org's job, file or project is not found.
func TestIntegrationMCPLLMCalls_OwnOrgOnly(t *testing.T) {
	w := newLLMWorld(t)
	srv := w.server(t, true)
	ownerA := &auth.Claims{TenantID: w.tenantA.ID, UserID: w.ownerA.ID, Role: "owner"}
	s := w.session(t, srv, ownerA, true, false)

	all := llmCalls(t, s, "openloc_list_llm_calls", map[string]any{})
	if got := all.users(); !slices.Equal(got, []string{"A one", "A other", "A two"}) {
		t.Errorf("own calls %v, want only org A's live projects'", got)
	}
	if all.Oldest == "" {
		t.Error("oldest not reported")
	}
	if c := all.Calls[0]; c.Response != "reply to A two" || c.JobID != w.jobOfA.ID || c.FileID != w.jobOfA.FileID {
		t.Errorf("newest call %+v", c)
	}

	for _, c := range []struct {
		args map[string]any
		want []string
	}{
		{map[string]any{"job_id": w.jobOfA.ID}, []string{"A one", "A two"}},
		{map[string]any{"file_id": w.jobOfA.FileID}, []string{"A one", "A two"}},
		{map[string]any{"project_id": w.projA.ID}, []string{"A one", "A two"}},
		{map[string]any{"job_id": w.jobOfA2.ID}, []string{"A other"}},
		{map[string]any{"file_id": w.jobOfA2.FileID}, []string{"A other"}},
		{map[string]any{"project_id": w.projA2.ID}, []string{"A other"}},
	} {
		if got := llmCalls(t, s, "openloc_list_llm_calls", c.args).users(); !slices.Equal(got, c.want) {
			t.Errorf("%v: %v, want %v", c.args, got, c.want)
		}
	}

	groups := llmCalls(t, s, "openloc_list_llm_calls", map[string]any{"group_by": "job"})
	if len(groups.Groups) != 2 || groups.Groups[0].JobID != w.jobOfA.ID || groups.Groups[0].Calls != 2 || groups.Groups[0].Errors != 1 ||
		groups.Groups[1].JobID != w.jobOfA2.ID || groups.Groups[1].Calls != 1 {
		t.Errorf("groups %+v", groups.Groups)
	}

	page := llmCalls(t, s, "openloc_list_llm_calls", map[string]any{"job_id": w.jobOfA.ID, "limit": 1})
	if !page.HasMore || page.users()[0] != "A two" {
		t.Fatalf("first page %+v", page)
	}
	next := llmCalls(t, s, "openloc_list_llm_calls", map[string]any{"job_id": w.jobOfA.ID, "limit": 1, "cursor": page.NextCursor})
	if next.HasMore || len(next.Calls) != 1 || next.Calls[0].User != "A one" {
		t.Errorf("second page %+v", next)
	}

	for _, args := range []map[string]any{
		{"job_id": w.jobOfB.ID}, {"file_id": w.jobOfB.FileID}, {"project_id": w.projB.ID},
	} {
		isErr, text := callTool(t, s, "openloc_list_llm_calls", args, nil)
		if !isErr {
			t.Errorf("%v of org B answered for org A", args)
			continue
		}
		assertOpaqueNotFound(t, text)
	}
}

// The support team reads every org's calls, including those with no file; a
// member cannot run the support tool even on a server that lists it.
func TestIntegrationMCPLLMCalls_SupportTeamReadsEveryOrg(t *testing.T) {
	w := newLLMWorld(t)
	srv := w.server(t, true)
	op := w.session(t, srv, &auth.Claims{TenantID: w.opsOrg.ID, UserID: w.op.UserID, Role: "owner"}, true, true)

	all := llmCalls(t, op, "openloc_support_list_llm_calls", map[string]any{})
	if all.Count != 6 {
		t.Errorf("support team sees %d calls, want 6", all.Count)
	}
	ext := llmCalls(t, op, "openloc_support_list_llm_calls", map[string]any{"kind": "extension"})
	if len(ext.Calls) != 1 || ext.Calls[0].Request != "no file" {
		t.Errorf("extension call %+v", ext.Calls)
	}
	if got := llmCalls(t, op, "openloc_support_list_llm_calls", map[string]any{"project_id": w.projB.ID}).users(); !slices.Equal(got, []string{"B secret"}) {
		t.Errorf("project B: %v", got)
	}

	member := w.session(t, srv, &auth.Claims{TenantID: w.tenantA.ID, UserID: w.ownerA.ID, Role: "owner"}, true, true)
	if !refused(t, member, "openloc_support_list_llm_calls", map[string]any{}) {
		t.Error("an owner off the support team read every org's calls")
	}
}

// The org tool is on the manager tier, in /mcp and the built-in assistant; the
// support tool only on the operator tiers, never in the assistant; neither
// without a log.
func TestIntegrationMCPLLMCalls_Listing(t *testing.T) {
	w := newLLMWorld(t)
	srv := w.server(t, true)
	ctx := context.Background()
	has := func(names []string, tool string) bool { return slices.Contains(names, tool) }

	ownerA := &auth.Claims{TenantID: w.tenantA.ID, UserID: w.ownerA.ID, Role: "owner"}
	if names := listToolNames(t, w.session(t, srv, ownerA, true, false)); !has(names, "openloc_list_llm_calls") || has(names, "openloc_support_list_llm_calls") {
		t.Errorf("manager tier: %v", names)
	}
	ling := &auth.Claims{TenantID: w.tenantA.ID, UserID: w.linguistA.ID, Role: "linguist"}
	if names := listToolNames(t, w.session(t, srv, ling, false, false)); has(names, "openloc_list_llm_calls") {
		t.Error("linguist tier lists the org's LLM calls")
	}
	opClaims := &auth.Claims{TenantID: w.opsOrg.ID, UserID: w.op.UserID, Role: "owner"}
	if names := listToolNames(t, w.session(t, srv, opClaims, false, true)); !has(names, "openloc_support_list_llm_calls") {
		t.Error("operator linguist tier lacks the support tool")
	}

	cs, err := srv.ConnectInProcess(auth.WithClaims(ctx, opClaims), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if names := listToolNames(t, cs); !has(names, "openloc_list_llm_calls") || has(names, "openloc_support_list_llm_calls") {
		t.Errorf("built-in assistant: %v", names)
	}

	if names := listToolNames(t, w.session(t, w.server(t, false), opClaims, true, true)); has(names, "openloc_list_llm_calls") || has(names, "openloc_support_list_llm_calls") {
		t.Error("log tools listed with no log")
	}
}

// cutShort is a log whose read the caller's deadline cut short.
type cutShort struct{ *llmdebug.Ring }

func (c cutShort) Snapshot(ctx context.Context) ([]llmdebug.Entry, bool) {
	entries, _ := c.Ring.Snapshot(ctx)
	return entries, false
}

// A log read in full says so; a read cut short says partial, so a missing
// call is not taken for one that was never made.
func TestIntegrationMCPLLMCalls_SaysWhenTheLogWasReadOnlyInPart(t *testing.T) {
	w := newLLMWorld(t)
	ownerA := &auth.Claims{TenantID: w.tenantA.ID, UserID: w.ownerA.ID, Role: "owner"}
	opClaims := &auth.Claims{TenantID: w.opsOrg.ID, UserID: w.op.UserID, Role: "owner"}

	full := w.server(t, true)
	if out := llmCalls(t, w.session(t, full, ownerA, true, false), "openloc_list_llm_calls", map[string]any{}); out.Partial {
		t.Error("a full read reported partial")
	}

	short := w.serverOver(t, cutShort{w.ring})
	if out := llmCalls(t, w.session(t, short, ownerA, true, false), "openloc_list_llm_calls", map[string]any{}); !out.Partial || len(out.Calls) != 3 {
		t.Errorf("cut-short read: partial %v, %d calls", out.Partial, len(out.Calls))
	}
	if out := llmCalls(t, w.session(t, short, opClaims, true, true), "openloc_support_list_llm_calls", map[string]any{"group_by": "job"}); !out.Partial {
		t.Error("cut-short grouped read by the support team did not report partial")
	}
}
