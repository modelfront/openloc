package issues_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"openloc/internal/issues"
	"openloc/internal/testutil"
)

// markDuplicate marks id a duplicate of original as the operator.
func (f *fixture) markDuplicate(t *testing.T, id, original int64) int {
	t.Helper()
	return f.asOperator(t, http.MethodPost, platformPath(id, "/status"),
		map[string]any{"status": "duplicate", "duplicate_of": original}).Code
}

// A member always learns the number and status of a duplicate's original,
// and its title and a link only when it is an issue of their org they may
// open: another org's title never reaches them. The support team sees the
// original in full, and the original lists its duplicates. A reply reopens a
// duplicate and drops the link.
func TestIntegrationIssue_DuplicateLinkOnlyWithinTheOrg(t *testing.T) {
	f := newFixture(t)
	dup := f.report(t, "Export broken again")
	ownOriginal := f.report(t, "Export broken")
	peerRec := f.asMember(t, "/api/issues", f.peer.ID, f.tenant.ID, bugFields("Peer original report"))
	otherRec := f.asMember(t, "/api/issues", f.outsider.ID, f.other.ID, bugFields("Outsider original report"))
	var peer, other struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, peerRec, &peer)
	testutil.DecodeJSON(t, otherRec, &other)

	third := f.report(t, "Third copy")
	if code := f.markDuplicate(t, third.ID, ownOriginal.ID); code != http.StatusOK {
		t.Fatalf("mark third: %d", code)
	}
	for name, body := range map[string]map[string]any{
		"no original":         {"status": "duplicate"},
		"itself":              {"status": "duplicate", "duplicate_of": dup.ID},
		"missing original":    {"status": "duplicate", "duplicate_of": 999999},
		"original on non-dup": {"status": "resolved", "duplicate_of": ownOriginal.ID},
		"original is a dup":   {"status": "duplicate", "duplicate_of": third.ID},
	} {
		if rec := f.asOperator(t, http.MethodPost, platformPath(dup.ID, "/status"), body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d %s", name, rec.Code, rec.Body.String())
		}
	}

	type view struct {
		name   string
		userID int64
		role   string
	}
	reporter := view{"reporter", f.reporter.ID, "linguist"}
	manager := view{"manager", f.colleague.ID, "owner"}
	for _, c := range []struct {
		name     string
		original issues.IssueView
		sees     map[view]bool
	}{
		{"same org, own issue", ownOriginal, map[view]bool{reporter: true, manager: true}},
		{"same org, a colleague's", peer.Data, map[view]bool{reporter: false, manager: true}},
		{"another org", other.Data, map[view]bool{reporter: false, manager: false}},
	} {
		if code := f.markDuplicate(t, dup.ID, c.original.ID); code != http.StatusOK {
			t.Fatalf("%s: mark duplicate: %d", c.name, code)
		}
		for v, sees := range c.sees {
			rec := f.getAs(t, issuePath(dup.ID, ""), v.userID, f.tenant.ID, v.role)
			d := decodeDetail(t, rec)
			if d.Issue.Status != issues.StatusDuplicate {
				t.Fatalf("%s/%s: status %s", c.name, v.name, d.Issue.Status)
			}
			wantTitle := ""
			if sees {
				wantTitle = c.original.Title
			}
			if o := d.Issue.DuplicateOf; o == nil || o.ID != c.original.ID || o.Status != issues.StatusOpen ||
				o.CanOpen != sees || o.Title != wantTitle {
				t.Errorf("%s/%s: duplicate_of = %+v, want #%d open, title %q, can_open %v", c.name, v.name, o, c.original.ID, wantTitle, sees)
			}
			if !sees && strings.Contains(rec.Body.String(), c.original.Title) {
				t.Errorf("%s/%s: the original's title leaks: %s", c.name, v.name, rec.Body.String())
			}
			for _, cm := range d.Comments {
				if cm.Change != nil && cm.Change.Field == "duplicate" && cm.Change.To != nil {
					t.Errorf("%s/%s: a duplicate entry names the original: %+v", c.name, v.name, cm.Change)
				}
			}
		}
		for v, sees := range c.sees {
			for _, order := range []string{"", "&sort=updated"} {
				rec := f.getAs(t, "/api/issues?status="+order, v.userID, f.tenant.ID, v.role)
				var page struct{ Data []issues.IssueView }
				testutil.DecodeJSON(t, rec, &page)
				var row *issues.IssueView
				for i := range page.Data {
					if page.Data[i].ID == dup.ID {
						row = &page.Data[i]
					}
				}
				if row == nil {
					t.Fatalf("%s/%s list%s: the duplicate is missing", c.name, v.name, order)
				}
				if o := row.DuplicateOf; o == nil || o.ID != c.original.ID || o.CanOpen != sees || (o.Title != "") != sees {
					t.Errorf("%s/%s list%s: duplicate_of = %+v, want #%d, can_open %v", c.name, v.name, order, o, c.original.ID, sees)
				}
				if !sees && strings.Contains(rec.Body.String(), c.original.Title) {
					t.Errorf("%s/%s list%s: the original's title leaks", c.name, v.name, order)
				}
			}
		}
		op := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(dup.ID, ""), nil))
		last := op.Comments[len(op.Comments)-1]
		if o := op.Issue.DuplicateOf; o == nil || o.ID != c.original.ID || o.Title != c.original.Title ||
			o.Status != issues.StatusOpen || !o.CanOpen {
			t.Errorf("%s: the support team sees the original as %+v, want it in full", c.name, o)
		}
		orig := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(c.original.ID, ""), nil)).Issue
		if !slices.Contains(orig.Duplicates, issues.IssueRef{ID: dup.ID, Title: dup.Title, Status: issues.StatusDuplicate, CanOpen: true}) {
			t.Errorf("%s: the original's duplicates = %+v, want #%d among them", c.name, orig.Duplicates, dup.ID)
		}
		if last.Change == nil ||
			last.Change.Field != "duplicate" || last.Change.To == nil || *last.Change.To != itoa(c.original.ID) {
			t.Errorf("%s: operator view = %+v, last entry %+v", c.name, op.Issue.DuplicateOf, last.Change)
		}
	}

	d := decodeDetail(t, f.asMember(t, issuePath(dup.ID, "/comments"), f.reporter.ID, f.tenant.ID,
		map[string]string{"body": "This is not the same problem."}))
	if d.Issue.Status != issues.StatusOpen || d.Issue.DuplicateOf != nil {
		t.Errorf("after a reply: status %s, duplicate_of %+v", d.Issue.Status, d.Issue.DuplicateOf)
	}
	if op := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(dup.ID, ""), nil)); op.Issue.DuplicateOf != nil {
		t.Errorf("operator still sees the original: %+v", op.Issue.DuplicateOf)
	}
}

// bellEvents counts a user's notifications about one issue, by event type.
func (f *fixture) bellEvents(t *testing.T, userID, tenantID int64, role string, issueID int64) map[string]int {
	t.Helper()
	var page struct {
		Data []struct {
			EventType     string `json:"event_type"`
			ReferenceType string `json:"reference_type"`
			ReferenceID   int64  `json:"reference_id"`
		}
	}
	rec := f.getAs(t, "/api/notifications", userID, tenantID, role)
	if rec.Code != http.StatusOK {
		t.Fatalf("notifications of user %d: %d", userID, rec.Code)
	}
	testutil.DecodeJSON(t, rec, &page)
	got := map[string]int{}
	for _, n := range page.Data {
		if n.ReferenceType == "issue" && n.ReferenceID == issueID {
			got[n.EventType]++
		}
	}
	return got
}

// On resolve, the bell rings for the org's owners, admins and project
// managers too: once each, never twice for a manager who reported it, never
// for a linguist or another org, and with no email.
func TestIntegrationIssue_ResolveRingsTheOrgBell(t *testing.T) {
	f := newFixture(t)
	admin := testutil.SeedUserWithRole(t, f.q, f.tenant.ID, "adm@test.com", "pw", "admin")
	pm := testutil.SeedUserWithRole(t, f.q, f.tenant.ID, "pm@test.com", "pw", "project_manager")
	issue := f.report(t, "Ring the bell")

	for _, status := range []string{"in_progress", "wont_fix", "open"} {
		if rec := f.asOperator(t, http.MethodPost, platformPath(issue.ID, "/status"), map[string]string{"status": status, "note": "x"}); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", status, rec.Code)
		}
	}
	if got := f.bellEvents(t, f.colleague.ID, f.tenant.ID, "owner", issue.ID); got["issue_resolved"] != 0 {
		t.Fatalf("bell before any resolve: %v", got)
	}
	mailsBefore := len(f.outbox.For("col@test.com")) + len(f.outbox.For("adm@test.com")) + len(f.outbox.For("pm@test.com"))

	if rec := f.setStatus(t, issue.ID, "resolved"); rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body.String())
	}
	for name, c := range map[string]struct {
		userID, tenantID int64
		role             string
		want             map[string]int
	}{
		"owner":           {f.colleague.ID, f.tenant.ID, "owner", map[string]int{"issue_resolved": 1}},
		"admin":           {admin.ID, f.tenant.ID, "admin", map[string]int{"issue_resolved": 1}},
		"project manager": {pm.ID, f.tenant.ID, "project_manager", map[string]int{"issue_resolved": 1}},
		"linguist peer":   {f.peer.ID, f.tenant.ID, "linguist", map[string]int{}},
		"reporter":        {f.reporter.ID, f.tenant.ID, "linguist", map[string]int{"issue_updated": 4}},
	} {
		got := f.bellEvents(t, c.userID, c.tenantID, c.role, issue.ID)
		if len(got) != len(c.want) || got["issue_resolved"] != c.want["issue_resolved"] || got["issue_updated"] != c.want["issue_updated"] {
			t.Errorf("%s: bell = %v, want %v", name, got, c.want)
		}
	}
	// The bell API reads one org at a time, so another org's member is counted
	// in the table, under any org.
	var outsider int
	if err := f.pool.QueryRow(testutil.Ctx(),
		"SELECT count(*) FROM notifications WHERE user_id = $1 AND reference_type = 'issue' AND reference_id = $2",
		f.outsider.ID, issue.ID).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	if outsider != 0 {
		t.Errorf("another org's owner got %d notifications", outsider)
	}
	if after := len(f.outbox.For("col@test.com")) + len(f.outbox.For("adm@test.com")) + len(f.outbox.For("pm@test.com")); after != mailsBefore {
		t.Errorf("the org's managers got %d emails", after-mailsBefore)
	}

	// A manager who reported the issue hears it once, as its reporter.
	rec := f.asMember(t, "/api/issues", f.colleague.ID, f.tenant.ID, bugFields("Owner's own report"))
	var own struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, rec, &own)
	if rec := f.setStatus(t, own.Data.ID, "resolved"); rec.Code != http.StatusOK {
		t.Fatalf("resolve the owner's issue: %d", rec.Code)
	}
	if got := f.bellEvents(t, f.colleague.ID, f.tenant.ID, "owner", own.Data.ID); got["issue_updated"] != 1 || got["issue_resolved"] != 0 {
		t.Errorf("manager reporter: bell = %v", got)
	}
	if got := f.bellEvents(t, admin.ID, f.tenant.ID, "admin", own.Data.ID); got["issue_resolved"] != 1 {
		t.Errorf("admin on the owner's issue: bell = %v", got)
	}
}

// The org confirms the fix of a resolved issue, once; a reopen clears the
// confirmation so the next fix is confirmed again. "Still broken" is a reply,
// which reopens.
func TestIntegrationIssue_ConfirmFix(t *testing.T) {
	f := newFixture(t)
	issue := f.report(t, "Confirm me")
	confirm := func(userID int64, role string) int {
		return f.asMemberWithRole(t, issuePath(issue.ID, "/confirm"), userID, f.tenant.ID, role, nil).Code
	}
	if code := confirm(f.reporter.ID, "linguist"); code != http.StatusBadRequest {
		t.Errorf("confirm an open issue: %d", code)
	}
	if rec := f.setStatus(t, issue.ID, "resolved"); rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d", rec.Code)
	}
	if code := confirm(f.peer.ID, "linguist"); code != http.StatusNotFound {
		t.Errorf("a linguist who cannot see it: %d", code)
	}
	// An owner of another org sees all of their own org's issues, none of this one's.
	if code := f.asMemberWithRole(t, issuePath(issue.ID, "/confirm"), f.outsider.ID, f.other.ID, "owner", nil).Code; code != http.StatusNotFound {
		t.Errorf("another org's owner: %d", code)
	}
	if d := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(issue.ID, ""), nil)); d.Issue.ConfirmedAt != nil {
		t.Fatalf("refused confirmations confirmed it: %v", d.Issue.ConfirmedAt)
	}
	rec := f.asMemberWithRole(t, issuePath(issue.ID, "/confirm"), f.reporter.ID, f.tenant.ID, "linguist", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
	}
	d := decodeDetail(t, rec)
	last := d.Comments[len(d.Comments)-1]
	if d.Issue.ConfirmedAt == nil || d.Issue.Status != issues.StatusResolved || last.Change == nil ||
		last.Change.Field != "confirmed" || last.AuthorSide != "reporter" {
		t.Fatalf("after confirm: %+v / %+v", d.Issue, last)
	}
	if code := confirm(f.colleague.ID, "owner"); code != http.StatusBadRequest {
		t.Errorf("confirm twice: %d", code)
	}
	if op := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(issue.ID, ""), nil)); op.Issue.ConfirmedAt == nil {
		t.Error("the support team does not see the confirmation")
	}

	d = decodeDetail(t, f.asMember(t, issuePath(issue.ID, "/comments"), f.reporter.ID, f.tenant.ID,
		map[string]string{"body": "Still broken on large files."}))
	if d.Issue.Status != issues.StatusOpen || d.Issue.ConfirmedAt != nil {
		t.Fatalf("still broken: status %s, confirmed %v", d.Issue.Status, d.Issue.ConfirmedAt)
	}
	if rec := f.setStatus(t, issue.ID, "resolved"); rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d", rec.Code)
	}
	if d := decodeDetail(t, f.get(t, issuePath(issue.ID, ""), f.reporter.ID, f.tenant.ID)); d.Issue.ConfirmedAt != nil {
		t.Errorf("a second resolve kept the old confirmation")
	}
	if code := confirm(f.reporter.ID, "linguist"); code != http.StatusOK {
		t.Errorf("confirm the second fix: %d", code)
	}
}

// When an original becomes a duplicate itself, its duplicates follow it to the
// new original, each with an entry its reporter sees, so no duplicate points
// at another duplicate.
func TestIntegrationIssue_DuplicatesFollowTheirOriginal(t *testing.T) {
	f := newFixture(t)
	a := f.report(t, "Copy of a copy")
	b := f.report(t, "First copy")
	c := f.report(t, "The real one")
	if code := f.markDuplicate(t, a.ID, b.ID); code != http.StatusOK {
		t.Fatalf("A as a duplicate of B: %d", code)
	}
	if code := f.markDuplicate(t, b.ID, c.ID); code != http.StatusOK {
		t.Fatalf("B as a duplicate of C: %d", code)
	}
	d := decodeDetail(t, f.get(t, issuePath(a.ID, ""), f.reporter.ID, f.tenant.ID))
	if d.Issue.Status != issues.StatusDuplicate || d.Issue.DuplicateOf == nil || d.Issue.DuplicateOf.ID != c.ID {
		t.Fatalf("A after B moved: status %s, duplicate_of %+v", d.Issue.Status, d.Issue.DuplicateOf)
	}
	last := d.Comments[len(d.Comments)-1]
	if last.Change == nil || last.Change.Field != "duplicate" || last.Internal || last.Status != nil {
		t.Errorf("A's entry for the move = %+v %+v", last, last.Change)
	}
	op := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(a.ID, ""), nil))
	if opLast := op.Comments[len(op.Comments)-1]; opLast.Change == nil || opLast.Change.To == nil || *opLast.Change.To != itoa(c.ID) {
		t.Errorf("the support team's entry for the move = %+v", opLast.Change)
	}
	// C has no duplicate of a duplicate to follow: B stays on C, A too.
	if b2 := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(b.ID, ""), nil)); b2.Issue.DuplicateOf == nil || b2.Issue.DuplicateOf.ID != c.ID {
		t.Errorf("B = %+v", b2.Issue.DuplicateOf)
	}
}

// markDuplicateAsync marks id a duplicate of original as the operator, on its
// own goroutine, and sends the response code.
func (f *fixture) markDuplicateAsync(token string, id, original int64) <-chan int {
	return f.requestAsync(token, http.MethodPost, platformPath(id, "/status"),
		fmt.Sprintf(`{"status":"duplicate","duplicate_of":%d}`, original))
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// Two operators marking duplicates at the same time still leave no duplicate
// pointing at another. While A becomes a duplicate of B and its duplicate D
// is being moved onto B, a second operator marks X a duplicate of A: that
// request sees A as a duplicate and is refused.
func TestIntegrationIssue_ConcurrentDuplicatesNeverChain(t *testing.T) {
	f := newFixture(t)
	ctx := testutil.Ctx()
	a, b, x, d := f.report(t, "A"), f.report(t, "B"), f.report(t, "X"), f.report(t, "D")
	if code := f.markDuplicate(t, d.ID, a.ID); code != http.StatusOK {
		t.Fatalf("D as a duplicate of A: %d", code)
	}

	// Holding D's row stops the first request inside its transaction: A is
	// already a duplicate there, and D waits to move onto B.
	hold, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(ctx) //nolint:errcheck // no-op after the explicit rollback
	if _, err := hold.Exec(ctx, "SELECT 1 FROM issues WHERE id = $1 FOR UPDATE", d.ID); err != nil {
		t.Fatal(err)
	}
	token := f.operatorToken(t)
	first := f.markDuplicateAsync(token, a.ID, b.ID)
	waitFor(t, "A's move to wait on D", func() bool { return testutil.LockWaiters(t, f.pool) == 1 })
	second := f.markDuplicateAsync(token, x.ID, a.ID)
	waitFor(t, "X's request to finish or wait", func() bool { return len(second) == 1 || testutil.LockWaiters(t, f.pool) == 2 })
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	if code := <-first; code != http.StatusOK {
		t.Fatalf("A as a duplicate of B: %d", code)
	}
	if code := <-second; code != http.StatusBadRequest {
		t.Errorf("X as a duplicate of A, while A became a duplicate: %d, want 400", code)
	}
	var chained int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM issues i JOIN issues o ON o.id = i.duplicate_of_id
		WHERE i.status = 'duplicate' AND o.status = 'duplicate'`).Scan(&chained); err != nil {
		t.Fatal(err)
	}
	if chained != 0 {
		t.Errorf("%d duplicates point at another duplicate", chained)
	}
	if op := decodePlatformDetail(t, f.asOperator(t, http.MethodGet, platformPath(d.ID, ""), nil)); op.Issue.DuplicateOf == nil || op.Issue.DuplicateOf.ID != b.ID {
		t.Errorf("D after A moved = %+v, want B", op.Issue.DuplicateOf)
	}
}

// requestAsync sends a request as the operator on its own goroutine and sends
// the response code.
func (f *fixture) requestAsync(token, method, path, body string) <-chan int {
	done := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		done <- rec.Code
	}()
	return done
}

// A delete of an original holds the duplicate lock until its duplicates are
// reopened, so a request that marks another issue a duplicate of it meanwhile
// waits, then finds no original.
func TestIntegrationIssue_DeleteAndMarkDuplicateDoNotCross(t *testing.T) {
	f := newFixture(t)
	ctx := testutil.Ctx()
	a, d, x := f.report(t, "Original"), f.report(t, "Its duplicate"), f.report(t, "Marked meanwhile")
	if code := f.markDuplicate(t, d.ID, a.ID); code != http.StatusOK {
		t.Fatalf("D as a duplicate of A: %d", code)
	}

	// Holding D's row stops the delete inside its transaction: A is deleted
	// there, and D waits to be reopened.
	hold, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(ctx) //nolint:errcheck // no-op after the explicit rollback
	if _, err := hold.Exec(ctx, "SELECT 1 FROM issues WHERE id = $1 FOR UPDATE", d.ID); err != nil {
		t.Fatal(err)
	}
	token := f.operatorToken(t)
	deleted := f.requestAsync(token, http.MethodDelete, platformPath(a.ID, ""), "")
	waitFor(t, "the delete to wait on D", func() bool { return testutil.LockWaiters(t, f.pool) == 1 })
	marked := f.markDuplicateAsync(token, x.ID, a.ID)
	waitFor(t, "X's request to finish or wait", func() bool { return len(marked) == 1 || testutil.LockWaiters(t, f.pool) == 2 })
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	if code := <-deleted; code != http.StatusNoContent {
		t.Fatalf("delete A: %d", code)
	}
	if code := <-marked; code != http.StatusBadRequest {
		t.Errorf("X as a duplicate of A, while A was deleted: %d, want 400", code)
	}
	var orphans int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM issues i JOIN issues o ON o.id = i.duplicate_of_id
		WHERE i.deleted_at IS NULL AND o.deleted_at IS NOT NULL`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("%d live issues point at a deleted original", orphans)
	}
}
