package issues_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hibiken/asynq"

	"openloc/internal/issues"
)

// cardFixture is a service whose only notice of an issue is Slack, with the
// fixture's operator on the support team, and one issue already posted.
type cardFixture struct {
	*fixture
	q        *fakeQueue
	slack    *fakeSlack
	svc      *issues.Service
	notifier *issues.SlackNotifier
	issue    issues.IssueView
}

func newCardFixture(t *testing.T) *cardFixture {
	t.Helper()
	f := newFixture(t)
	c := &cardFixture{fixture: f, q: &fakeQueue{}, slack: &fakeSlack{}}
	c.svc = issues.NewService(f.pool, nil, nil, nil, []string{operatorEmail}, "http://localhost").
		WithEmailQueue(c.q).WithSlack(c.slack, slackChannel)
	c.notifier = issues.NewSlackNotifier(f.q, c.slack, slackChannel, "http://localhost")
	issue, err := c.svc.Create(context.Background(), f.member(), newIssue("Card"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	c.issue = issue
	c.work(t)
	if len(c.slack.posts) != 1 {
		t.Fatalf("posts = %d, want the new-issue card", len(c.slack.posts))
	}
	return c
}

// work runs the queued Slack tasks and clears the queue.
func (c *cardFixture) work(t *testing.T) int {
	t.Helper()
	n := runTasks(t, c.q, issues.TaskSlackPost, c.notifier.HandleSlackPost)
	c.q.tasks = nil
	return n
}

// card is the card's text after the last edit; it fails unless the edit
// went to the card itself.
func (c *cardFixture) card(t *testing.T) string {
	t.Helper()
	if len(c.slack.updates) == 0 {
		t.Fatal("the card was never edited")
	}
	u := c.slack.updates[len(c.slack.updates)-1]
	if u.channel != slackChannel || u.ts != "1700000000.000001" {
		t.Fatalf("edited %s/%s, want the card %s/1700000000.000001", u.channel, u.ts, slackChannel)
	}
	return u.text
}

func (c *cardFixture) op() issues.Operator { return issues.Operator{UserID: c.operator.ID} }

// A status change rewrites the card's status line; the rest stays. Its note,
// if any, goes in the thread.
func TestIntegrationSlackCardFollowsTheStatus(t *testing.T) {
	c := newCardFixture(t)
	ctx := context.Background()
	for _, tc := range []struct{ status, note, want string }{
		{issues.StatusInProgress, "", ":large_yellow_circle: *In progress*"},
		{issues.StatusNeedsInfo, "Which browser?", ":question: *Needs info*"},
		{issues.StatusResolved, "", ":white_check_mark: *Resolved*"},
	} {
		if _, err := c.svc.PlatformSetStatus(ctx, c.op(), c.issue.ID, issues.StatusChange{Status: tc.status, Note: tc.note}); err != nil {
			t.Fatalf("set %s: %v", tc.status, err)
		}
		want := 1
		if tc.note != "" {
			want = 2
		}
		if n := c.work(t); n != want {
			t.Fatalf("%s: queued %d slack tasks, want the refresh and one post per note (%d)", tc.status, n, want)
		}
		card := c.card(t)
		if !strings.HasPrefix(card, tc.want) {
			t.Errorf("card after %s starts %q, want %q", tc.status, card[:min(len(card), 40)], tc.want)
		}
		if !strings.Contains(card, fmt.Sprintf("|#%d Card>", c.issue.ID)) || !strings.Contains(card, "*Organization:* Acme Org") {
			t.Errorf("card lost its link or details: %q", card)
		}
	}
	if len(c.slack.posts) != 2 {
		t.Errorf("posts = %d, want the card and the one note: a status change edits the card", len(c.slack.posts))
	}
}

// "Still broken": the reporter's reply to a resolved issue reopens it, so the
// card is open again. A reply that changes no status leaves the card alone.
func TestIntegrationSlackCardReopensOnTheReportersReply(t *testing.T) {
	c := newCardFixture(t)
	ctx := context.Background()
	if _, err := c.svc.MemberComment(ctx, c.member(), c.issue.ID, issues.NewComment{Body: "Also on PDF."}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	c.work(t)
	if len(c.slack.updates) != 0 {
		t.Errorf("a reply to an open issue edited the card: %+v", c.slack.updates)
	}

	if _, err := c.svc.PlatformSetStatus(ctx, c.op(), c.issue.ID, issues.StatusChange{Status: issues.StatusResolved}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	c.work(t)
	if _, err := c.svc.MemberComment(ctx, c.member(), c.issue.ID, issues.NewComment{Body: "Still broken."}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	c.work(t)
	if card := c.card(t); !strings.HasPrefix(card, ":large_blue_circle: *Open*") {
		t.Errorf("card after the reopening reply = %q, want it open", card)
	}
}

// Confirming the fix marks the card.
func TestIntegrationSlackCardShowsTheConfirmedFix(t *testing.T) {
	c := newCardFixture(t)
	ctx := context.Background()
	if _, err := c.svc.PlatformSetStatus(ctx, c.op(), c.issue.ID, issues.StatusChange{Status: issues.StatusResolved}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	c.work(t)
	if _, err := c.svc.Confirm(ctx, c.member(), c.issue.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	c.work(t)
	if card := c.card(t); !strings.HasPrefix(card, ":white_check_mark: *Resolved* (fix confirmed)") {
		t.Errorf("card = %q, want the fix confirmed", card)
	}
}

// The triage the team sets shows on the card: assignee and kind.
func TestIntegrationSlackCardShowsTheTriage(t *testing.T) {
	c := newCardFixture(t)
	kind, area := "enhancement", "tm"
	if _, err := c.svc.PlatformUpdate(context.Background(), c.op(), c.issue.ID, issues.IssueUpdate{
		Kind: &kind, Area: &area, AssigneeSet: true, AssigneeID: &c.operator.ID,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	c.work(t)
	card := c.card(t)
	for _, want := range []string{"*Kind:* Enhancement", "*Area:* Translation memory", "*Assignee:* " + operatorEmail} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q: %q", want, card)
		}
	}
}

// A deleted issue's card keeps nothing of the report: no title, no text,
// whether its org or the support team deleted it.
func TestIntegrationSlackCardOfADeletedIssue(t *testing.T) {
	for _, tc := range []struct {
		name string
		del  func(c *cardFixture) error
	}{
		{"member", func(c *cardFixture) error { return c.svc.Delete(context.Background(), c.member(), c.issue.ID) }},
		{"operator", func(c *cardFixture) error { return c.svc.PlatformDelete(context.Background(), c.issue.ID) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCardFixture(t)
			if err := tc.del(c); err != nil {
				t.Fatalf("delete: %v", err)
			}
			c.work(t)
			if card := c.card(t); card != fmt.Sprintf(":wastebasket: ~#%d~ deleted", c.issue.ID) {
				t.Errorf("card = %q", card)
			}
		})
	}
}

// The org's own kind change shows on the card too.
func TestIntegrationSlackCardFollowsTheReportersKindChange(t *testing.T) {
	c := newCardFixture(t)
	if _, err := c.svc.ChangeKind(context.Background(), c.member(), c.issue.ID, issues.KindChange{Kind: "feature_request"}); err != nil {
		t.Fatalf("change kind: %v", err)
	}
	c.work(t)
	if card := c.card(t); !strings.Contains(card, "*Kind:* Feature request") {
		t.Errorf("card = %q, want the new kind", card)
	}
}

// lastUpdate is the newest text the card ts was edited to, "" when none.
func lastUpdate(s *fakeSlack, ts string) string {
	for i := len(s.updates) - 1; i >= 0; i-- {
		if s.updates[i].ts == ts {
			return s.updates[i].text
		}
	}
	return ""
}

// Deleting an original sends its duplicates back to the queue, and their cards
// say so.
func TestIntegrationSlackCardOfAReopenedDuplicate(t *testing.T) {
	c := newCardFixture(t)
	ctx := context.Background()
	dup, err := c.svc.Create(ctx, c.member(), newIssue("Same problem"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	c.work(t)
	if _, err := c.svc.PlatformSetStatus(ctx, c.op(), dup.ID, issues.StatusChange{Status: issues.StatusDuplicate, DuplicateOf: &c.issue.ID}); err != nil {
		t.Fatalf("mark duplicate: %v", err)
	}
	c.work(t)
	if card := lastUpdate(c.slack, "1700000000.000002"); !strings.HasPrefix(card, ":link: *Duplicate*") {
		t.Fatalf("duplicate's card = %q, want it marked duplicate", card)
	}
	if err := c.svc.Delete(ctx, c.member(), c.issue.ID); err != nil {
		t.Fatalf("delete original: %v", err)
	}
	c.work(t)
	if card := lastUpdate(c.slack, "1700000000.000002"); !strings.HasPrefix(card, ":large_blue_circle: *Open*") {
		t.Errorf("reopened duplicate's card = %q, want it open", card)
	}
}

// An issue filed before Slack was set up has no card: a status change edits
// and posts nothing.
func TestIntegrationSlackRefreshWithoutACard(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	ctx := context.Background()
	plain := issues.NewService(f.pool, nil, nil, nil, []string{operatorEmail}, "http://localhost").WithEmailQueue(q)
	issue, err := plain.Create(ctx, f.member(), newIssue("Before slack"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	svc := issues.NewService(f.pool, nil, nil, nil, []string{operatorEmail}, "http://localhost").
		WithEmailQueue(q).WithSlack(s, slackChannel)
	if _, err := svc.PlatformSetStatus(ctx, issues.Operator{UserID: f.operator.ID}, issue.ID, issues.StatusChange{Status: issues.StatusInProgress}); err != nil {
		t.Fatalf("set status: %v", err)
	}
	runTasks(t, q, issues.TaskSlackPost, issues.NewSlackNotifier(f.q, s, slackChannel, "http://localhost").HandleSlackPost)
	if len(s.posts) != 0 || len(s.updates) != 0 {
		t.Errorf("posts %+v, updates %+v, want none", s.posts, s.updates)
	}
}

// A reply on an issue that has no card goes to the channel with a link. It
// never becomes the card, so a later status change edits nothing.
func TestIntegrationSlackReplyOnAnIssueWithoutACard(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	ctx := context.Background()
	plain := issues.NewService(f.pool, nil, nil, nil, []string{operatorEmail}, "http://localhost").WithEmailQueue(q)
	issue, err := plain.Create(ctx, f.member(), newIssue("Before slack"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	q.tasks = nil
	svc := issues.NewService(f.pool, nil, nil, nil, []string{operatorEmail}, "http://localhost").
		WithEmailQueue(q).WithSlack(s, slackChannel)
	if _, err := svc.MemberComment(ctx, f.member(), issue.ID, issues.NewComment{Body: "Still there."}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if _, err := svc.PlatformSetStatus(ctx, issues.Operator{UserID: f.operator.ID}, issue.ID, issues.StatusChange{Status: issues.StatusInProgress}); err != nil {
		t.Fatalf("set status: %v", err)
	}
	runTasks(t, q, issues.TaskSlackPost, issues.NewSlackNotifier(f.q, s, slackChannel, "http://localhost").HandleSlackPost)
	if len(s.posts) != 1 || !strings.Contains(s.posts[0].Text, "replied on*") {
		t.Errorf("posts = %+v, want the reply posted with a link", s.posts)
	}
	if len(s.updates) != 0 {
		t.Errorf("updates = %+v: the reply was edited as if it were the card", s.updates)
	}
}

// Changes in quick succession share one delayed card refresh, which shows
// the issue as it is when it runs. A refresh refused as a twin is not sent
// from the request either.
func TestIntegrationSlackCardRefreshesAreMerged(t *testing.T) {
	c := newCardFixture(t)
	ctx := context.Background()
	if _, err := c.svc.PlatformSetStatus(ctx, c.op(), c.issue.ID, issues.StatusChange{Status: issues.StatusInProgress}); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if _, err := c.svc.PlatformUpdate(ctx, c.op(), c.issue.ID, issues.IssueUpdate{AssigneeSet: true, AssigneeID: &c.operator.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := c.svc.PlatformSetStatus(ctx, c.op(), c.issue.ID, issues.StatusChange{Status: issues.StatusResolved}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(c.q.tasks) != 1 {
		t.Fatalf("queued %d refreshes for three quick changes, want 1", len(c.q.tasks))
	}
	if !hasOption(c.q.opts[c.q.tasks[0]], asynq.ProcessInOpt) {
		t.Error("the refresh runs at once; it should wait for the changes that follow")
	}
	if len(c.slack.updates) != 0 {
		t.Fatalf("updates = %+v: a merged refresh went out from the request", c.slack.updates)
	}
	c.work(t)
	card := c.card(t)
	if !strings.HasPrefix(card, ":white_check_mark: *Resolved*") || !strings.Contains(card, "*Assignee:* "+operatorEmail) {
		t.Errorf("card = %q, want the state after the last change", card)
	}
	if len(c.slack.updates) != 1 {
		t.Errorf("updates = %d, want 1", len(c.slack.updates))
	}
}

// A soft delete also queues the card for the sweep, so it is blanked even
// when a refresh that read the issue before the delete writes it back.
func TestIntegrationSlackSoftDeleteQueuesTheBlank(t *testing.T) {
	c := newCardFixture(t)
	ctx := context.Background()
	if err := c.svc.Delete(ctx, c.member(), c.issue.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := c.queuedBlanks(t); n != 1 {
		t.Fatalf("queued blanks = %d after the delete, want the card", n)
	}
	s := &fakeSlack{}
	if n, err := issues.NewSlackBlankSweeper(c.fixture.q, s).Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	if want := fmt.Sprintf(":wastebasket: ~#%d~ deleted", c.issue.ID); len(s.updates) != 1 || s.updates[0].text != want {
		t.Errorf("updates = %+v, want the card blanked", s.updates)
	}
}

// Names keep to one line on the card and in a reply's header, so none can
// add a line that looks like the card's own.
func TestIntegrationSlackNamesStayOnOneLine(t *testing.T) {
	c := newCardFixture(t)
	ctx := context.Background()
	const fake = "\n:white_check_mark: *Resolved*"
	for _, stmt := range []struct {
		sql string
		id  int64
	}{
		{"UPDATE tenants SET name = name || $2 WHERE id = $1", c.tenant.ID},
		{"UPDATE users SET name = name || $2 WHERE id = $1", c.reporter.ID},
		{"UPDATE users SET name = name || $2 WHERE id = $1", c.operator.ID},
	} {
		if _, err := c.pool.Exec(ctx, stmt.sql, stmt.id, fake); err != nil {
			t.Fatalf("seed name: %v", err)
		}
	}
	if _, err := c.svc.PlatformUpdate(ctx, c.op(), c.issue.ID, issues.IssueUpdate{AssigneeSet: true, AssigneeID: &c.operator.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := c.svc.MemberComment(ctx, c.member(), c.issue.ID, issues.NewComment{Body: "More."}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	c.work(t)
	reply := c.slack.posts[len(c.slack.posts)-1].Text
	for name, text := range map[string]string{"card": c.card(t), "reply": reply} {
		if strings.Contains(text, "\n:white_check_mark:") {
			t.Errorf("a name added a line to the %s: %q", name, text)
		}
	}
	if !strings.Contains(c.card(t), "*Organization:* Acme Org :white_check_mark: *Resolved*") {
		t.Errorf("card = %q, want the org name kept on its line", c.card(t))
	}
}
