package issues_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hibiken/asynq"

	"openloc/internal/emaillayout"
	"openloc/internal/issues"
	"openloc/internal/notification"
)

// fakeQueue records the tasks a request would hand to the worker, and their
// options.
type fakeQueue struct {
	tasks []*asynq.Task
	opts  map[*asynq.Task][]asynq.Option
	err   error
}

// EnqueueTask refuses a Unique task whose twin is still queued, with the
// error the worker's enqueuer returns for it.
func (q *fakeQueue) EnqueueTask(t *asynq.Task, opts ...asynq.Option) error {
	if q.err != nil {
		return q.err
	}
	if hasOption(opts, asynq.UniqueOpt) {
		for _, queued := range q.tasks {
			if queued.Type() == t.Type() && bytes.Equal(queued.Payload(), t.Payload()) {
				return fmt.Errorf("enqueue %s task: %w", t.Type(), asynq.ErrDuplicateTask)
			}
		}
	}
	if q.opts == nil {
		q.opts = map[*asynq.Task][]asynq.Option{}
	}
	q.tasks = append(q.tasks, t)
	q.opts[t] = opts
	return nil
}

func hasOption(opts []asynq.Option, kind asynq.OptionType) bool {
	for _, o := range opts {
		if o.Type() == kind {
			return true
		}
	}
	return false
}

type failingSender struct{}

func (failingSender) SendEmail(context.Context, string, string, emaillayout.Email) error {
	return errors.New("smtp down")
}

// rejectingSender fails the way a mail server does: its reply quotes the
// recipient.
type rejectingSender struct{}

func (rejectingSender) SendEmail(_ context.Context, to, _ string, _ emaillayout.Email) error {
	return fmt.Errorf("550 5.1.1 <%s>: Recipient address rejected", to)
}

var queueOperators = []string{operatorEmail, operator2Email}

// queued is a service whose assignee emails go through q, the outbox that
// records what the worker sends, and the worker's mailer. Both fixture
// operators are on the support team.
func (f *fixture) queued(q *fakeQueue) (*issues.Service, *notification.Outbox, *issues.OperatorMailer) {
	outbox := notification.NewOutbox(50)
	sender := &notification.RecordingSender{Next: &notification.ConsoleSender{}, Outbox: outbox}
	svc := issues.NewService(f.pool, nil, nil, sender, queueOperators, "http://localhost").WithEmailQueue(q)
	return svc, outbox, issues.NewOperatorMailer(f.q, sender, "http://localhost", queueOperators)
}

func (f *fixture) member() issues.Member {
	return issues.Member{TenantID: f.tenant.ID, UserID: f.reporter.ID, Role: "linguist"}
}

func newIssue(title string) issues.NewIssue {
	return issues.NewIssue{Kind: "bug", Severity: "major", Area: "editor", Title: title,
		Description: "The export button does nothing.", Context: "{}"}
}

// sendQueued runs every queued email task through mailer and clears the
// queue.
func sendQueued(t *testing.T, q *fakeQueue, mailer *issues.OperatorMailer) int {
	t.Helper()
	n := 0
	for _, task := range q.tasks {
		if task.Type() != issues.TaskOperatorEmail {
			continue
		}
		if err := mailer.HandleOperatorEmail(context.Background(), task); err != nil {
			t.Fatalf("worker: %v", err)
		}
		n++
	}
	q.tasks = nil
	return n
}

// assigned files an issue and assigns it to the fixture's first operator, who
// assigns it to themself. Neither step emails anyone.
func (f *fixture) assigned(t *testing.T, svc *issues.Service, q *fakeQueue, outbox *notification.Outbox, mailer *issues.OperatorMailer) issues.IssueView {
	t.Helper()
	ctx := context.Background()
	issue, err := svc.Create(ctx, f.member(), newIssue("Export is broken"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.PlatformUpdate(ctx, issues.Operator{UserID: f.operator.ID}, issue.ID,
		issues.IssueUpdate{AssigneeSet: true, AssigneeID: &f.operator.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	sendQueued(t, q, mailer)
	for _, to := range queueOperators {
		if got := subjects(outbox, to); len(got) != 0 {
			t.Fatalf("%s got %v for a new issue and its assignment, want nothing", to, got)
		}
	}
	return issue
}

// A new issue emails nobody on the support team.
func TestIntegrationNewIssueEmailsNoOperator(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	svc, outbox, mailer := f.queued(q)
	if _, err := svc.Create(context.Background(), f.member(), newIssue("Quiet report")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if n := sendQueued(t, q, mailer); n != 0 {
		t.Errorf("queued %d operator emails for a new issue, want none", n)
	}
	for _, to := range queueOperators {
		if got := subjects(outbox, to); len(got) != 0 {
			t.Errorf("%s got %v", to, got)
		}
	}
}

// Whatever someone else does on an assigned issue, the assignee gets one
// email saying who did what, and nobody else on the team gets one.
func TestIntegrationAssigneeEmailForEachAction(t *testing.T) {
	high, kind := "high", issues.KindChange{Kind: "feature_request"}
	for _, tc := range []struct {
		name  string
		act   func(f *fixture, svc *issues.Service, id int64) error
		wants []string
	}{
		{"reporter reply", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.MemberComment(context.Background(), f.member(), id, issues.NewComment{Body: "It happens on DOCX only."})
			return err
		}, []string{"rep@test.com replied", ">Reply</div>", "It happens on DOCX only."}},
		{"colleague reply", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.MemberComment(context.Background(), issues.Member{TenantID: f.tenant.ID, UserID: f.colleague.ID, Role: "owner"},
				id, issues.NewComment{Body: "Same for me."})
			return err
		}, []string{"col@test.com replied", "Same for me."}},
		{"operator comment", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.PlatformComment(context.Background(), issues.Operator{UserID: f.operator2.ID}, id, issues.NewComment{Body: "Looking into it."})
			return err
		}, []string{"ops2@test.com replied to the reporter", "Looking into it."}},
		{"internal note", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.PlatformNote(context.Background(), issues.Operator{UserID: f.operator2.ID}, id, issues.NewComment{Body: "Seen this before."})
			return err
		}, []string{"ops2@test.com added an internal note", ">Note</div>", "Seen this before."}},
		{"status change", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.PlatformSetStatus(context.Background(), issues.Operator{UserID: f.operator2.ID}, id,
				issues.StatusChange{Status: issues.StatusNeedsInfo, Note: "Which file?"})
			return err
		}, []string{"ops2@test.com changed the status to Needs info", "Which file?"}},
		{"edit", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.PlatformUpdate(context.Background(), issues.Operator{UserID: f.operator2.ID}, id, issues.IssueUpdate{Priority: &high})
			return err
		}, []string{"ops2@test.com edited the issue", "Changed: Priority"}},
		{"org kind change", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.ChangeKind(context.Background(), f.member(), id, kind)
			return err
		}, []string{"rep@test.com changed the kind to Feature request"}},
		{"confirmed fix", func(f *fixture, svc *issues.Service, id int64) error {
			if _, err := svc.PlatformSetStatus(context.Background(), issues.Operator{UserID: f.operator.ID}, id,
				issues.StatusChange{Status: issues.StatusResolved}); err != nil {
				return err
			}
			_, err := svc.Confirm(context.Background(), f.member(), id)
			return err
		}, []string{"rep@test.com confirmed the fix"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			q := &fakeQueue{}
			svc, outbox, mailer := f.queued(q)
			issue := f.assigned(t, svc, q, outbox, mailer)
			if err := tc.act(f, svc, issue.ID); err != nil {
				t.Fatalf("act: %v", err)
			}
			if got := subjects(outbox, operatorEmail); len(got) != 0 {
				t.Fatalf("the request sent %v itself; the worker sends it", got)
			}
			sendQueued(t, q, mailer)
			got := outbox.For(operatorEmail)
			if len(got) != 1 {
				t.Fatalf("assignee got %d emails, want 1", len(got))
			}
			if want := fmt.Sprintf("Issue #%d: Export is broken", issue.ID); got[0].Subject != want {
				t.Errorf("subject = %q, want %q", got[0].Subject, want)
			}
			assertEmail(t, tc.name, got[0].Body, append(tc.wants, emaillayout.HeaderMarker, ">Organization</td>",
				fmt.Sprintf("http://localhost/platform/issues/%d", issue.ID), ">Open the issue</a>")...)
			if others := subjects(outbox, operator2Email); len(others) != 0 {
				t.Errorf("another operator got %v; only the assignee is emailed", others)
			}
		})
	}
}

// No email when nobody else acted on an assigned issue: the assignee acted,
// or the change is a reassignment, or the issue is not assigned.
func TestIntegrationNoAssigneeEmail(t *testing.T) {
	high := "high"
	for _, tc := range []struct {
		name string
		act  func(f *fixture, svc *issues.Service, id int64) error
	}{
		{"assignee's own comment", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.PlatformComment(context.Background(), issues.Operator{UserID: f.operator.ID}, id, issues.NewComment{Body: "On it."})
			return err
		}},
		{"assignee's own note", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.PlatformNote(context.Background(), issues.Operator{UserID: f.operator.ID}, id, issues.NewComment{Body: "Mine."})
			return err
		}},
		{"assignee's own status change", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.PlatformSetStatus(context.Background(), issues.Operator{UserID: f.operator.ID}, id, issues.StatusChange{Status: issues.StatusInProgress})
			return err
		}},
		{"assignee's own edit", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.PlatformUpdate(context.Background(), issues.Operator{UserID: f.operator.ID}, id, issues.IssueUpdate{Priority: &high})
			return err
		}},
		{"another operator takes the issue with an edit", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.PlatformUpdate(context.Background(), issues.Operator{UserID: f.operator2.ID}, id,
				issues.IssueUpdate{Priority: &high, AssigneeSet: true, AssigneeID: &f.operator2.ID})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			q := &fakeQueue{}
			svc, outbox, mailer := f.queued(q)
			issue := f.assigned(t, svc, q, outbox, mailer)
			if err := tc.act(f, svc, issue.ID); err != nil {
				t.Fatalf("act: %v", err)
			}
			if n := sendQueued(t, q, mailer); n != 0 {
				t.Errorf("queued %d emails, want none", n)
			}
			for _, to := range queueOperators {
				if got := subjects(outbox, to); len(got) != 0 {
					t.Errorf("%s got %v", to, got)
				}
			}
		})
	}

	t.Run("unassigned issue", func(t *testing.T) {
		f := newFixture(t)
		q := &fakeQueue{}
		svc, outbox, mailer := f.queued(q)
		issue, err := svc.Create(context.Background(), f.member(), newIssue("Nobody's yet"))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := svc.MemberComment(context.Background(), f.member(), issue.ID, issues.NewComment{Body: "Hello?"}); err != nil {
			t.Fatalf("comment: %v", err)
		}
		if _, err := svc.PlatformNote(context.Background(), issues.Operator{UserID: f.operator2.ID}, issue.ID, issues.NewComment{Body: "Later."}); err != nil {
			t.Fatalf("note: %v", err)
		}
		if n := sendQueued(t, q, mailer); n != 0 {
			t.Errorf("queued %d emails for an unassigned issue, want none", n)
		}
		if got := len(outbox.For(operatorEmail)) + len(outbox.For(operator2Email)); got != 0 {
			t.Errorf("operators got %d emails", got)
		}
	})
}

// An email whose issue was reassigned or deleted before the worker got to it
// is not sent, and the task is done, not retried.
func TestIntegrationAssigneeEmailSkipsAStaleTask(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(f *fixture, svc *issues.Service, id int64) error
	}{
		{"reassigned", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := svc.PlatformUpdate(context.Background(), issues.Operator{UserID: f.operator2.ID}, id,
				issues.IssueUpdate{AssigneeSet: true, AssigneeID: &f.operator2.ID})
			return err
		}},
		{"deleted", func(f *fixture, svc *issues.Service, id int64) error {
			return svc.Delete(context.Background(), f.member(), id)
		}},
		{"deactivated assignee", func(f *fixture, svc *issues.Service, id int64) error {
			_, err := f.pool.Exec(context.Background(), "UPDATE users SET is_active = false WHERE id = $1", f.operator.ID)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			q := &fakeQueue{}
			svc, outbox, mailer := f.queued(q)
			issue := f.assigned(t, svc, q, outbox, mailer)
			if _, err := svc.MemberComment(context.Background(), f.member(), issue.ID, issues.NewComment{Body: "Any news?"}); err != nil {
				t.Fatalf("comment: %v", err)
			}
			pending := q.tasks
			q.tasks = nil
			if err := tc.change(f, svc, issue.ID); err != nil {
				t.Fatalf("change: %v", err)
			}
			for _, task := range pending {
				if task.Type() != issues.TaskOperatorEmail {
					continue
				}
				if err := mailer.HandleOperatorEmail(context.Background(), task); err != nil {
					t.Errorf("worker = %v, want nil", err)
				}
			}
			if got := len(outbox.For(operatorEmail)) + len(outbox.For(operator2Email)); got != 0 {
				t.Errorf("sent %d emails for a %s issue", got, tc.name)
			}
		})
	}
}

// A task queued in the older shape (one per operator, about a new issue or a
// reply) is dropped: no email, no retry.
func TestIntegrationOperatorEmailDropsAnOldTask(t *testing.T) {
	f := newFixture(t)
	_, outbox, mailer := f.queued(&fakeQueue{})
	old := asynq.NewTask(issues.TaskOperatorEmail, []byte(`{"issue_id":1,"kind":"issue-new","to":"ops@test.com"}`))
	if err := mailer.HandleOperatorEmail(context.Background(), old); err != nil {
		t.Errorf("worker = %v, want the old task dropped", err)
	}
	if got := subjects(outbox, operatorEmail); len(got) != 0 {
		t.Errorf("sent %v for an old task", got)
	}
}

// A failed send is returned, so asynq retries it, and the error carries no
// email address: the shared task error handler reports it to a third party.
func TestIntegrationAssigneeEmailFailureIsRetriedWithoutTheAddress(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	svc, outbox, mailer := f.queued(q)
	issue := f.assigned(t, svc, q, outbox, mailer)
	if _, err := svc.MemberComment(context.Background(), f.member(), issue.ID, issues.NewComment{Body: "Ping."}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	var task *asynq.Task
	for _, tk := range q.tasks {
		if tk.Type() == issues.TaskOperatorEmail {
			task = tk
		}
	}
	if task == nil {
		t.Fatal("no email task queued for the reply")
	}
	if err := issues.NewOperatorMailer(f.q, failingSender{}, "http://localhost", queueOperators).HandleOperatorEmail(context.Background(), task); err == nil {
		t.Error("worker = nil for a failed send, want the error so the task is retried")
	}
	err := issues.NewOperatorMailer(f.q, rejectingSender{}, "http://localhost", queueOperators).HandleOperatorEmail(context.Background(), task)
	if err == nil || strings.Contains(err.Error(), "@") {
		t.Errorf("worker = %v, want an error with no address", err)
	}
	if errors.Is(err, asynq.SkipRetry) {
		t.Errorf("worker = %v, want it retryable", err)
	}
}

// When the queue refuses the task, the request sends the email itself.
func TestIntegrationAssigneeEmailFallsBackWhenTheQueueFails(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	svc, outbox, mailer := f.queued(q)
	issue := f.assigned(t, svc, q, outbox, mailer)
	q.err = errors.New("redis down")
	if _, err := svc.MemberComment(context.Background(), f.member(), issue.ID, issues.NewComment{Body: "Inline please."}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if got := subjects(outbox, operatorEmail); len(got) != 1 {
		t.Errorf("assignee got %v, want the email sent inline", got)
	}
}

// An assignee who is no longer on the support team gets no email about the
// issues still assigned to them: the worker checks the team as it is when it
// sends.
func TestIntegrationAssigneeEmailSkipsSomeoneOffTheTeam(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	svc, outbox, mailer := f.queued(q)
	issue := f.assigned(t, svc, q, outbox, mailer)
	if _, err := svc.MemberComment(context.Background(), f.member(), issue.ID, issues.NewComment{Body: "Still there?"}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	sender := &notification.RecordingSender{Next: &notification.ConsoleSender{}, Outbox: outbox}
	smallerTeam := issues.NewOperatorMailer(f.q, sender, "http://localhost", []string{operator2Email})
	if n := sendQueued(t, q, smallerTeam); n != 1 {
		t.Fatalf("queued %d emails, want the reply's", n)
	}
	if got := subjects(outbox, operatorEmail); len(got) != 0 {
		t.Errorf("someone off the support team got %v", got)
	}
}
