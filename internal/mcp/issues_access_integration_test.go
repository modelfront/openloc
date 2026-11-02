package mcp_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/issues"
	"openloc/internal/mcp"
	"openloc/internal/testutil"
)

var issueAccessTools = []string{
	"openloc_list_issues", "openloc_get_issue", "openloc_reply_to_issue",
	"openloc_confirm_issue_fix", "openloc_change_issue_kind",
}

// issueWorld is an issue fixture with issues in two orgs and the support
// team's side of their conversations.
type issueWorld struct {
	*fixture
	svc      *issues.Service
	op       issues.Operator
	opsOrg   db.Tenant // the operator's own org, with no issues
	peer     db.User   // a second linguist of org A
	ownerB   db.User
	bTenant  db.Tenant
	own      issues.IssueView // the linguist's bug, with an internal note and a visible reply
	peerIss  issues.IssueView // the peer's feature request, resolved
	ownerIss issues.IssueView // the owner's enhancement, a duplicate of org B's issue
	otherIss issues.IssueView // org B's issue
}

func newIssueWorld(t *testing.T) *issueWorld {
	t.Helper()
	ctx := context.Background()
	w := &issueWorld{fixture: newIssueFixture(t)}
	w.svc = issues.NewService(w.pool, file.NewMemoryStore(), nil, nil, []string{issueOperator}, "")
	w.opsOrg = testutil.SeedTenant(t, w.q, "McpOps")
	w.op = issues.Operator{UserID: testutil.SeedUserWithRole(t, w.q, w.opsOrg.ID, issueOperator, "pass", "owner").ID}
	w.peer = testutil.SeedUserWithRole(t, w.q, w.tenantA.ID, "mcp-peer@test.com", "pass", "linguist")
	w.bTenant = testutil.SeedTenant(t, w.q, "McpIssuesB")
	w.ownerB = testutil.SeedUserWithRole(t, w.q, w.bTenant.ID, "mcp-ib@test.com", "pass", "owner")

	file := func(u db.User, tenantID int64, role, kind, severity, title string) issues.IssueView {
		v, err := w.svc.Create(ctx, issues.Member{TenantID: tenantID, UserID: u.ID, Role: role},
			issues.NewIssue{Kind: kind, Severity: severity, Area: "editor", Title: title, Description: "Seeded for MCP."})
		if err != nil {
			t.Fatalf("file %s: %v", title, err)
		}
		return v
	}
	w.own = file(w.linguistA, w.tenantA.ID, "linguist", "bug", "major", "Linguist bug")
	w.peerIss = file(w.peer, w.tenantA.ID, "linguist", "feature_request", "", "Peer request")
	w.ownerIss = file(w.ownerA, w.tenantA.ID, "owner", "enhancement", "", "Owner enhancement")
	w.otherIss = file(w.ownerB, w.bTenant.ID, "owner", "bug", "minor", "Other org secret title")

	must := func(_ issues.PlatformIssueDetail, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(w.svc.PlatformNote(ctx, w.op, w.own.ID, issues.NewComment{Body: "INTERNAL-ONLY note"}))
	must(w.svc.PlatformComment(ctx, w.op, w.own.ID, issues.NewComment{Body: "Which file was it?"}))
	must(w.svc.PlatformSetStatus(ctx, w.op, w.peerIss.ID, issues.StatusChange{Status: issues.StatusResolved}))
	must(w.svc.PlatformSetStatus(ctx, w.op, w.ownerIss.ID, issues.StatusChange{Status: issues.StatusDuplicate, DuplicateOf: &w.otherIss.ID}))
	return w
}

type issueListOut struct {
	Issues []struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		DuplicateOf *struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"duplicate_of"`
	} `json:"issues"`
	Count      int    `json:"count"`
	Total      int64  `json:"total"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

type issueThreadOut struct {
	ID          int64   `json:"id"`
	Kind        string  `json:"kind"`
	Severity    *string `json:"severity"`
	Status      string  `json:"status"`
	ConfirmedAt *string `json:"confirmed_at"`
	DuplicateOf *struct {
		ID      int64  `json:"id"`
		Title   string `json:"title"`
		CanOpen bool   `json:"can_open"`
	} `json:"duplicate_of"`
	Entries []struct {
		AuthorSide string  `json:"author_side"`
		Text       *string `json:"text"`
	} `json:"entries"`
}

// listIssueTitles reads every page of openloc_list_issues, titles in the
// order the pages give them.
func listIssueTitles(t *testing.T, s *sdk.ClientSession, args map[string]any) (titles []string, total int64) {
	t.Helper()
	cursor := ""
	for range 20 {
		a := map[string]any{}
		for k, v := range args {
			a[k] = v
		}
		if cursor != "" {
			a["cursor"] = cursor
		}
		var page issueListOut
		if isErr, text := callTool(t, s, "openloc_list_issues", a, &page); isErr {
			t.Fatalf("list issues: %s", text)
		}
		for _, i := range page.Issues {
			titles = append(titles, i.Title)
		}
		if page.Count != len(page.Issues) || page.HasMore != (page.NextCursor != "") {
			t.Errorf("page counts: %+v", page)
		}
		if !page.HasMore {
			return titles, page.Total
		}
		cursor = page.NextCursor
	}
	t.Fatal("more than 20 pages")
	return nil, 0
}

// Each caller lists what the app's Reported issues page shows them: a manager
// the org's issues, a linguist their own; another org's never. Pages of one
// give the same list, and total counts it.
func TestIntegrationMCPIssues_ListFollowsTheApp(t *testing.T) {
	w := newIssueWorld(t)
	// Filed in the order Linguist bug, Peer request, Owner enhancement: the
	// default order is the newest report first. ordered cases compare the
	// order the pages give; the others only the issues. One session per
	// key keeps the calls under the /mcp rate limit.
	owner, linguist := connect(t, w.url, w.tokenA), connect(t, w.url, w.tokenLingA)
	for name, c := range map[string]struct {
		s       *sdk.ClientSession
		args    map[string]any
		want    []string
		ordered bool
	}{
		"owner, the org's, newest first": {owner, nil, []string{"Owner enhancement", "Peer request", "Linguist bug"}, true},
		"owner, by kind":                 {owner, map[string]any{"sort": "kind,-updated", "limit": 1}, []string{"Linguist bug", "Owner enhancement", "Peer request"}, true},
		"owner, by kind reversed":        {owner, map[string]any{"sort": "-kind"}, []string{"Peer request", "Owner enhancement", "Linguist bug"}, true},
		"linguist, their own":            {linguist, map[string]any{"limit": 1}, []string{"Linguist bug"}, false},
		"owner, by status":               {owner, map[string]any{"statuses": []string{"resolved"}}, []string{"Peer request"}, false},
		"owner, by kind filter":          {owner, map[string]any{"kinds": []string{"enhancement", "feature_request"}}, []string{"Owner enhancement", "Peer request"}, false},
		"owner, by severity":             {owner, map[string]any{"severities": []string{"major"}}, []string{"Linguist bug"}, false},
		"owner, by area":                 {owner, map[string]any{"areas": []string{"tm"}}, nil, false},
		"owner, a search":                {owner, map[string]any{"query": "secret"}, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, total := listIssueTitles(t, c.s, c.args)
			if !c.ordered {
				slices.Sort(got)
			}
			if !slices.Equal(got, c.want) || total != int64(len(c.want)) {
				t.Errorf("titles %v (total %d), want %v", got, total, c.want)
			}
		})
	}
	if isErr, _ := callTool(t, owner, "openloc_list_issues", map[string]any{"sort": "priority"}, nil); !isErr {
		t.Error("the support team's sort key is accepted")
	}
}

// One issue reads as the app shows it: never another org's, never a
// colleague's to a linguist, never the support team's internal notes, and
// another org's original of a duplicate without its title.
func TestIntegrationMCPIssues_GetFollowsTheApp(t *testing.T) {
	w := newIssueWorld(t)
	owner, linguist := connect(t, w.url, w.tokenA), connect(t, w.url, w.tokenLingA)
	for name, c := range map[string]struct {
		s  *sdk.ClientSession
		id int64
	}{"another org's issue": {owner, w.otherIss.ID}, "a colleague's, to a linguist": {linguist, w.peerIss.ID}} {
		if isErr, text := callTool(t, c.s, "openloc_get_issue", map[string]any{"issue_id": c.id}, nil); !isErr ||
			strings.Contains(text, "Other org secret title") || strings.Contains(text, "Peer request") {
			t.Errorf("%s: read (%s)", name, text)
		}
	}

	var own issueThreadOut
	isErr, text := callTool(t, linguist, "openloc_get_issue", map[string]any{"issue_id": w.own.ID}, &own)
	if isErr || !strings.Contains(text, "Which file was it?") || strings.Contains(text, "INTERNAL-ONLY") {
		t.Errorf("the linguist's own issue: %s", text)
	}

	var dup issueThreadOut
	isErr, text = callTool(t, owner, "openloc_get_issue", map[string]any{"issue_id": w.ownerIss.ID}, &dup)
	if isErr || dup.DuplicateOf == nil || dup.DuplicateOf.ID != w.otherIss.ID || dup.DuplicateOf.Title != "" ||
		dup.DuplicateOf.CanOpen || strings.Contains(text, "Other org secret title") {
		t.Errorf("another org's original: %s", text)
	}
}

// Replies, fix confirmations and kind changes need write access, act only
// where the app lets the caller act, and do what the app does.
func TestIntegrationMCPIssues_Actions(t *testing.T) {
	w := newIssueWorld(t)
	ctx := context.Background()
	readOwner := connect(t, w.url, w.tokenA)
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"openloc_reply_to_issue", map[string]any{"issue_id": w.peerIss.ID, "text": "Still broken."}},
		{"openloc_confirm_issue_fix", map[string]any{"issue_id": w.peerIss.ID}},
		{"openloc_change_issue_kind", map[string]any{"issue_id": w.peerIss.ID, "kind": "enhancement"}},
	} {
		if isErr, _ := callTool(t, readOwner, c.tool, c.args, nil); !isErr {
			t.Errorf("%s with a read key ran", c.tool)
		}
	}
	if d, err := w.svc.PlatformGet(ctx, w.peerIss.ID); err != nil || d.Issue.Status != issues.StatusResolved || d.Issue.Kind != "feature_request" {
		t.Fatalf("read keys changed the issue: %+v %v", d.Issue, err)
	}

	owner := connect(t, w.url, writeToken(t, w.fixture, w.tenantA.ID, w.ownerA.ID))
	linguist := connect(t, w.url, writeToken(t, w.fixture, w.tenantA.ID, w.linguistA.ID))
	ownerB := connect(t, w.url, writeToken(t, w.fixture, w.bTenant.ID, w.ownerB.ID))

	// Outside the caller's scope: not found, nothing changes.
	for name, c := range map[string]struct {
		s    *sdk.ClientSession
		tool string
		args map[string]any
	}{
		"a linguist replies to a colleague's": {linguist, "openloc_reply_to_issue", map[string]any{"issue_id": w.peerIss.ID, "text": "Hi"}},
		"another org replies":                 {ownerB, "openloc_reply_to_issue", map[string]any{"issue_id": w.peerIss.ID, "text": "Hi"}},
		"another org confirms":                {ownerB, "openloc_confirm_issue_fix", map[string]any{"issue_id": w.peerIss.ID}},
		"another org changes the kind":        {ownerB, "openloc_change_issue_kind", map[string]any{"issue_id": w.own.ID, "kind": "enhancement"}},
	} {
		if isErr, _ := callTool(t, c.s, c.tool, c.args, nil); !isErr {
			t.Errorf("%s: ran", name)
		}
	}

	// A manager's reply to a colleague's resolved issue reopens it and emails
	// the operator it is assigned to.
	if _, err := w.svc.PlatformUpdate(ctx, w.op, w.peerIss.ID, issues.IssueUpdate{AssigneeSet: true, AssigneeID: &w.op.UserID}); err != nil {
		t.Fatal(err)
	}
	before := len(w.outbox.For(issueOperator))
	var replied issueThreadOut
	if isErr, text := callTool(t, owner, "openloc_reply_to_issue", map[string]any{"issue_id": w.peerIss.ID, "text": "The fix does not work."}, &replied); isErr ||
		replied.Status != issues.StatusOpen || !strings.Contains(text, "The fix does not work.") {
		t.Errorf("reply: %s", text)
	}
	if len(w.outbox.For(issueOperator)) != before+1 {
		t.Errorf("the assignee got %d emails, want 1", len(w.outbox.For(issueOperator))-before)
	}

	// Confirming needs a resolved issue.
	if isErr, _ := callTool(t, linguist, "openloc_confirm_issue_fix", map[string]any{"issue_id": w.own.ID}, nil); !isErr {
		t.Error("an open issue's fix was confirmed")
	}
	if _, err := w.svc.PlatformSetStatus(ctx, w.op, w.own.ID, issues.StatusChange{Status: issues.StatusResolved}); err != nil {
		t.Fatal(err)
	}
	var confirmed issueThreadOut
	if isErr, text := callTool(t, linguist, "openloc_confirm_issue_fix", map[string]any{"issue_id": w.own.ID}, &confirmed); isErr || confirmed.ConfirmedAt == nil {
		t.Errorf("confirm: %s", text)
	}

	// A kind change follows the app's rules: a severity for a bug only.
	if isErr, _ := callTool(t, linguist, "openloc_change_issue_kind", map[string]any{"issue_id": w.own.ID, "kind": "enhancement", "severity": "major"}, nil); !isErr {
		t.Error("a severity on an enhancement was accepted")
	}
	var changed issueThreadOut
	if isErr, text := callTool(t, linguist, "openloc_change_issue_kind", map[string]any{"issue_id": w.own.ID, "kind": "enhancement"}, &changed); isErr ||
		changed.Kind != "enhancement" || changed.Severity != nil {
		t.Errorf("kind change: %s", text)
	}
}

// The issue tools are listed for every role exactly when the server takes
// reports; the list and the read are allowed in read-only mode, the actions
// are not, and replies and confirmations are marked as reaching outside.
func TestIntegrationMCPIssues_ListedWithTheirGates(t *testing.T) {
	off := newFixture(t)
	if names := listToolNames(t, connect(t, off.url, off.tokenA)); slices.ContainsFunc(issueAccessTools, func(n string) bool { return slices.Contains(names, n) }) {
		t.Error("issue tools listed on a server that takes no reports")
	}
	w := newIssueFixture(t)
	want := map[string]struct{ readOnly, openWorld bool }{
		"openloc_list_issues": {true, false}, "openloc_get_issue": {true, false},
		"openloc_reply_to_issue": {false, true}, "openloc_confirm_issue_fix": {false, true},
		"openloc_change_issue_kind": {false, false},
	}
	for role, token := range map[string]string{"owner": w.tokenA, "linguist": w.tokenLingA} {
		res, err := connect(t, w.url, token).ListTools(context.Background(), nil)
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
			if tool.Annotations.ReadOnlyHint != c.readOnly || mcp.AllowedInReadOnly(tool) != c.readOnly ||
				tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint != c.openWorld {
				t.Errorf("%s %s: read-only %v, allowed in read-only %v, open world %v", role, tool.Name,
					tool.Annotations.ReadOnlyHint, mcp.AllowedInReadOnly(tool), tool.Annotations.OpenWorldHint)
			}
		}
		if seen != len(want) {
			t.Errorf("%s: %d of the %d issue tools listed", role, seen, len(want))
		}
	}
}
