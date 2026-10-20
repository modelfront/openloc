package issues_test

import (
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

// fakeQueue records the tasks a request would hand to the worker.
type fakeQueue struct {
	tasks []*asynq.Task
	err   error
}

func (q *fakeQueue) EnqueueTask(t *asynq.Task, _ ...asynq.Option) error {
	if q.err != nil {
		return q.err
	}
	q.tasks = append(q.tasks, t)
	return nil
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

var queueOperators = []string{"ops1@test.com", "ops2@test.com"}

// queued is a service whose operator emails go through q, the outbox that
// records what the worker sends, and the worker's mailer.
func (f *fixture) queued(q *fakeQueue) (*issues.Service, *notification.Outbox, *issues.OperatorMailer) {
	outbox := notification.NewOutbox(50)
	sender := &notification.RecordingSender{Next: &notification.ConsoleSender{}, Outbox: outbox}
	svc := issues.NewService(f.pool, nil, nil, sender, queueOperators, "http://localhost").WithEmailQueue(q)
	return svc, outbox, issues.NewOperatorMailer(f.q, sender, "http://localhost")
}

func (f *fixture) member() issues.Member {
	return issues.Member{TenantID: f.tenant.ID, UserID: f.reporter.ID, Role: "linguist"}
}

func newIssue(title string) issues.NewIssue {
	return issues.NewIssue{Kind: "bug", Severity: "major", Area: "editor", Title: title,
		Description: "The export button does nothing.", Context: "{}"}
}

// A report hands one email per operator to the worker and sends none itself;
// the worker then sends each.
func TestIntegrationReportQueuesOperatorEmails(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	svc, outbox, mailer := f.queued(q)
	ctx := context.Background()

	issue, err := svc.Create(ctx, f.member(), newIssue("Queued report"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(q.tasks) != len(queueOperators) {
		t.Fatalf("queued %d tasks, want one per operator (%d)", len(q.tasks), len(queueOperators))
	}
	for _, to := range queueOperators {
		if got := subjects(outbox, to); len(got) != 0 {
			t.Fatalf("the request sent %v to %s itself", got, to)
		}
	}
	for _, task := range q.tasks {
		if task.Type() != issues.TaskOperatorEmail {
			t.Fatalf("task type = %q", task.Type())
		}
		if err := mailer.HandleOperatorEmail(ctx, task); err != nil {
			t.Fatalf("worker: %v", err)
		}
	}
	for _, to := range queueOperators {
		if got := subjects(outbox, to); len(got) != 1 || got[0] != fmt.Sprintf("Issue #%d: Queued report", issue.ID) {
			t.Errorf("%s got %v, want the new-issue email", to, got)
		}
	}
	// The worker's email is the shared layout, like the one sent in a request.
	assertEmail(t, "queued new issue", newestBody(t, outbox, queueOperators[0]),
		emaillayout.HeaderMarker, ">Organization</td>", ">Description</div>", ">Open the issue</a>")
}

// A reply goes the same way, with its text.
func TestIntegrationReplyQueuesOperatorEmails(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	svc, outbox, mailer := f.queued(q)
	ctx := context.Background()
	issue, err := svc.Create(ctx, f.member(), newIssue("Replied to"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	q.tasks = nil

	if _, err := svc.MemberComment(ctx, f.member(), issue.ID, issues.NewComment{Body: "It happens on DOCX only."}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if len(q.tasks) != len(queueOperators) {
		t.Fatalf("queued %d tasks, want %d", len(q.tasks), len(queueOperators))
	}
	for _, task := range q.tasks {
		if err := mailer.HandleOperatorEmail(ctx, task); err != nil {
			t.Fatalf("worker: %v", err)
		}
	}
	got := outbox.For(queueOperators[0])
	if len(got) != 1 || got[0].Subject != fmt.Sprintf("Issue #%d: Replied to", issue.ID) || !strings.Contains(got[0].Body, "It happens on DOCX only.") {
		t.Errorf("reply email = %+v", got)
	}
}

// An issue deleted before the worker gets to it sends nothing, and the task
// is done, not retried.
func TestIntegrationOperatorEmailSkipsDeletedIssue(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	svc, outbox, mailer := f.queued(q)
	ctx := context.Background()
	issue, err := svc.Create(ctx, f.member(), newIssue("Gone before sending"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Delete(ctx, f.member(), issue.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := mailer.HandleOperatorEmail(ctx, q.tasks[0]); err != nil {
		t.Errorf("worker = %v, want nil", err)
	}
	if got := subjects(outbox, queueOperators[0]); len(got) != 0 {
		t.Errorf("sent %v for a deleted issue", got)
	}
}

// A failed send is returned, so asynq retries that one email.
func TestIntegrationOperatorEmailFailureIsRetried(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	svc, _, _ := f.queued(q)
	if _, err := svc.Create(context.Background(), f.member(), newIssue("Retried")); err != nil {
		t.Fatalf("create: %v", err)
	}
	mailer := issues.NewOperatorMailer(f.q, failingSender{}, "http://localhost")
	if err := mailer.HandleOperatorEmail(context.Background(), q.tasks[0]); err == nil {
		t.Error("worker = nil for a failed send, want the error so the task is retried")
	}
}

// When the queue refuses the task, the request sends the email itself.
func TestIntegrationOperatorEmailFallsBackWhenTheQueueFails(t *testing.T) {
	f := newFixture(t)
	svc, outbox, _ := f.queued(&fakeQueue{err: errors.New("redis down")})
	if _, err := svc.Create(context.Background(), f.member(), newIssue("Fallback")); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, to := range queueOperators {
		if got := subjects(outbox, to); len(got) != 1 {
			t.Errorf("%s got %v, want the email sent inline", to, got)
		}
	}
}

// The worker hands its failure to the shared task error handler, which logs
// it and reports the last one to error tracking, a third party. So the error
// it returns carries no email address, and stays retryable.
func TestIntegrationOperatorEmailFailureHidesTheAddress(t *testing.T) {
	f := newFixture(t)
	q := &fakeQueue{}
	svc, _, _ := f.queued(q)
	if _, err := svc.Create(context.Background(), f.member(), newIssue("Rejected")); err != nil {
		t.Fatalf("create: %v", err)
	}
	mailer := issues.NewOperatorMailer(f.q, rejectingSender{}, "http://localhost")
	err := mailer.HandleOperatorEmail(context.Background(), q.tasks[0])
	if err == nil {
		t.Fatal("worker = nil for a rejected send")
	}
	if strings.Contains(err.Error(), "@") {
		t.Errorf("returned error quotes an address: %q", err)
	}
	if !strings.Contains(err.Error(), "Recipient address rejected") {
		t.Errorf("returned error lost the server's reason: %q", err)
	}
	if errors.Is(err, asynq.SkipRetry) {
		t.Error("a rejected send is not retried")
	}
}
