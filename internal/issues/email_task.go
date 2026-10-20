package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/errtrack"
	"openloc/internal/logging"
	"openloc/internal/notification"
)

// TaskOperatorEmail is the asynq task type for one email to one platform
// admin about an issue. Producer: Service.emailOperators. Consumer:
// OperatorMailer.HandleOperatorEmail.
const TaskOperatorEmail = "issue:operator-email"

const (
	emailNewIssue = "issue-new"
	emailReply    = "issue-reply"

	operatorEmailMaxRetry = 5
	operatorEmailTimeout  = time.Minute
)

// TaskEnqueuer puts a task on this deployment's queue (*worker.Enqueuer).
type TaskEnqueuer interface {
	EnqueueTask(task *asynq.Task, opts ...asynq.Option) error
}

// WithEmailQueue sends the operator emails through q's worker instead of in
// the request.
func (s *Service) WithEmailQueue(q TaskEnqueuer) *Service {
	s.queue = q
	return s
}

// operatorEmailPayload is one email: the issue, the kind, the recipient, and
// a reply's text. One task per recipient, so a retry resends only that one.
type operatorEmailPayload struct {
	IssueID int64  `json:"issue_id"`
	Kind    string `json:"kind"`
	To      string `json:"to"`
	Reply   string `json:"reply,omitempty"`
}

func (s *Service) enqueueOperatorEmail(p operatorEmailPayload) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode issue email task: %w", err)
	}
	return s.queue.EnqueueTask(asynq.NewTask(TaskOperatorEmail, data),
		asynq.MaxRetry(operatorEmailMaxRetry), asynq.Timeout(operatorEmailTimeout))
}

type issueEmailQuerier interface {
	GetIssueForPlatform(ctx context.Context, id int64) (db.GetIssueForPlatformRow, error)
}

// OperatorMailer sends the operator emails, from the worker or, without a
// queue, from the request.
type OperatorMailer struct {
	q       issueEmailQuerier
	sender  notification.Sender
	baseURL string
}

func NewOperatorMailer(q issueEmailQuerier, sender notification.Sender, baseURL string) *OperatorMailer {
	return &OperatorMailer{q: q, sender: sender, baseURL: baseURL}
}

// RegisterHandlers wires the email task onto the shared asynq mux.
func (m *OperatorMailer) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskOperatorEmail, m.HandleOperatorEmail)
}

// HandleOperatorEmail is the asynq handler. A failed send is returned, so
// asynq retries it. The shared task error handler logs each failure and
// reports the last one to error tracking, so the error is returned with every
// email address removed and is not reported here.
func (m *OperatorMailer) HandleOperatorEmail(ctx context.Context, t *asynq.Task) error {
	var p operatorEmailPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("decode issue email task: %v: %w", err, asynq.SkipRetry)
	}
	if err := m.deliver(ctx, p); err != nil {
		return errtrack.SendFailure(err, p.To)
	}
	return nil
}

// deliver loads the issue and sends the email. An issue deleted in the
// meantime sends nothing.
func (m *OperatorMailer) deliver(ctx context.Context, p operatorEmailPayload) error {
	row, err := m.q.GetIssueForPlatform(ctx, p.IssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		logging.Info(ctx, "issue gone before its email was sent", "issue-email-skipped", logging.IssueID(p.IssueID))
		return nil
	}
	if err != nil {
		return fmt.Errorf("load issue for email: %w", err)
	}
	subject, content, err := operatorEmail(m.baseURL, p.Kind, row, p.Reply)
	if err != nil {
		return err
	}
	body, err := emaillayout.Render(m.baseURL, content)
	if err != nil {
		return fmt.Errorf("render issue email: %w", err)
	}
	return m.sender.SendEmail(ctx, p.To, subject, body.InThread(issueThread(m.baseURL, row.ID, row.CreatedAt.Time, p.Kind == emailNewIssue)))
}

// report logs a failed email and sends it to error tracking: the request's
// path, which has no task error handler.
func (m *OperatorMailer) report(ctx context.Context, p operatorEmailPayload, err error) {
	// The address stays out of the notice: notices go to a third party.
	logging.Error(ctx, err, "sending an issue email failed", "issue-email-send-failed", logging.IssueID(p.IssueID))
	errtrack.Capture(ctx, errtrack.SendFailure(err, p.To), errtrack.CaptureOptions{
		Class: errtrack.ClassEmailSendFailed, Message: "failed to send " + p.Kind + " email",
		Subsystem: "email", Event: p.Kind,
	})
}
