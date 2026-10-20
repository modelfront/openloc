package issues_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/issues"
	"openloc/internal/testutil"
)

// A filed issue keeps what the reporter typed, a cleaned context with the role
// from the session, and the files; the operators get an email.
func TestIntegrationIssue_CreateAndReadBack(t *testing.T) {
	f := newFixture(t)
	body, ct := multipartBody(t, map[string]string{
		"kind": "bug", "severity": "critical", "area": "mt",
		"title": "  Export does nothing  ", "description": "I click Export and nothing happens.",
		"context": `{"path":"/projects/7/jobs/9?tab=qa#x","project_id":7,"job_id":"9","role":"owner","secret":"x","browser":"Firefox 131"}`,
	}, png("screen.png"), upload{"server.log", []byte("panic: export failed\n")})
	req := httptest.NewRequest(http.MethodPost, "/api/issues", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", testutil.AuthHeaderWithRole(f.reporter.ID, f.tenant.ID, "linguist"))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}

	var created struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, rec, &created)
	got := decodeDetail(t, f.get(t, issuePath(created.Data.ID, ""), f.reporter.ID, f.tenant.ID)).Issue
	if got.Title != "Export does nothing" || got.Status != issues.StatusOpen || got.Kind != "bug" ||
		got.Severity == nil || *got.Severity != "critical" || got.Area != "mt" {
		t.Errorf("issue = %+v", got)
	}
	if len(got.Attachments) != 2 || got.Attachments[0].Filename != "screen.png" || !got.Attachments[0].IsImage ||
		got.Attachments[1].Filename != "server.log" || got.Attachments[1].IsImage ||
		got.Attachments[1].SizeBytes != int64(len("panic: export failed\n")) {
		t.Errorf("attachments = %+v", got.Attachments)
	}
	var ctx map[string]any
	if err := json.Unmarshal(got.Context, &ctx); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"path": "/projects/7/jobs/9", "project_id": float64(7), "job_id": float64(9),
		"role": "linguist", "browser": "Firefox 131"}
	if len(ctx) != len(want) {
		t.Errorf("context = %v, want %v", ctx, want)
	}
	for k, v := range want {
		if ctx[k] != v {
			t.Errorf("context[%s] = %v, want %v", k, ctx[k], v)
		}
	}

	if s := subjects(f.outbox, operatorEmail); len(s) != 1 || s[0] != fmt.Sprintf("Issue #%d: Export does nothing", created.Data.ID) {
		t.Errorf("operator emails = %v", s)
	}

	var list struct{ Data []issues.IssueView }
	testutil.DecodeJSON(t, f.get(t, "/api/issues", f.reporter.ID, f.tenant.ID), &list)
	if len(list.Data) != 1 || list.Data[0].ID != got.ID {
		t.Errorf("list = %+v", list.Data)
	}
}

// Outside the reporter, only the org's managers reach an issue: not a linguist
// peer in the same org, not a member of another org.
func TestIntegrationIssue_WhoSeesIt(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Private issue", png("screen.png"))
	attachment := issuePath(issue.ID, "/attachments/"+itoa(issue.Attachments[0].ID))
	for name, who := range map[string][2]int64{
		"linguist peer": {f.peer.ID, f.tenant.ID},
		"outsider":      {f.outsider.ID, f.other.ID},
	} {
		t.Run(name, func(t *testing.T) {
			if rec := f.getAs(t, issuePath(issue.ID, ""), who[0], who[1], "linguist"); rec.Code != http.StatusNotFound {
				t.Errorf("get: %d", rec.Code)
			}
			if rec := f.getAs(t, attachment, who[0], who[1], "linguist"); rec.Code != http.StatusNotFound {
				t.Errorf("attachment: %d", rec.Code)
			}
			if rec := f.asMemberWithRole(t, issuePath(issue.ID, "/comments"), who[0], who[1], "linguist", map[string]string{"body": "me too"}); rec.Code != http.StatusNotFound {
				t.Errorf("comment: %d", rec.Code)
			}
			var list struct{ Data []issues.IssueView }
			testutil.DecodeJSON(t, f.getAs(t, "/api/issues", who[0], who[1], "linguist"), &list)
			if len(list.Data) != 0 {
				t.Errorf("list = %+v", list.Data)
			}
		})
	}
	// The outsider is an owner of their own org: a manager role does not reach
	// another org's issues.
	if rec := f.getAs(t, issuePath(issue.ID, ""), f.outsider.ID, f.other.ID, "owner"); rec.Code != http.StatusNotFound {
		t.Errorf("owner of another org: %d", rec.Code)
	}
}

// A manager sees every member's issues, with the reporter and without the org,
// reads their files, and answers for the org: Needs info goes back to Open.
func TestIntegrationIssue_ManagerSeesTheOrg(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Linguist's issue", png("screen.png"))
	for _, role := range []string{"owner", "admin", "project_manager"} {
		t.Run(role, func(t *testing.T) {
			var list struct{ Data []issues.IssueView }
			testutil.DecodeJSON(t, f.getAs(t, "/api/issues", f.colleague.ID, f.tenant.ID, role), &list)
			if len(list.Data) != 1 || list.Data[0].ID != issue.ID || list.Data[0].ReporterEmail != "rep@test.com" ||
				list.Data[0].OrgName != "" {
				t.Errorf("list = %+v", list.Data)
			}
			if rec := f.getAs(t, issuePath(issue.ID, "/attachments/"+itoa(issue.Attachments[0].ID)), f.colleague.ID, f.tenant.ID, role); rec.Code != http.StatusOK {
				t.Errorf("attachment: %d", rec.Code)
			}
		})
	}

	f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"), map[string]string{"status": "needs_info", "note": "Which file?"})
	d := decodeDetail(t, f.asMemberWithRole(t, issuePath(issue.ID, "/comments"), f.colleague.ID, f.tenant.ID, "owner",
		map[string]string{"body": "The contract DOCX."}))
	last := d.Comments[len(d.Comments)-1]
	if d.Issue.Status != issues.StatusOpen || last.AuthorSide != "reporter" || last.AuthorName == "" {
		t.Errorf("after the manager's answer: %+v", d)
	}
	if d.Issue.ReporterEmail != "rep@test.com" {
		t.Errorf("detail reporter = %q", d.Issue.ReporterEmail)
	}
}

// The org's list takes the same filters as the operators', and refuses unknown values.
func TestIntegrationIssue_MemberFilters(t *testing.T) {
	f := newFixture(t)
	bug := f.report(t, "Editor bug")
	f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, map[string]string{
		"kind": "feature_request", "area": "tm", "title": "TM feature", "description": "x"})
	var list struct{ Data []issues.IssueView }
	testutil.DecodeJSON(t, f.getAs(t, "/api/issues?kind=bug&area=editor&severity=major&status=open", f.reporter.ID, f.tenant.ID, "linguist"), &list)
	if len(list.Data) != 1 || list.Data[0].ID != bug.ID {
		t.Errorf("filtered = %+v", list.Data)
	}
	if rec := f.getAs(t, "/api/issues?area=billing", f.reporter.ID, f.tenant.ID, "linguist"); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown area: %d", rec.Code)
	}
}

func TestIntegrationIssue_RefusesBadInput(t *testing.T) {
	f := newFixture(t)
	valid := bugFields("Title")
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for kk, vv := range valid {
			m[kk] = vv
		}
		m[k] = v
		return m
	}
	without := func(k string) map[string]string {
		m := with(k, "")
		delete(m, k)
		return m
	}
	enhancement := with("kind", "enhancement")
	tooMany := make([]upload, issues.MaxFiles+1)
	for i := range tooMany {
		tooMany[i] = upload{"note" + itoa(int64(i)) + ".txt", []byte("x")}
	}
	cases := map[string]struct {
		fields map[string]string
		files  []upload
	}{
		"unknown kind":          {with("kind", "question"), nil},
		"bug without severity":  {without("severity"), nil},
		"unknown severity":      {with("severity", "urgent"), nil},
		"severity on a non-bug": {enhancement, nil},
		"unknown area":          {with("area", "billing"), nil},
		"empty title":           {with("title", "   "), nil},
		"long title":            {with("title", strings.Repeat("x", 201)), nil},
		"empty description":     {with("description", ""), nil},
		"too many files":        {valid, tooMany},
		"file over 25 MB":       {valid, []upload{{"big.bin", make([]byte, issues.MaxFileSize+1)}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, c.fields, c.files...)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d %s", rec.Code, rec.Body.String())
			}
		})
	}
	issue := f.report(t, "Valid")
	if rec := f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID, map[string]string{"body": " "}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty comment: %d", rec.Code)
	}
	if f.store.Len() != 0 {
		t.Errorf("refused requests left %d blobs", f.store.Len())
	}
}

// The eleventh issue in an hour is refused.
func TestIntegrationIssue_RateLimit(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 10; i++ {
		f.report(t, "Issue "+itoa(int64(i)))
	}
	rec := f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, bugFields("One more"))
	if rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != "issue_rate_limited" {
		t.Errorf("11th issue: %d %s", rec.Code, rec.Body.String())
	}
}

// With no platform admins nobody would read issues: the routes are off and the
// app config says so.
func TestIntegrationIssue_OffWithoutOperators(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "Acme Org")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "rep@test.com", "pw", "owner")
	if rec := testutil.DoRequest(t, router, http.MethodGet, "/api/issues", nil, user.ID, tenant.ID); rec.Code != http.StatusNotFound {
		t.Errorf("/api/issues: %d", rec.Code)
	}
	var cfg map[string]any
	testutil.DecodeJSON(t, testutil.DoRequest(t, router, http.MethodGet, "/api/config", nil, 0, 0), &cfg)
	if cfg["issues"] != false {
		t.Errorf("config issues = %v", cfg["issues"])
	}
	f := newFixture(t)
	testutil.DecodeJSON(t, testutil.DoRequest(t, f.router, http.MethodGet, "/api/config", nil, 0, 0), &cfg)
	if cfg["issues"] != true {
		t.Errorf("config issues with operators = %v", cfg["issues"])
	}
}

// The form marks a report the assistant wrote; no other "via" is kept.
func TestIntegrationIssue_ViaAssistantOnly(t *testing.T) {
	f := newFixture(t)
	for via, kept := range map[string]bool{"AI assistant": true, "Support team": false, "": false} {
		fields := bugFields("Via " + via)
		fields["context"] = `{"path":"/projects","via":"` + via + `"}`
		var created struct{ Data issues.IssueView }
		testutil.DecodeJSON(t, f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, fields), &created)
		var ctx map[string]any
		if err := json.Unmarshal(created.Data.Context, &ctx); err != nil {
			t.Fatal(err)
		}
		if _, has := ctx["via"]; has != kept || (kept && ctx["via"] != via) {
			t.Errorf("via %q: context = %v", via, ctx)
		}
	}
}

// A member may write 30 comments an hour; the 31st is refused. Operators are
// not limited.
func TestIntegrationIssue_CommentLimit(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Chatty")
	for i := 0; i < 30; i++ {
		if rec := f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID, map[string]string{"body": "more " + itoa(int64(i))}); rec.Code != http.StatusCreated {
			t.Fatalf("comment %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID, map[string]string{"body": "one more"})
	if rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != "issue_rate_limited" {
		t.Errorf("31st comment: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.operatorMultipart(t, platformPath(issue.ID, "/comments"), map[string]string{"body": "Operators are not limited."}); rec.Code != http.StatusCreated {
		t.Errorf("operator comment: %d", rec.Code)
	}
}

// The server replaces the secret part of share, invite and reset links in the
// page it stores, whatever the client sent.
func TestIntegrationIssue_ContextDropsLinkSecrets(t *testing.T) {
	f := newFixture(t)
	for path, want := range map[string]string{
		"/invite/abc123?x=1":         "/invite/:token",
		"/shared/charts/LB7Kq9x#top": "/shared/charts/:token",
		"/reset-password/s3cr3t":     "/reset-password/:token",
		"/projects/7?tab=qa":         "/projects/7",
	} {
		fields := bugFields("Path " + path)
		fields["context"] = `{"path":"` + path + `"}`
		var created struct{ Data issues.IssueView }
		testutil.DecodeJSON(t, f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID, fields), &created)
		var ctx map[string]any
		if err := json.Unmarshal(created.Data.Context, &ctx); err != nil {
			t.Fatal(err)
		}
		if ctx["path"] != want {
			t.Errorf("%s: stored path = %v, want %s", path, ctx["path"], want)
		}
	}
}
