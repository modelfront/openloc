package issues_test

import (
	"context"
	"net/http"
	"testing"

	"openloc/internal/issues"
	"openloc/internal/testutil"
)

// An organization being closed (its data is deleted in the background) leaves
// the support team's queue at once: its issues are not listed, cannot be
// opened or acted on, and the org drops out of the organization filter, so
// nobody replies to, resolves or notifies an org that no longer exists.
func TestIntegrationIssue_ClosingOrgLeavesTheSupportQueue(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Export does nothing")
	otherRec := f.asMember(t, "/api/issues", f.outsider.ID, f.other.ID, bugFields("Other org's report"))
	if otherRec.Code != http.StatusCreated {
		t.Fatalf("other org's report: %d %s", otherRec.Code, otherRec.Body.String())
	}
	var other struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, otherRec, &other)
	// The closing org's issue duplicates the other org's: the original lists
	// it until the org closes.
	if code := f.markDuplicate(t, issue.ID, other.Data.ID); code != http.StatusOK {
		t.Fatalf("mark duplicate: %d", code)
	}
	if d := f.platformGet(t, other.Data.ID).Issue.Duplicates; len(d) != 1 || d[0].ID != issue.ID {
		t.Fatalf("before the close, the original's duplicates = %+v", d)
	}
	if rec := f.asOperator(t, http.MethodPatch, platformPath(issue.ID, ""), map[string]any{"tags": []string{"closing-only"}}); rec.Code != http.StatusOK {
		t.Fatalf("tag: %d", rec.Code)
	}
	bells := func() int {
		var n int
		if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE tenant_id = $1`, f.tenant.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	bellsBefore := bells()
	emailsBefore := len(subjects(f.outbox, "rep@test.com"))
	if _, err := f.pool.Exec(context.Background(), `UPDATE tenants SET status = 'deleting', domain = NULL WHERE id = $1`, f.tenant.ID); err != nil {
		t.Fatal(err)
	}
	var tags struct{ Data []string }
	testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues/tags", nil), &tags)
	for _, tag := range tags.Data {
		if tag == "closing-only" {
			t.Errorf("the tag picker still offers a tag only the closing org's issue has")
		}
	}
	if d := f.platformGet(t, other.Data.ID).Issue.Duplicates; len(d) != 0 {
		t.Errorf("the original still lists the closing org's duplicate: %+v", d)
	}

	var page struct{ Data []issues.PlatformIssueView }
	testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues?status=", nil), &page)
	for _, v := range page.Data {
		if v.ID == issue.ID {
			t.Errorf("the closing org's issue is still listed for the support team")
		}
	}
	if len(page.Data) == 0 || page.Data[0].ID != other.Data.ID {
		t.Errorf("the other org's issue is gone too: %+v", page.Data)
	}
	var orgs struct{ Data []issues.IssueOrg }
	testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues/orgs", nil), &orgs)
	for _, o := range orgs.Data {
		if o.ID == f.tenant.ID {
			t.Errorf("the closing org is still in the organization filter")
		}
	}
	for name, rec := range map[string]int{
		"open":    f.asOperator(t, http.MethodGet, platformPath(issue.ID, ""), nil).Code,
		"resolve": f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"), map[string]string{"status": "resolved"}).Code,
		"triage":  f.asOperator(t, http.MethodPatch, platformPath(issue.ID, ""), map[string]any{"priority": "high"}).Code,
		"reply":   f.operatorMultipart(t, platformPath(issue.ID, "/comments"), map[string]string{"body": "Fixed."}).Code,
	} {
		if rec != http.StatusNotFound {
			t.Errorf("%s the closing org's issue: %d, want 404", name, rec)
		}
	}
	if s := subjects(f.outbox, "rep@test.com"); len(s) != emailsBefore {
		t.Errorf("the closing org's reporter was emailed after the close: %v", s[:len(s)-emailsBefore])
	}
	if n := bells(); n != bellsBefore {
		t.Errorf("the closing org got %d new bell notifications after the close, want none", n-bellsBefore)
	}
}
