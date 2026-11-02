package issues_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hibiken/asynq"

	"openloc/internal/issues"
)

// postedIssue files an issue and posts its card, so later posts have a
// thread.
func postedIssue(t *testing.T, f *fixture, svc *issues.Service, q *fakeQueue, n *issues.SlackNotifier, title string) issues.IssueView {
	t.Helper()
	issue, err := svc.Create(context.Background(), f.member(), newIssue(title))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	runTasks(t, q, issues.TaskSlackPost, n.HandleSlackPost)
	q.tasks = nil
	return issue
}

// Everything written on an issue goes in its card's thread under the name of
// whoever wrote it: a reply from the reporter's org, a reply or an internal
// note from the support team, and the note of a status change.
func TestIntegrationSlackThreadHasEveryReplyAndNote(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	svc, notifier := f.slackQueued(q, s)
	ctx := context.Background()
	issue := postedIssue(t, f, svc, q, notifier, "Export is broken")
	colleague := issues.Member{TenantID: f.tenant.ID, UserID: f.colleague.ID, Role: "owner"}

	for _, tc := range []struct {
		name string
		act  func() error
		want string
	}{
		{"the reporter replies", func() error {
			_, err := svc.MemberComment(ctx, f.member(), issue.ID, issues.NewComment{Body: "On DOCX only."})
			return err
		}, "*rep@test.com replied:*\n> On DOCX only."},
		{"a colleague of the reporter replies", func() error {
			_, err := svc.MemberComment(ctx, colleague, issue.ID, issues.NewComment{Body: "Same for me."})
			return err
		}, "*col@test.com replied:*\n> Same for me."},
		{"the support team replies", func() error {
			_, err := svc.PlatformComment(ctx, issues.Operator{UserID: f.operator.ID}, issue.ID, issues.NewComment{Body: "Looking into it."})
			return err
		}, "*ops@test.com replied:*\n> Looking into it."},
		{"the support team adds an internal note", func() error {
			_, err := svc.PlatformNote(ctx, issues.Operator{UserID: f.operator2.ID}, issue.ID, issues.NewComment{Body: "Seen this before."})
			return err
		}, "*ops2@test.com added an internal note:*\n> Seen this before."},
		{"a status change with a note", func() error {
			_, err := svc.PlatformSetStatus(ctx, issues.Operator{UserID: f.operator.ID}, issue.ID,
				issues.StatusChange{Status: issues.StatusNeedsInfo, Note: "Which file?"})
			return err
		}, "*ops@test.com changed the status to Needs info:*\n> Which file?"},
		{"a note that keeps the status", func() error {
			_, err := svc.PlatformSetStatus(ctx, issues.Operator{UserID: f.operator.ID}, issue.ID,
				issues.StatusChange{Status: issues.StatusNeedsInfo, Note: "Any update?"})
			return err
		}, "*ops@test.com replied:*\n> Any update?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q.tasks = nil
			before := len(s.posts)
			if err := tc.act(); err != nil {
				t.Fatalf("act: %v", err)
			}
			runTasks(t, q, issues.TaskSlackPost, notifier.HandleSlackPost)
			if len(s.posts) != before+1 {
				t.Fatalf("posted %d messages, want 1", len(s.posts)-before)
			}
			post := s.posts[before]
			if post.Channel != slackChannel || post.ThreadTS != "1700000000.000001" {
				t.Errorf("post = %+v, want it in the card's thread", post)
			}
			if post.Text != tc.want {
				t.Errorf("post = %q, want %q", post.Text, tc.want)
			}
		})
	}
}

// A status change without a note only refreshes the card: the thread gets no
// post.
func TestIntegrationSlackStatusWithoutANotePostsNothing(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	svc, notifier := f.slackQueued(q, s)
	issue := postedIssue(t, f, svc, q, notifier, "Quiet change")
	if _, err := svc.PlatformSetStatus(context.Background(), issues.Operator{UserID: f.operator.ID}, issue.ID,
		issues.StatusChange{Status: issues.StatusInProgress}); err != nil {
		t.Fatalf("status: %v", err)
	}
	runTasks(t, q, issues.TaskSlackPost, notifier.HandleSlackPost)
	if len(s.posts) != 1 {
		t.Errorf("posts = %d, want only the card", len(s.posts))
	}
}

// An internal note on an issue with no card in the channel is posted to the
// channel with a link, and still reads as internal.
func TestIntegrationSlackNoteOutsideTheThread(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	svc, _ := f.slackQueued(q, s)
	ctx := context.Background()
	issue, err := svc.Create(ctx, f.member(), newIssue("No card"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	q.tasks = nil
	if _, err := svc.PlatformNote(ctx, issues.Operator{UserID: f.operator.ID}, issue.ID, issues.NewComment{Body: "Check the logs."}); err != nil {
		t.Fatalf("note: %v", err)
	}
	runTasks(t, q, issues.TaskSlackPost, issues.NewSlackNotifier(f.q, s, slackChannel, "http://localhost").HandleSlackPost)
	want := fmt.Sprintf("*ops@test.com added an internal note on* <http://localhost/platform/issues/%d|#%d No card>\n> Check the logs.", issue.ID, issue.ID)
	if len(s.posts) != 1 || s.posts[0].ThreadTS != "" || s.posts[0].Text != want {
		t.Errorf("posts = %+v, want one top-level post %q", s.posts, want)
	}
}

// A reply task with no author id is the reporter's reply, posted under the
// reporter's name.
func TestIntegrationSlackReplyWithoutAnAuthor(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	svc, notifier := f.slackQueued(q, s)
	issue := postedIssue(t, f, svc, q, notifier, "Queued earlier")
	payload, err := json.Marshal(map[string]any{"issue_id": issue.ID, "kind": "issue-reply", "reply": "Any news?"})
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.HandleSlackPost(context.Background(), asynq.NewTask(issues.TaskSlackPost, payload)); err != nil {
		t.Fatalf("worker: %v", err)
	}
	if got := s.posts[len(s.posts)-1].Text; !strings.HasPrefix(got, "*rep@test.com replied:*") {
		t.Errorf("post = %q, want it under the reporter's name", got)
	}
}

// An author deactivated before the post is sent is "Someone" in the thread.
func TestIntegrationSlackPostOfADeactivatedAuthor(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	svc, notifier := f.slackQueued(q, s)
	ctx := context.Background()
	issue := postedIssue(t, f, svc, q, notifier, "Left the org")
	colleague := issues.Member{TenantID: f.tenant.ID, UserID: f.colleague.ID, Role: "owner"}
	if _, err := svc.MemberComment(ctx, colleague, issue.ID, issues.NewComment{Body: "Still broken."}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE users SET is_active = false WHERE id = $1", f.colleague.ID); err != nil {
		t.Fatal(err)
	}
	runTasks(t, q, issues.TaskSlackPost, notifier.HandleSlackPost)
	if got := s.posts[len(s.posts)-1].Text; got != "*Someone replied:*\n> Still broken." {
		t.Errorf("post = %q, want it under Someone", got)
	}
}
