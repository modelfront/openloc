package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	"openloc/internal/file"
	"openloc/internal/issues"
	"openloc/internal/mcp"
	"openloc/internal/testutil"
)

const (
	toolReportIssue = "openloc_report_issue"
	issueVia        = "AI assistant"
)

const issueOperator = "ops@test.com"

// newIssueFixture is a server whose deployment has a platform operator, so it
// takes issue reports, with an owner and a linguist holding READ tokens.
func newIssueFixture(t *testing.T) *fixture {
	t.Helper()
	return newIssueFixtureWithStore(t, file.NewMemoryStore())
}

func newIssueFixtureWithStore(t *testing.T, store *file.MemoryStore) *fixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router, _ := testutil.NewTestRouterWithIssues(pool, store, []string{issueOperator})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	f := &fixture{q: q, pool: pool, url: srv.URL}
	f.tenantA = testutil.SeedTenant(t, q, "McpTenantA")
	f.ownerA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "mcp-a@test.com", "pass", "owner")
	f.linguistA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "mcp-l@test.com", "pass", "linguist")
	f.tokenA = testutil.SeedAPIToken(t, q, f.tenantA.ID, f.ownerA.ID, auth.CapabilityRead, time.Hour)
	f.tokenLingA = testutil.SeedAPIToken(t, q, f.tenantA.ID, f.linguistA.ID, auth.CapabilityRead, time.Hour)
	return f
}

type reportOut struct {
	Status       string                 `json:"status"`
	ConfirmToken string                 `json:"confirm_token"`
	Preview      *mcp.Preview           `json:"preview"`
	Result       *mcp.ReportIssueResult `json:"result"`
}

func bugArgs(title string) map[string]any {
	return map[string]any{"kind": "bug", "severity": "critical", "area": "editor", "title": title,
		"description": "The export button does nothing.", "page": "/projects/7/jobs/9/edit?tab=qa"}
}

// A READ token reports an issue: the first call previews and files nothing,
// the confirmed call files it with the caller's role, the page and how it was
// sent, and the reporter can then read it in the app.
func TestIntegrationMCPReportIssue_ReadTokenPreviewsThenFiles(t *testing.T) {
	f := newIssueFixture(t)
	for name, c := range map[string]struct {
		token, role string
		userID      int64
	}{
		"owner":    {f.tokenA, "owner", f.ownerA.ID},
		"linguist": {f.tokenLingA, "linguist", f.linguistA.ID},
	} {
		t.Run(name, func(t *testing.T) {
			s := connect(t, f.url, c.token)
			args := bugArgs("Export does nothing for " + name)
			var first reportOut
			if isErr, text := callTool(t, s, toolReportIssue, args, &first); isErr {
				t.Fatalf("preview: %s", text)
			}
			if first.Status != "needs_confirmation" || first.ConfirmToken == "" || first.Preview == nil ||
				!strings.Contains(first.Preview.Summary, "Bug (Critical), Editor") {
				t.Fatalf("preview = %+v", first)
			}
			if n := countIssues(t, f, c.userID); n != 0 {
				t.Fatalf("the preview filed %d issues", n)
			}

			args["confirm_token"] = first.ConfirmToken
			var done reportOut
			if isErr, text := callTool(t, s, toolReportIssue, args, &done); isErr {
				t.Fatalf("confirm: %s", text)
			}
			if done.Status != "done" || done.Result == nil || done.Result.Status != "open" ||
				done.Result.Path != "/issues/"+itoa(done.Result.ID) {
				t.Fatalf("done = %+v", done)
			}
			var issueCtx map[string]any
			if err := json.Unmarshal(issueContext(t, f, done.Result.ID), &issueCtx); err != nil {
				t.Fatal(err)
			}
			if issueCtx["role"] != c.role || issueCtx["path"] != "/projects/7/jobs/9/edit" || issueCtx["via"] != issueVia {
				t.Errorf("context = %v", issueCtx)
			}
		})
	}
}

// What the service refuses is refused at the preview, before the user is asked
// to confirm; a token for other arguments is refused.
func TestIntegrationMCPReportIssue_Refusals(t *testing.T) {
	f := newIssueFixture(t)
	s := connect(t, f.url, f.tokenA)
	noSeverity := bugArgs("No severity")
	delete(noSeverity, "severity")
	featureWithSeverity := bugArgs("Feature")
	featureWithSeverity["kind"] = "feature_request"
	for name, args := range map[string]map[string]any{
		"bug without severity":  noSeverity,
		"severity on a non-bug": featureWithSeverity,
		"unknown kind":          map[string]any{"kind": "question", "title": "x", "description": "y"},
		"empty description":     map[string]any{"kind": "enhancement", "title": "x", "description": " "},
	} {
		t.Run(name, func(t *testing.T) {
			if isErr, _ := callTool(t, s, toolReportIssue, args, nil); !isErr {
				t.Error("preview accepted it")
			}
		})
	}

	var first reportOut
	callTool(t, s, toolReportIssue, bugArgs("Original"), &first)
	changed := bugArgs("Changed after the preview")
	changed["confirm_token"] = first.ConfirmToken
	if isErr, _ := callTool(t, s, toolReportIssue, changed, nil); !isErr {
		t.Error("a confirm token for other arguments was accepted")
	}
	if n := countIssues(t, f, f.ownerA.ID); n != 0 {
		t.Errorf("%d issues filed", n)
	}
}

// A server without platform operators takes no reports: the tool is not
// listed, for any role, and one that takes reports lists it for every role.
func TestIntegrationMCPReportIssue_ListedOnlyWhenReportsAreOn(t *testing.T) {
	for name, c := range map[string]struct {
		fixture func(*testing.T) *fixture
		listed  bool
	}{"no operators": {newFixture, false}, "operators": {newIssueFixture, true}} {
		t.Run(name, func(t *testing.T) {
			f := c.fixture(t)
			for role, token := range map[string]string{"owner": f.tokenA, "linguist": f.tokenLingA} {
				if got := slices.Contains(listToolNames(t, connect(t, f.url, token)), toolReportIssue); got != c.listed {
					t.Errorf("%s: listed = %v, want %v", role, got, c.listed)
				}
			}
		})
	}
}

func countIssues(t *testing.T, f *fixture, reporterID int64) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM issues WHERE reporter_id = $1`, reporterID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func issueContext(t *testing.T, f *fixture, id int64) []byte {
	t.Helper()
	var raw []byte
	if err := f.pool.QueryRow(context.Background(), `SELECT context FROM issues WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

// A check from an MCP client other than the built-in assistant is the
// reporter's own claim: the preview signs none and the report stores none.
// The built-in assistant's path is TestIntegrationChatIssueCheckTokenGoesToTheBrowserOnly.
func TestIntegrationMCPReportIssue_ExternalClientCheckIsIgnored(t *testing.T) {
	f := newIssueFixture(t)
	s := connect(t, f.url, f.tokenA)
	args := bugArgs("Editor stops at 100")
	args["verification"], args["verification_note"] = "verified", "I saw it myself."
	var first reportOut
	if isErr, text := callTool(t, s, toolReportIssue, args, &first); isErr {
		t.Fatalf("preview: %s", text)
	}
	if first.Preview == nil || first.Preview.VerificationToken != "" {
		t.Fatalf("an external client got a verification token: %+v", first.Preview)
	}
	args["confirm_token"] = first.ConfirmToken
	var done reportOut
	if isErr, text := callTool(t, s, toolReportIssue, args, &done); isErr || done.Result == nil {
		t.Fatalf("confirm: %s", text)
	}
	var verdict *string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT chat_verification FROM issues WHERE id = $1`, done.Result.ID).Scan(&verdict); err != nil {
		t.Fatal(err)
	}
	if verdict != nil {
		t.Errorf("stored an external client's check: %q", *verdict)
	}
}

// The schema tells the model the screenshot limit the server enforces.
func TestIntegrationMCPReportIssue_ScreenshotLimit(t *testing.T) {
	f := newIssueFixture(t)
	s := connect(t, f.url, f.tokenA)
	tools, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("At most %d.", issues.MaxScreenshots)
	i := slices.IndexFunc(tools.Tools, func(tool *sdk.Tool) bool { return tool.Name == toolReportIssue })
	if i < 0 {
		t.Fatal("the report tool is not listed")
	}
	if raw, _ := json.Marshal(tools.Tools[i].InputSchema); !strings.Contains(string(raw), want) {
		t.Errorf("attachment_upload_ids does not say %q: %s", want, raw)
	}
	args := bugArgs("Too many")
	ids := make([]string, issues.MaxScreenshots+1)
	for n := range ids {
		ids[n] = fmt.Sprintf("upload-%d", n)
	}
	args["attachment_upload_ids"] = ids
	refusal := fmt.Sprintf("at most %d screenshots", issues.MaxScreenshots)
	if isErr, text := callTool(t, s, toolReportIssue, args, nil); !isErr || !strings.Contains(text, refusal) {
		t.Errorf("%d screenshots: %v %s", len(ids), isErr, text)
	}
}

// The report attaches the screenshots the user sent in the conversation: their
// own unexpired uploads, images only, checked at the preview.
func TestIntegrationMCPReportIssue_AttachesScreenshots(t *testing.T) {
	store := file.NewMemoryStore()
	f := newIssueFixtureWithStore(t, store)
	s := connect(t, f.url, f.tokenA)
	png := testutil.PNG("screenshot")
	shot := stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "screenshot.png", png)
	notImage := stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "notes.txt", []byte("plain text"))
	theirs := stageUpload(t, f, store, f.tenantA.ID, f.linguistA.ID, "theirs.png", png)

	for name, id := range map[string]string{"not an image": notImage, "a colleague's upload": theirs, "made up": "00000000-0000-0000-0000-000000000000"} {
		args := bugArgs("Bad attachment")
		args["attachment_upload_ids"] = []string{id}
		if isErr, _ := callTool(t, s, toolReportIssue, args, nil); !isErr {
			t.Errorf("%s: the preview accepted it", name)
		}
	}

	args := bugArgs("Grid is empty")
	args["attachment_upload_ids"] = []string{shot}
	var first reportOut
	if isErr, text := callTool(t, s, toolReportIssue, args, &first); isErr {
		t.Fatalf("preview: %s", text)
	}
	args["confirm_token"] = first.ConfirmToken
	var done reportOut
	if isErr, text := callTool(t, s, toolReportIssue, args, &done); isErr || done.Result == nil {
		t.Fatalf("confirm: %s", text)
	}
	var name string
	var size int64
	if err := f.pool.QueryRow(context.Background(),
		`SELECT filename, size_bytes FROM issue_attachments WHERE issue_id = $1`, done.Result.ID).Scan(&name, &size); err != nil {
		t.Fatal(err)
	}
	if name != "screenshot.png" || size != int64(len(png)) {
		t.Errorf("attachment = %s, %d bytes", name, size)
	}
}
