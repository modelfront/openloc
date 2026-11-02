package issues_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hibiken/asynq"

	"openloc/internal/issues"
	"openloc/internal/slack"
)

const slackChannel = "C0ISSUES"

// fakeSlack records the posts and edits, answers each post with the next
// ts, or fails with err; errFor fails the edits of one ts only.
type fakeSlack struct {
	posts   []slack.Message
	updates []slackUpdate
	err     error
	errFor  map[string]error
}

type slackUpdate struct{ channel, ts, text string }

func (s *fakeSlack) PostMessage(_ context.Context, m slack.Message) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	s.posts = append(s.posts, m)
	return fmt.Sprintf("1700000000.%06d", len(s.posts)), nil
}

func (s *fakeSlack) UpdateMessage(_ context.Context, channel, ts, text string) error {
	if s.err != nil {
		return s.err
	}
	if err := s.errFor[ts]; err != nil {
		return err
	}
	s.updates = append(s.updates, slackUpdate{channel, ts, text})
	return nil
}

// slackQueued is a service that posts to Slack and emails the operators
// through q, and the worker's Slack notifier.
func (f *fixture) slackQueued(q *fakeQueue, s *fakeSlack) (*issues.Service, *issues.SlackNotifier) {
	svc, _, _ := f.queued(q)
	svc.WithSlack(s, slackChannel)
	return svc, issues.NewSlackNotifier(f.q, s, slackChannel, "http://localhost")
}

// runTasks runs every queued task of type typ through handle.
func runTasks(t *testing.T, q *fakeQueue, typ string, handle func(context.Context, *asynq.Task) error) int {
	t.Helper()
	n := 0
	for _, task := range q.tasks {
		if task.Type() != typ {
			continue
		}
		if err := handle(context.Background(), task); err != nil {
			t.Fatalf("worker %s: %v", typ, err)
		}
		n++
	}
	return n
}

// A report queues one Slack post besides the emails; the worker posts it to
// the channel, and the reporter's reply then goes in its thread.
func TestIntegrationSlackPostsNewIssueAndThreadsTheReply(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	svc, notifier := f.slackQueued(q, s)
	ctx := context.Background()

	issue, err := svc.Create(ctx, f.member(), newIssue("Export is broken"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(s.posts) != 0 {
		t.Fatalf("the request posted %d messages itself", len(s.posts))
	}
	if n := runTasks(t, q, issues.TaskSlackPost, notifier.HandleSlackPost); n != 1 {
		t.Fatalf("queued %d slack posts, want 1", n)
	}
	if n := runTasks(t, q, issues.TaskOperatorEmail, func(context.Context, *asynq.Task) error { return nil }); n != 0 {
		t.Errorf("queued %d operator emails for a new issue, want none: Slack is the support team's notice", n)
	}
	if len(s.posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(s.posts))
	}
	first := s.posts[0]
	if first.Channel != slackChannel || first.ThreadTS != "" {
		t.Errorf("new-issue post = %+v, want a top-level post to %s", first, slackChannel)
	}
	for _, want := range []string{
		fmt.Sprintf("<http://localhost/platform/issues/%d|#%d Export is broken>", issue.ID, issue.ID),
		":large_blue_circle: *Open*", "*Organization:* Acme Org", "*Kind:* Bug (Major)", "*Area:* Editor",
		"> The export button does nothing.",
	} {
		if !strings.Contains(first.Text, want) {
			t.Errorf("new-issue post lacks %q:\n%s", want, first.Text)
		}
	}

	q.tasks = nil
	if _, err := svc.MemberComment(ctx, f.member(), issue.ID, issues.NewComment{Body: "It happens on DOCX only."}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if n := runTasks(t, q, issues.TaskSlackPost, notifier.HandleSlackPost); n != 1 {
		t.Fatalf("queued %d slack posts for the reply, want 1", n)
	}
	reply := s.posts[1]
	if reply.ThreadTS != "1700000000.000001" || reply.Channel != slackChannel {
		t.Errorf("reply post = %+v, want it in the new-issue post's thread", reply)
	}
	if !strings.Contains(reply.Text, "replied:*\n> It happens on DOCX only.") {
		t.Errorf("reply post = %q", reply.Text)
	}
}

// Without a queue, or when it refuses the task, the request posts itself.
func TestIntegrationSlackPostsInlineWhenTheQueueFails(t *testing.T) {
	f := newFixture(t)
	s := &fakeSlack{}
	svc, _ := f.slackQueued(&fakeQueue{err: errors.New("redis down")}, s)
	if _, err := svc.Create(context.Background(), f.member(), newIssue("Inline")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(s.posts) != 1 || !strings.Contains(s.posts[0].Text, "Inline") {
		t.Errorf("posts = %+v, want the new-issue post sent inline", s.posts)
	}
}

// A failing Slack never fails the report.
func TestIntegrationSlackFailureDoesNotFailTheReport(t *testing.T) {
	f := newFixture(t)
	svc, _ := f.slackQueued(&fakeQueue{err: errors.New("redis down")}, &fakeSlack{err: errors.New("slack down")})
	if _, err := svc.Create(context.Background(), f.member(), newIssue("Still filed")); err != nil {
		t.Fatalf("create = %v, want the issue filed", err)
	}
}

// A reply to an issue whose first post went to another channel, or that has
// none, is posted to the current channel with a link to the issue.
func TestIntegrationSlackReplyOutsideTheThread(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	svc, _ := f.slackQueued(q, s)
	ctx := context.Background()
	issue, err := svc.Create(ctx, f.member(), newIssue("Moved channel"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	old := issues.NewSlackNotifier(f.q, s, "C0OLD", "http://localhost")
	runTasks(t, q, issues.TaskSlackPost, old.HandleSlackPost)

	q.tasks = nil
	if _, err := svc.MemberComment(ctx, f.member(), issue.ID, issues.NewComment{Body: "Any news?"}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	current := issues.NewSlackNotifier(f.q, s, slackChannel, "http://localhost")
	runTasks(t, q, issues.TaskSlackPost, current.HandleSlackPost)
	reply := s.posts[len(s.posts)-1]
	if reply.Channel != slackChannel || reply.ThreadTS != "" {
		t.Errorf("reply = %+v, want a top-level post to the current channel", reply)
	}
	if !strings.Contains(reply.Text, fmt.Sprintf("|#%d Moved channel>", issue.ID)) {
		t.Errorf("reply lacks the issue link: %q", reply.Text)
	}
}

// A refusal a retry cannot fix ends the task; anything else is retried.
func TestIntegrationSlackFailureRetries(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	svc, _ := f.slackQueued(q, &fakeSlack{})
	if _, err := svc.Create(context.Background(), f.member(), newIssue("Retry")); err != nil {
		t.Fatalf("create: %v", err)
	}
	var task *asynq.Task
	for _, tk := range q.tasks {
		if tk.Type() == issues.TaskSlackPost {
			task = tk
		}
	}
	for _, tc := range []struct {
		err       error
		skipRetry bool
	}{
		{&slack.APIError{Code: "channel_not_found"}, true},
		{&slack.APIError{Code: "ratelimited"}, false},
		{errors.New("connection reset"), false},
	} {
		n := issues.NewSlackNotifier(f.q, &fakeSlack{err: tc.err}, slackChannel, "http://localhost")
		err := n.HandleSlackPost(context.Background(), task)
		if err == nil {
			t.Errorf("%v: worker = nil, want the error", tc.err)
			continue
		}
		if got := errors.Is(err, asynq.SkipRetry); got != tc.skipRetry {
			t.Errorf("%v: skip retry = %v, want %v", tc.err, got, tc.skipRetry)
		}
	}
}

// An issue deleted before the worker gets to it posts nothing.
func TestIntegrationSlackSkipsDeletedIssue(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	svc, notifier := f.slackQueued(q, s)
	ctx := context.Background()
	issue, err := svc.Create(ctx, f.member(), newIssue("Gone before posting"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Delete(ctx, f.member(), issue.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	runTasks(t, q, issues.TaskSlackPost, notifier.HandleSlackPost)
	if len(s.posts) != 0 {
		t.Errorf("posted %+v for a deleted issue", s.posts)
	}
}

// What a reporter types cannot ping the channel, forge a link, or add a line
// to the card.
func TestIntegrationSlackEscapesReporterText(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	s := &fakeSlack{}
	svc, notifier := f.slackQueued(q, s)
	in := newIssue("<!channel> urgent\n:white_check_mark: *Resolved*")
	in.Description = "See <https://evil.example|the docs> & reply"
	if _, err := svc.Create(context.Background(), f.member(), in); err != nil {
		t.Fatalf("create: %v", err)
	}
	runTasks(t, q, issues.TaskSlackPost, notifier.HandleSlackPost)
	text := s.posts[0].Text
	if strings.Contains(text, "<!channel>") || strings.Contains(text, "<https://evil.example") {
		t.Errorf("post carries live markup: %q", text)
	}
	if strings.Contains(text, "\n:white_check_mark:") {
		t.Errorf("the title added a line to the card: %q", text)
	}
	if !strings.Contains(text, "&lt;!channel&gt; urgent :white_check_mark:") || !strings.Contains(text, "&amp; reply") {
		t.Errorf("post lost the escaped text: %q", text)
	}
}
