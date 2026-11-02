package mcp_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/issues"
	"openloc/internal/mcp"
	"openloc/internal/testutil"
)

var supportIssueTools = []string{
	"openloc_support_list_issues", "openloc_support_get_issue", "openloc_support_set_issue_status",
	"openloc_support_reply_to_issue", "openloc_support_add_issue_note", "openloc_support_update_issue",
	"openloc_support_list_team",
}

// supportWorld is an issue world with keys for the operator: a write key and
// a read key on their own org.
type supportWorld struct {
	*issueWorld
	opToken, opReadToken string
}

func newSupportWorld(t *testing.T) *supportWorld {
	t.Helper()
	w := newIssueWorld(t)
	return &supportWorld{issueWorld: w,
		opToken:     writeToken(t, w.fixture, w.opsOrg.ID, w.op.UserID),
		opReadToken: testutil.SeedAPIToken(t, w.q, w.opsOrg.ID, w.op.UserID, auth.CapabilityRead, time.Hour)}
}

type supportListOut struct {
	Issues []struct {
		ID         int64  `json:"id"`
		Title      string `json:"title"`
		OrgID      int64  `json:"org_id"`
		OrgName    string `json:"org_name"`
		ReporterID int64  `json:"reporter_id"`
		Path       string `json:"path"`
	} `json:"issues"`
	Total   int64 `json:"total"`
	HasMore bool  `json:"has_more"`
}

type supportEntryOut struct {
	AuthorSide string  `json:"author_side"`
	Text       *string `json:"text"`
	Status     *string `json:"status"`
	Internal   bool    `json:"internal"`
}

type supportThreadOut struct {
	ID            int64             `json:"id"`
	Kind          string            `json:"kind"`
	Severity      *string           `json:"severity"`
	Status        string            `json:"status"`
	OrgName       string            `json:"org_name"`
	ReporterEmail string            `json:"reporter_email"`
	Path          string            `json:"path"`
	AssigneeID    *int64            `json:"assignee_id"`
	Priority      *string           `json:"priority"`
	Tags          []string          `json:"tags"`
	Entries       []supportEntryOut `json:"entries"`
	Duplicates    []struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	} `json:"duplicates"`
}

// supportList reads one page of the support queue.
func supportList(t *testing.T, s *sdk.ClientSession, args map[string]any) supportListOut {
	t.Helper()
	var out supportListOut
	if isErr, text := callTool(t, s, "openloc_support_list_issues", args, &out); isErr {
		t.Fatalf("support list %v: %s", args, text)
	}
	return out
}

func supportGet(t *testing.T, s *sdk.ClientSession, id int64) (supportThreadOut, string) {
	t.Helper()
	var out supportThreadOut
	isErr, text := callTool(t, s, "openloc_support_get_issue", map[string]any{"issue_id": id}, &out)
	if isErr {
		t.Fatalf("support get %d: %s", id, text)
	}
	return out, text
}

func (o supportListOut) titles() []string {
	titles := make([]string, 0, len(o.Issues))
	for _, i := range o.Issues {
		titles = append(titles, i.Title)
	}
	slices.Sort(titles)
	return titles
}

// refused reports whether a call fails, as a tool error or, for a tool the
// caller's server does not list, a protocol error.
func refused(t *testing.T, s *sdk.ClientSession, name string, args map[string]any) bool {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	return err != nil || res.IsError
}

// supportServer is an MCP server over the issue world's data whose issue
// service reads from pool, with or without issue reports.
func supportServer(t *testing.T, w *supportWorld, pool *pgxpool.Pool, reports bool) *mcp.Server {
	t.Helper()
	b := mcp.NewDirectBackend(w.q)
	mcp.WireIssues(b, issues.NewService(pool, file.NewMemoryStore(), nil, nil, []string{issueOperator}, ""))
	srv, err := mcp.NewServer(mcp.Deps{Backend: b, Audit: w.q, Version: "test", IssueReports: reports})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// auditRow is the first audit row of a tenant for one tool, polled because
// the audit write is fire-and-forget.
func auditRow(t *testing.T, w *supportWorld, tenantID int64, tool string) db.ListMCPToolCallsRow {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := w.q.ListMCPToolCalls(context.Background(), db.ListMCPToolCallsParams{TenantID: tenantID, Cursor: 0, RowLimit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.Tool == tool {
				return r
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no audit row for %s in tenant %d", tool, tenantID)
	return db.ListMCPToolCallsRow{}
}

// The support tools are listed for a platform operator over /mcp only, on
// both role tiers: never for a member, an operator viewing an organization or
// in the demo organization, a deployment without reports, a failed account
// lookup, or the built-in chat; a member cannot call them by name. The reads
// work with read access, the changes do not, and what emails the reporter is
// marked as reaching outside.
func TestIntegrationMCPSupportIssues_ListedForTheSupportTeamOnly(t *testing.T) {
	w := newSupportWorld(t)
	member := connect(t, w.url, writeToken(t, w.fixture, w.tenantA.ID, w.ownerA.ID))
	if names := listToolNames(t, member); slices.ContainsFunc(supportIssueTools, func(n string) bool { return slices.Contains(names, n) }) {
		t.Error("support tools listed for an owner who is not an operator")
	}
	if !refused(t, member, "openloc_support_list_issues", map[string]any{}) ||
		!refused(t, member, "openloc_support_set_issue_status", map[string]any{"issue_id": w.own.ID, "status": "wont_fix"}) {
		t.Error("a member ran a support tool by name")
	}

	want := map[string]struct{ readOnly, openWorld bool }{
		"openloc_support_list_issues": {true, false}, "openloc_support_get_issue": {true, false},
		"openloc_support_list_team": {true, false}, "openloc_support_set_issue_status": {false, true},
		"openloc_support_reply_to_issue": {false, true}, "openloc_support_add_issue_note": {false, false},
		"openloc_support_update_issue": {false, false},
	}
	res, err := connect(t, w.url, w.opToken).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, tool := range res.Tools {
		c, ok := want[tool.Name]
		if !ok {
			continue
		}
		seen++
		if tool.Annotations.ReadOnlyHint != c.readOnly || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint != c.openWorld {
			t.Errorf("%s: read-only %v, open world %v", tool.Name, tool.Annotations.ReadOnlyHint, tool.Annotations.OpenWorldHint)
		}
	}
	if seen != len(want) {
		t.Errorf("%d of the %d support tools listed for the operator", seen, len(want))
	}

	// The tier follows the claims and the deployment.
	ctx := context.Background()
	srv := supportServer(t, w, w.pool, true)
	opClaims := &auth.Claims{TenantID: w.opsOrg.ID, UserID: w.op.UserID, Role: "owner"}
	for name, c := range map[string]struct {
		srv      *mcp.Server
		claims   *auth.Claims
		operator bool
	}{
		"the operator":                 {srv, opClaims, true},
		"the operator as a linguist":   {srv, &auth.Claims{TenantID: w.opsOrg.ID, UserID: w.op.UserID, Role: "linguist"}, true},
		"the operator viewing an org":  {srv, &auth.Claims{TenantID: w.tenantA.ID, UserID: w.op.UserID, Role: "owner", Impersonated: true}, false},
		"the operator in the demo org": {srv, &auth.Claims{TenantID: w.tenantA.ID, UserID: w.op.UserID, Role: "owner", Demo: true}, false},
		"an owner":                     {srv, &auth.Claims{TenantID: w.tenantA.ID, UserID: w.ownerA.ID, Role: "owner"}, false},
		"no reports":                   {supportServer(t, w, w.pool, false), opClaims, false},
		"a failed account lookup":      {supportServer(t, w, testutil.FailingPool(t, "GetUser"), true), opClaims, false},
	} {
		manager, operator := c.srv.TierForTest(auth.WithClaims(ctx, c.claims), c.claims)
		if operator != c.operator || manager != (c.claims.Role != "linguist") {
			t.Errorf("%s: tier manager %v, operator %v; want operator %v", name, manager, operator, c.operator)
		}
	}

	// The built-in chat never lists them, even for the operator.
	cs, err := srv.ConnectInProcess(auth.WithClaims(ctx, opClaims), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	names := listToolNames(t, cs)
	if slices.Contains(names, "openloc_support_list_issues") || !slices.Contains(names, "openloc_list_projects") {
		t.Errorf("the chat lists the support tools, or not the manager tools: %v", names)
	}
}

// The call-time gate stands on its own: on a server that lists the support
// tools, a caller off the team is refused and the denial is audited, a failed
// account lookup is an error (not a denial), and the operator gets through.
func TestIntegrationMCPSupportIssues_CallTimeGate(t *testing.T) {
	w := newSupportWorld(t)
	ctx := context.Background()
	srv := supportServer(t, w, w.pool, true)
	opClaims := &auth.Claims{TenantID: w.opsOrg.ID, UserID: w.op.UserID, Role: "owner"}

	member, err := srv.ConnectTierForTest(auth.WithClaims(ctx, &auth.Claims{TenantID: w.tenantA.ID, UserID: w.ownerA.ID, Role: "owner"}), true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer member.Close()
	if isErr, text := callInProcess(t, member, "openloc_support_set_issue_status", map[string]any{"issue_id": w.otherIss.ID, "status": "wont_fix"}); !isErr ||
		!strings.Contains(text, "only the support team") {
		t.Errorf("a member on the operator tier: %v %s", isErr, text)
	}
	if row := auditRow(t, w, w.tenantA.ID, "openloc_support_set_issue_status"); row.Outcome != "denied" {
		t.Errorf("audit outcome %q, want denied", row.Outcome)
	}
	if d, err := w.svc.PlatformGet(ctx, w.otherIss.ID); err != nil || d.Issue.Status != issues.StatusOpen {
		t.Errorf("a member changed the issue: %+v %v", d.Issue, err)
	}

	broken, err := supportServer(t, w, testutil.FailingPool(t, "GetUser"), true).ConnectTierForTest(auth.WithClaims(ctx, opClaims), true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	if isErr, text := callInProcess(t, broken, "openloc_support_list_team", map[string]any{}); !isErr ||
		!strings.Contains(text, "the error has been logged") || strings.Contains(text, "support team can") {
		t.Errorf("a failed lookup: %v %s", isErr, text)
	}
	if row := auditRow(t, w, w.opsOrg.ID, "openloc_support_list_team"); row.Outcome != "error" {
		t.Errorf("audit outcome %q, want error", row.Outcome)
	}

	op, err := srv.ConnectTierForTest(auth.WithClaims(ctx, opClaims), true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	if isErr, text := callInProcess(t, op, "openloc_support_list_team", map[string]any{}); isErr || !strings.Contains(text, issueOperator) {
		t.Errorf("the operator: %v %s", isErr, text)
	}
}

// The operator lists every organization's issues with the queue's filters
// and reads any of them with the internal notes and the duplicates; the
// member tools still show them only their own organization's issues.
func TestIntegrationMCPSupportIssues_ListAndGet(t *testing.T) {
	w := newSupportWorld(t)
	ctx := context.Background()
	// One more report, the one with the assistant's check; then the
	// linguist's bug becomes the one issue with an assignee, a priority, a
	// tag and the area tm, and the last one touched.
	linguist := issues.Member{TenantID: w.tenantA.ID, UserID: w.linguistA.ID, Role: "linguist"}
	check := issues.Verification{Result: issues.VerificationVerified, Note: "Seen in the data."}
	token, err := w.svc.SignVerification(linguist, check)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Create(ctx, linguist, issues.NewIssue{Kind: "bug", Severity: "major", Area: "editor", Title: "Verified bug",
		Description: "Seeded for MCP.", Via: issues.ViaAssistant, Verification: check, VerificationToken: token}); err != nil {
		t.Fatal(err)
	}
	high, tm := "high", "tm"
	if _, err := w.svc.PlatformUpdate(ctx, w.op, w.own.ID, issues.IssueUpdate{
		Area: &tm, Priority: &high, Tags: &[]string{"mvp"}, AssigneeSet: true, AssigneeID: &w.op.UserID}); err != nil {
		t.Fatal(err)
	}
	op := connect(t, w.url, w.opReadToken)
	all := []string{"Linguist bug", "Other org secret title", "Owner enhancement", "Peer request", "Verified bug"}
	others := []string{"Other org secret title", "Owner enhancement", "Peer request", "Verified bug"}
	for name, c := range map[string]struct {
		args map[string]any
		want []string
		// ordered compares the page's first row (under limit 1); the
		// others compare the set and the total.
		ordered bool
	}{
		"every org":               {nil, all, false},
		"one org":                 {map[string]any{"org_ids": []int64{w.bTenant.ID}}, []string{"Other org secret title"}, false},
		"one reporter":            {map[string]any{"reporter_ids": []int64{w.peer.ID}}, []string{"Peer request"}, false},
		"one org and a reporter":  {map[string]any{"org_ids": []int64{w.bTenant.ID}, "reporter_ids": []int64{w.peer.ID}}, []string{"Other org secret title", "Peer request"}, false},
		"unassigned":              {map[string]any{"unassigned": true}, others, false},
		"by assignee":             {map[string]any{"assignee_ids": []int64{w.op.UserID}}, []string{"Linguist bug"}, false},
		"assignee or unassigned":  {map[string]any{"assignee_ids": []int64{w.op.UserID}, "unassigned": true}, all, false},
		"by status":               {map[string]any{"statuses": []string{"duplicate"}}, []string{"Owner enhancement"}, false},
		"by kind":                 {map[string]any{"kinds": []string{"bug"}}, []string{"Linguist bug", "Other org secret title", "Verified bug"}, false},
		"by severity":             {map[string]any{"severities": []string{"minor"}}, []string{"Other org secret title"}, false},
		"by area":                 {map[string]any{"areas": []string{"tm"}}, []string{"Linguist bug"}, false},
		"by priority":             {map[string]any{"priorities": []string{"high"}}, []string{"Linguist bug"}, false},
		"by tag":                  {map[string]any{"tags": []string{"mvp", "other"}}, []string{"Linguist bug"}, false},
		"by verification":         {map[string]any{"verifications": []string{"verified"}}, []string{"Verified bug"}, false},
		"a search":                {map[string]any{"query": "secret"}, []string{"Other org secret title"}, false},
		"sorted by priority":      {map[string]any{"sort": "priority", "limit": 1}, []string{"Linguist bug"}, true},
		"sorted by last touched":  {map[string]any{"sort": "-updated", "limit": 1}, []string{"Linguist bug"}, true},
		"sorted by first touched": {map[string]any{"sort": "updated,-assignee", "limit": 1}, []string{"Other org secret title"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			got := supportList(t, op, c.args)
			if c.ordered {
				if len(got.Issues) != 1 || got.Issues[0].Title != c.want[0] || !got.HasMore {
					t.Errorf("first row %+v (more %v), want %v", got.Issues, got.HasMore, c.want)
				}
				return
			}
			if !slices.Equal(got.titles(), c.want) {
				t.Errorf("titles %v, want %v", got.titles(), c.want)
			}
			if got.Total != int64(len(c.want)) {
				t.Errorf("total %d, want %d", got.Total, len(c.want))
			}
		})
	}
	// A second key for the rest: the /mcp limiter allows a burst of 20
	// requests per credential, and the session plus the table used them.
	op = connect(t, w.url, testutil.SeedAPIToken(t, w.q, w.opsOrg.ID, w.op.UserID, auth.CapabilityRead, time.Hour))
	page := supportList(t, op, map[string]any{"org_ids": []int64{w.bTenant.ID}})
	if i := page.Issues[0]; i.OrgID != w.bTenant.ID || i.OrgName != "McpIssuesB" || i.ReporterID != w.ownerB.ID ||
		i.Path != "/platform/issues/"+itoa(i.ID) {
		t.Errorf("row = %+v", i)
	}
	if isErr, _ := callTool(t, op, "openloc_support_list_issues", map[string]any{"priorities": []string{"urgent"}}, nil); !isErr {
		t.Error("an unknown priority was accepted")
	}

	own, text := supportGet(t, op, w.own.ID)
	if own.OrgName != "McpTenantA" || own.ReporterEmail != w.linguistA.Email || own.Path != "/platform/issues/"+itoa(w.own.ID) ||
		!strings.Contains(text, "Which file was it?") {
		t.Errorf("own = %+v", own)
	}
	if i := slices.IndexFunc(own.Entries, func(e supportEntryOut) bool {
		return e.Text != nil && *e.Text == "INTERNAL-ONLY note"
	}); i < 0 || !own.Entries[i].Internal || own.Entries[i].AuthorSide != "operator" {
		t.Errorf("the internal note is missing or not marked: %+v", own.Entries)
	}
	other, _ := supportGet(t, op, w.otherIss.ID)
	if len(other.Duplicates) != 1 || other.Duplicates[0].ID != w.ownerIss.ID || other.Duplicates[0].Title != "Owner enhancement" {
		t.Errorf("duplicates = %+v", other.Duplicates)
	}

	// The operator is a member of their own org for the member tools.
	if titles, total := listIssueTitles(t, op, nil); len(titles) != 0 || total != 0 {
		t.Errorf("the member list shows the operator %v", titles)
	}
	if !refused(t, op, "openloc_get_issue", map[string]any{"issue_id": w.own.ID}) {
		t.Error("the member read showed the operator another org's issue")
	}
}

// Status changes, replies and notes: a read key makes none; a status change
// and a reply reach the reporter's organization and email the reporter; a
// note stays with the team.
func TestIntegrationMCPSupportIssues_StatusAndReplies(t *testing.T) {
	w := newSupportWorld(t)
	ctx := context.Background()
	reader := connect(t, w.url, w.opReadToken)
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"openloc_support_set_issue_status", map[string]any{"issue_id": w.otherIss.ID, "status": "wont_fix"}},
		{"openloc_support_reply_to_issue", map[string]any{"issue_id": w.otherIss.ID, "text": "Hi"}},
		{"openloc_support_add_issue_note", map[string]any{"issue_id": w.otherIss.ID, "text": "Hi"}},
		{"openloc_support_update_issue", map[string]any{"issue_id": w.otherIss.ID, "priority": "high"}},
	} {
		if !refused(t, reader, c.tool, c.args) {
			t.Errorf("%s with a read key ran", c.tool)
		}
	}
	if d, err := w.svc.PlatformGet(ctx, w.otherIss.ID); err != nil || d.Issue.Status != issues.StatusOpen || len(d.Comments) != 0 {
		t.Fatalf("a read key changed the issue: %+v %v", d.Issue, err)
	}

	op := connect(t, w.url, w.opToken)
	for name, args := range map[string]map[string]any{
		"a duplicate without its original": {"issue_id": w.otherIss.ID, "status": "duplicate"},
		"an original on another status":    {"issue_id": w.otherIss.ID, "status": "wont_fix", "duplicate_of": w.own.ID},
		"a question without the question":  {"issue_id": w.otherIss.ID, "status": "needs_info"},
		"the status it has, with no note":  {"issue_id": w.otherIss.ID, "status": "open"},
		"an unknown status":                {"issue_id": w.otherIss.ID, "status": "closed"},
	} {
		if isErr, _ := callTool(t, op, "openloc_support_set_issue_status", args, nil); !isErr {
			t.Errorf("%s was accepted", name)
		}
	}

	before := len(w.outbox.For(w.ownerB.Email))
	var closed supportThreadOut
	if isErr, text := callTool(t, op, "openloc_support_set_issue_status",
		map[string]any{"issue_id": w.otherIss.ID, "status": "wont_fix", "note": "Works as designed."}, &closed); isErr {
		t.Fatalf("won't fix: %s", text)
	}
	last := closed.Entries[len(closed.Entries)-1]
	if closed.Status != issues.StatusWontFix || last.Status == nil || *last.Status != issues.StatusWontFix ||
		last.Text == nil || *last.Text != "Works as designed." || last.Internal {
		t.Errorf("closed = %+v", closed)
	}
	var replied supportThreadOut
	if isErr, text := callTool(t, op, "openloc_support_reply_to_issue",
		map[string]any{"issue_id": w.otherIss.ID, "text": "Tell us if that changes."}, &replied); isErr || replied.Status != issues.StatusWontFix {
		t.Errorf("reply: %s", text)
	}
	if isErr, text := callTool(t, op, "openloc_support_add_issue_note",
		map[string]any{"issue_id": w.otherIss.ID, "text": "INTERNAL-TWO"}, nil); isErr {
		t.Errorf("note: %s", text)
	}
	if got := len(w.outbox.For(w.ownerB.Email)) - before; got != 2 {
		t.Errorf("the reporter got %d emails, want 2 (the status change and the reply)", got)
	}

	// The reporter's org sees the status, the note to them and the reply,
	// never the internal note; the team sees it all.
	ownerB := connect(t, w.url, writeToken(t, w.fixture, w.bTenant.ID, w.ownerB.ID))
	var asReporter issueThreadOut
	isErr, text := callTool(t, ownerB, "openloc_get_issue", map[string]any{"issue_id": w.otherIss.ID}, &asReporter)
	if isErr || asReporter.Status != issues.StatusWontFix || !strings.Contains(text, "Works as designed.") ||
		!strings.Contains(text, "Tell us if that changes.") || strings.Contains(text, "INTERNAL-TWO") {
		t.Errorf("as the reporter: %s", text)
	}
	if _, text := supportGet(t, op, w.otherIss.ID); !strings.Contains(text, "INTERNAL-TWO") {
		t.Errorf("as the team: %s", text)
	}
}

// Updating the fields follows the app's panel: only a team member may be
// assigned, a kind change applies the severity rule, a priority is set or
// cleared, tags are replaced, and an empty update is refused.
func TestIntegrationMCPSupportIssues_Update(t *testing.T) {
	w := newSupportWorld(t)
	op := connect(t, w.url, w.opToken)
	var team struct {
		Members []struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
		} `json:"members"`
	}
	if isErr, text := callTool(t, op, "openloc_support_list_team", map[string]any{}, &team); isErr ||
		len(team.Members) != 1 || team.Members[0].ID != w.op.UserID || team.Members[0].Email != issueOperator {
		t.Errorf("team: %s", text)
	}

	id := w.own.ID
	for name, args := range map[string]map[string]any{
		"nothing":                 {"issue_id": id},
		"an outsider assigned":    {"issue_id": id, "assignee_email": w.ownerA.Email},
		"assign and unassign":     {"issue_id": id, "assignee_email": issueOperator, "unassign": true},
		"set and clear priority":  {"issue_id": id, "priority": "high", "clear_priority": true},
		"an unknown priority":     {"issue_id": id, "priority": "urgent"},
		"a severity on a request": {"issue_id": id, "kind": "feature_request", "severity": "major"},
	} {
		if isErr, _ := callTool(t, op, "openloc_support_update_issue", args, nil); !isErr {
			t.Errorf("%s was accepted", name)
		}
	}

	update := func(args map[string]any) supportThreadOut {
		t.Helper()
		args["issue_id"] = id
		var out supportThreadOut
		if isErr, text := callTool(t, op, "openloc_support_update_issue", args, &out); isErr {
			t.Fatalf("update %v: %s", args, text)
		}
		return out
	}
	got := update(map[string]any{"assignee_email": strings.ToUpper(issueOperator), "priority": "high", "tags": []string{"mvp", "blocks-testing"}})
	if got.AssigneeID == nil || *got.AssigneeID != w.op.UserID || got.Priority == nil || *got.Priority != "high" ||
		!slices.Equal(got.Tags, []string{"blocks-testing", "mvp"}) {
		t.Errorf("after assign: %+v", got)
	}
	if got = update(map[string]any{"kind": "enhancement"}); got.Kind != "enhancement" || got.Severity != nil {
		t.Errorf("after kind: %+v", got)
	}
	if got = update(map[string]any{"kind": "bug"}); got.Kind != "bug" || got.Severity == nil || *got.Severity != "major" {
		t.Errorf("after kind back to bug: %+v", got)
	}
	if got = update(map[string]any{"severity": "blocker", "area": "tm"}); got.Severity == nil || *got.Severity != "blocker" {
		t.Errorf("after severity: %+v", got)
	}
	if got = update(map[string]any{"unassign": true, "clear_priority": true, "tags": []string{}}); got.AssigneeID != nil || got.Priority != nil || len(got.Tags) != 0 {
		t.Errorf("after clearing: %+v", got)
	}

	// The reporter sees the assignment changes only: the team's kind,
	// severity, area, priority and tag changes are internal.
	var asReporter struct {
		Entries []struct {
			Change *struct {
				Field string `json:"field"`
			} `json:"change"`
		} `json:"entries"`
	}
	linguist := connect(t, w.url, w.tokenLingA)
	if isErr, text := callTool(t, linguist, "openloc_get_issue", map[string]any{"issue_id": id}, &asReporter); isErr {
		t.Fatalf("as the reporter: %s", text)
	}
	var fields []string
	for _, e := range asReporter.Entries {
		if e.Change != nil {
			fields = append(fields, e.Change.Field)
		}
	}
	slices.Sort(fields)
	if want := []string{"assignee", "assignee"}; !slices.Equal(fields, want) {
		t.Errorf("the reporter sees the changes %v, want %v", fields, want)
	}
}
