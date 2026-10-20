package issues_test

import (
	"context"
	"net/http"
	"testing"

	"openloc/internal/issues"
	"openloc/internal/testutil"

	db "openloc/internal/database/sqlcgen"
)

// deleteFixture adds a member of every role to the base fixture's org.
type deleteFixture struct {
	*fixture
	pm, pm2, admin, admin2, owner2 db.User
}

func newDeleteFixture(t *testing.T) *deleteFixture {
	t.Helper()
	f := &deleteFixture{fixture: newFixture(t)}
	seed := func(email, role string) db.User {
		return testutil.SeedUserWithRole(t, f.q, f.tenant.ID, email, "pw", role)
	}
	f.pm, f.pm2 = seed("pm@test.com", "project_manager"), seed("pm2@test.com", "project_manager")
	f.admin, f.admin2 = seed("adm@test.com", "admin"), seed("adm2@test.com", "admin")
	f.owner2 = seed("own2@test.com", "owner")
	return f
}

func (f *deleteFixture) role(u db.User) string {
	switch u.ID {
	case f.pm.ID, f.pm2.ID:
		return "project_manager"
	case f.admin.ID, f.admin2.ID:
		return "admin"
	}
	return f.roleOf(u.ID)
}

// file reports a bug as u and returns its id.
func (f *deleteFixture) file(t *testing.T, u db.User, title string) int64 {
	t.Helper()
	rec := f.asMemberWithRole(t, "/api/issues", u.ID, f.tenant.ID, f.role(u), bugFields(title))
	if rec.Code != http.StatusCreated {
		t.Fatalf("report as %s: %d %s", u.Email, rec.Code, rec.Body.String())
	}
	var env struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, rec, &env)
	return env.Data.ID
}

func (f *deleteFixture) del(t *testing.T, id int64, u db.User) int {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, http.MethodDelete, issuePath(id, ""), nil, u.ID, f.tenant.ID, f.role(u)).Code
}

// Everyone deletes their own issues; past that, a role deletes the issues of
// lower roles, an owner every issue of the org, and nobody a same-rank
// colleague's. An issue the caller cannot see is not found.
func TestIntegrationDeleteIssueFollowsRoles(t *testing.T) {
	f := newDeleteFixture(t)
	for _, tc := range []struct {
		name     string
		reporter func() db.User
		by       func() db.User
		want     int
	}{
		{"own, linguist", func() db.User { return f.reporter }, func() db.User { return f.reporter }, 204},
		{"own, admin", func() db.User { return f.admin }, func() db.User { return f.admin }, 204},
		{"linguist's, by another linguist", func() db.User { return f.reporter }, func() db.User { return f.peer }, 404},
		{"linguist's, by a project manager", func() db.User { return f.reporter }, func() db.User { return f.pm }, 204},
		{"project manager's, by another project manager", func() db.User { return f.pm }, func() db.User { return f.pm2 }, 403},
		{"project manager's, by an admin", func() db.User { return f.pm }, func() db.User { return f.admin }, 204},
		{"admin's, by a project manager", func() db.User { return f.admin }, func() db.User { return f.pm }, 403},
		{"admin's, by another admin", func() db.User { return f.admin }, func() db.User { return f.admin2 }, 403},
		{"admin's, by an owner", func() db.User { return f.admin }, func() db.User { return f.colleague }, 204},
		{"owner's, by another owner", func() db.User { return f.colleague }, func() db.User { return f.owner2 }, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := f.file(t, tc.reporter(), tc.name)
			if got := f.del(t, id, tc.by()); got != tc.want {
				t.Fatalf("delete = %d, want %d", got, tc.want)
			}
			// Deleted means gone for the reporter too; refused means still there.
			got := f.getAs(t, issuePath(id, ""), tc.reporter().ID, f.tenant.ID, f.role(tc.reporter())).Code
			if want := map[bool]int{true: 404, false: 200}[tc.want == 204]; got != want {
				t.Errorf("reporter's get after = %d, want %d", got, want)
			}
		})
	}
}

// Another org's member cannot reach the issue at all.
func TestIntegrationDeleteIssueIsTenantScoped(t *testing.T) {
	f := newDeleteFixture(t)
	id := f.file(t, f.reporter, "Cross-tenant")
	rec := testutil.DoRequestWithRole(t, f.router, http.MethodDelete, issuePath(id, ""), nil, f.outsider.ID, f.other.ID, "owner")
	if rec.Code != 404 {
		t.Fatalf("outsider delete = %d, want 404", rec.Code)
	}
	if rec := f.get(t, issuePath(id, ""), f.reporter.ID, f.tenant.ID); rec.Code != 200 {
		t.Errorf("issue gone after an outsider's delete: %d", rec.Code)
	}
}

// A platform operator deletes any org's issue; it leaves both views.
func TestIntegrationOperatorDeletesAnyIssue(t *testing.T) {
	f := newDeleteFixture(t)
	id := f.file(t, f.colleague, "Operator cleans up")
	if rec := f.asOperator(t, http.MethodDelete, platformPath(id, ""), nil); rec.Code != 204 {
		t.Fatalf("operator delete = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := f.asOperator(t, http.MethodGet, platformPath(id, ""), nil); rec.Code != 404 {
		t.Errorf("operator get after = %d, want 404", rec.Code)
	}
	if rec := f.get(t, issuePath(id, ""), f.colleague.ID, f.tenant.ID); rec.Code != 404 {
		t.Errorf("reporter get after = %d, want 404", rec.Code)
	}
	// A member cannot use the operator route.
	other := f.file(t, f.reporter, "Not via the platform route")
	rec := testutil.DoRequestWithRole(t, f.router, http.MethodDelete, platformPath(other, ""), nil, f.colleague.ID, f.tenant.ID, "owner")
	if rec.Code == 204 {
		t.Errorf("an org owner deleted through the operator route")
	}
}

// An issue whose reporter left the org ranks lowest: a manager may delete it.
func TestIntegrationDeleteIssueOfFormerMember(t *testing.T) {
	f := newDeleteFixture(t)
	id := f.file(t, f.admin, "Left behind")
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM memberships WHERE user_id = $1 AND tenant_id = $2`, f.admin.ID, f.tenant.ID); err != nil {
		t.Fatalf("remove the admin from the org: %v", err)
	}
	if got := f.del(t, id, f.pm); got != 204 {
		t.Errorf("project manager deleting a former member's issue = %d, want 204", got)
	}
}

// The list says which issues the viewer may delete, by the same rule.
func TestIntegrationIssueListSaysWhatCanBeDeleted(t *testing.T) {
	f := newDeleteFixture(t)
	own := f.file(t, f.admin, "Mine")
	lower := f.file(t, f.pm, "A manager's")
	same := f.file(t, f.admin2, "Another admin's")

	rec := f.getAs(t, "/api/issues", f.admin.ID, f.tenant.ID, "admin")
	var env struct{ Data []issues.IssueView }
	testutil.DecodeJSON(t, rec, &env)
	got := map[int64]bool{}
	for _, i := range env.Data {
		got[i.ID] = i.CanDelete
	}
	want := map[int64]bool{own: true, lower: true, same: false}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("can_delete of %d = %v, want %v (all: %v)", id, got[id], w, got)
		}
	}

	var detail struct{ Data issues.IssueDetail }
	testutil.DecodeJSON(t, f.getAs(t, issuePath(same, ""), f.admin.ID, f.tenant.ID, "admin"), &detail)
	if detail.Data.Issue.CanDelete {
		t.Errorf("detail of another admin's issue says it can be deleted")
	}
	var platform struct{ Data []issues.IssueView }
	testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues", nil), &platform)
	for _, i := range platform.Data {
		if !i.CanDelete {
			t.Errorf("operator list: issue %d not deletable", i.ID)
		}
	}
}

// The reporter may always delete what they just filed, and the create answer
// says so like every other read of the issue.
func TestIntegrationCreatedIssueSaysItCanBeDeleted(t *testing.T) {
	f := newFixture(t)
	if got := f.report(t, "Just filed"); !got.CanDelete {
		t.Errorf("create answered can_delete = false for the reporter's own issue")
	}
}

// hardDelete deletes a row the way an account or org closure does, cascading
// through its issues.
func hardDelete(t *testing.T, f *fixture, table string, id int64) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), "DELETE FROM "+table+" WHERE id = $1", id); err != nil {
		t.Fatalf("delete from %s: %v", table, err)
	}
}

// duplicateLink is an issue's status and original as stored: every read hides
// a deleted original.
func duplicateLink(t *testing.T, f *fixture, id int64) (string, *int64) {
	t.Helper()
	var status string
	var link *int64
	if err := f.pool.QueryRow(context.Background(), "SELECT status, duplicate_of_id FROM issues WHERE id = $1", id).Scan(&status, &link); err != nil {
		t.Fatal(err)
	}
	return status, link
}

// Deleting the original of duplicates sends every one of them back to the
// support team's queue: open, with no original, waiting on the support team,
// with an entry their org sees. Nothing else tracks their problem. The
// original may be another org's, deleted by its own reporter or by the support
// team, or gone with its org or its reporter's account. A duplicate of another
// original stays as it is.
func TestIntegrationDeletingAnOriginalReopensItsDuplicates(t *testing.T) {
	for name, del := range map[string]func(t *testing.T, f *fixture, id int64){
		"its reporter, in another org": func(t *testing.T, f *fixture, id int64) {
			if rec := testutil.DoRequestWithRole(t, f.router, http.MethodDelete, issuePath(id, ""), nil, f.outsider.ID, f.other.ID, "owner"); rec.Code != http.StatusNoContent {
				t.Fatalf("delete the original: %d", rec.Code)
			}
		},
		"the support team": func(t *testing.T, f *fixture, id int64) {
			if rec := f.asOperator(t, http.MethodDelete, platformPath(id, ""), nil); rec.Code != http.StatusNoContent {
				t.Fatalf("delete the original: %d", rec.Code)
			}
		},
		"its org, closed":                 func(t *testing.T, f *fixture, _ int64) { hardDelete(t, f, "tenants", f.other.ID) },
		"its reporter's account, removed": func(t *testing.T, f *fixture, _ int64) { hardDelete(t, f, "users", f.outsider.ID) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			var original struct{ Data issues.IssueView }
			testutil.DecodeJSON(t, f.asMember(t, "/api/issues", f.outsider.ID, f.other.ID, bugFields("Original elsewhere")), &original)
			dup, dup2 := f.report(t, "The same problem here"), f.report(t, "And here")
			kept, keptDup := f.report(t, "Another problem"), f.report(t, "Another problem again")
			for _, p := range [][2]int64{{dup.ID, original.Data.ID}, {dup2.ID, original.Data.ID}, {keptDup.ID, kept.ID}} {
				if code := f.markDuplicate(t, p[0], p[1]); code != http.StatusOK {
					t.Fatalf("mark %d a duplicate of %d: %d", p[0], p[1], code)
				}
			}
			del(t, f, original.Data.ID)

			for _, id := range []int64{dup.ID, dup2.ID} {
				if status, link := duplicateLink(t, f, id); status != issues.StatusOpen || link != nil {
					t.Errorf("duplicate %d after its original went: %s, linked to %v", id, status, link)
				}
			}
			if status, link := duplicateLink(t, f, keptDup.ID); status != issues.StatusDuplicate || link == nil || *link != kept.ID {
				t.Errorf("the duplicate of another original: %s, linked to %v, want it kept on %d", status, link, kept.ID)
			}
			d := decodeDetail(t, f.get(t, issuePath(dup.ID, ""), f.reporter.ID, f.tenant.ID))
			if d.Issue.LastActivityBy != "reporter" || d.Issue.ResolvedAt != nil {
				t.Errorf("the reopened duplicate: last activity by %s, resolved at %v", d.Issue.LastActivityBy, d.Issue.ResolvedAt)
			}
			last := d.Comments[len(d.Comments)-1]
			if last.Status == nil || *last.Status != issues.StatusOpen || last.AuthorSide != "operator" || last.AuthorID != nil || last.Internal {
				t.Errorf("the reopen entry = %+v", last)
			}
			var queue struct{ Data []issues.PlatformIssueView }
			testutil.DecodeJSON(t, f.asOperator(t, http.MethodGet, "/api/platform/issues?status=open,in_progress,needs_info", nil), &queue)
			inQueue := map[int64]bool{}
			for _, i := range queue.Data {
				inQueue[i.ID] = true
			}
			if !inQueue[dup.ID] || !inQueue[dup2.ID] || inQueue[keptDup.ID] {
				t.Errorf("the support team's queue = %v, want both reopened duplicates and not the kept one", inQueue)
			}
		})
	}
}
