package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/errtrack"
	"openloc/internal/logging"
	"openloc/internal/notification"
	"openloc/internal/ptr"
)

// TaskOperatorEmail is the asynq task type for one email to the operator an
// issue is assigned to, about what someone else did on it. Producer:
// Service.emailAssignee. Consumer: OperatorMailer.HandleOperatorEmail.
const TaskOperatorEmail = "issue:operator-email"

const (
	notifyMaxRetry = 5
	notifyTimeout  = time.Minute
)

// TaskEnqueuer puts a task on this deployment's queue (*worker.Enqueuer).
type TaskEnqueuer interface {
	EnqueueTask(task *asynq.Task, opts ...asynq.Option) error
}

// WithEmailQueue sends the assignee emails and Slack posts through q's worker
// instead of in the request.
func (s *Service) WithEmailQueue(q TaskEnqueuer) *Service {
	s.queue = q
	return s
}

// dispatch hands one operator notification to the worker as a taskType
// task with opts, or delivers it here without a queue or when the queue
// refuses it. A Unique task whose twin is still queued is left to the twin.
// Best-effort: a failure is reported, never returned.
func (s *Service) dispatch(ctx context.Context, issueID int64, taskType string, payload any, enqueueFailed string,
	deliver func(context.Context) error, report func(context.Context, error), opts ...asynq.Option) {
	if s.queue != nil {
		err := s.enqueue(taskType, payload, opts...)
		if err == nil || errors.Is(err, asynq.ErrDuplicateTask) {
			return
		}
		logging.Warn(ctx, err, "queueing an issue notification failed; sending it now", enqueueFailed, logging.IssueID(issueID))
	}
	if err := deliver(ctx); err != nil {
		report(ctx, err)
	}
}

func (s *Service) enqueue(taskType string, payload any, opts ...asynq.Option) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s task: %w", taskType, err)
	}
	return s.queue.EnqueueTask(asynq.NewTask(taskType, data),
		append([]asynq.Option{asynq.MaxRetry(notifyMaxRetry), asynq.Timeout(notifyTimeout)}, opts...)...)
}

// What an assignee email tells.
const (
	noticeReply   = "reply"   // someone in the reporter's org commented
	noticeComment = "comment" // another operator replied to the reporter
	noticeNote    = "note"    // another operator added an internal note
	noticeStatus  = "status"  // another operator changed the status
	noticeEdit    = "edit"    // another operator changed kind, severity, area, priority or tags
	noticeKind    = "kind"    // the reporter's org changed the kind
	noticeConfirm = "confirm" // the reporter's org confirmed the fix
)

var noticeKinds = map[string]bool{
	noticeReply: true, noticeComment: true, noticeNote: true, noticeStatus: true,
	noticeEdit: true, noticeKind: true, noticeConfirm: true,
}

// assigneeNotice is one email to the operator an issue is assigned to, about
// what someone else did on it. AssigneeID is the assignee when it happened,
// ActorID who did it; Text is a comment or note, Value a new status or kind,
// Changes the labels of edited fields.
type assigneeNotice struct {
	IssueID    int64    `json:"issue_id"`
	Kind       string   `json:"kind"`
	AssigneeID int64    `json:"assignee_id"`
	ActorID    int64    `json:"actor_id"`
	Text       string   `json:"text,omitempty"`
	Value      string   `json:"value,omitempty"`
	Changes    []string `json:"changes,omitempty"`
}

// emailAssignee emails the operator issue is assigned to about what actorID
// did, unless nobody is assigned or the assignee did it. Best-effort.
func (s *Service) emailAssignee(ctx context.Context, issue db.Issue, actorID int64, n assigneeNotice) {
	if s.sender == nil || issue.AssigneeID == nil || *issue.AssigneeID == actorID {
		return
	}
	n.IssueID, n.AssigneeID, n.ActorID = issue.ID, *issue.AssigneeID, actorID
	mailer := NewOperatorMailer(db.New(s.pool), s.sender, s.baseURL, s.operators)
	s.dispatch(ctx, issue.ID, TaskOperatorEmail, n, "issue-email-enqueue-failed",
		func(ctx context.Context) error {
			_, err := mailer.deliver(ctx, n)
			return err
		},
		func(ctx context.Context, err error) { mailer.report(ctx, n, err) })
}

type issueEmailQuerier interface {
	GetIssueForPlatform(ctx context.Context, id int64) (db.GetIssueForPlatformRow, error)
	GetActiveUser(ctx context.Context, id int64) (db.User, error)
}

// OperatorMailer sends the assignee emails, from the worker or, without a
// queue, from the request. team is the support team (PLATFORM_ADMIN_EMAILS):
// an assignee no longer on it gets no email.
type OperatorMailer struct {
	q       issueEmailQuerier
	sender  notification.Sender
	baseURL string
	team    auth.EmailSet
}

func NewOperatorMailer(q issueEmailQuerier, sender notification.Sender, baseURL string, operators []string) *OperatorMailer {
	return &OperatorMailer{q: q, sender: sender, baseURL: baseURL, team: auth.NewEmailSet(operators)}
}

// RegisterHandlers wires the email task onto the shared asynq mux.
func (m *OperatorMailer) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskOperatorEmail, m.HandleOperatorEmail)
}

// HandleOperatorEmail is the asynq handler. A failed send is returned, so
// asynq retries it. The shared task error handler logs each failure and
// reports the last one to error tracking, so the error is returned with every
// email address removed and is not reported here. A task in an older shape
// is dropped.
func (m *OperatorMailer) HandleOperatorEmail(ctx context.Context, t *asynq.Task) error {
	var n assigneeNotice
	if err := json.Unmarshal(t.Payload(), &n); err != nil {
		return fmt.Errorf("decode issue email task: %v: %w", err, asynq.SkipRetry)
	}
	if !noticeKinds[n.Kind] || n.AssigneeID == 0 {
		logging.Info(ctx, "dropped an issue email task of an older shape", "issue-email-dropped", logging.IssueID(n.IssueID))
		return nil
	}
	if to, err := m.deliver(ctx, n); err != nil {
		return errtrack.SendFailure(err, to)
	}
	return nil
}

// deliver sends the email and returns the address it went to. An issue that
// is gone, or no longer assigned to the same active member of the support
// team, sends nothing.
func (m *OperatorMailer) deliver(ctx context.Context, n assigneeNotice) (string, error) {
	row, err := m.q.GetIssueForPlatform(ctx, n.IssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		logging.Info(ctx, "issue gone before its email was sent", "issue-email-skipped", logging.IssueID(n.IssueID))
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load issue for email: %w", err)
	}
	if row.AssigneeID == nil || *row.AssigneeID != n.AssigneeID {
		logging.Info(ctx, "issue reassigned before its email was sent", "issue-email-skipped", logging.IssueID(n.IssueID))
		return "", nil
	}
	assignee, err := m.q.GetActiveUser(ctx, n.AssigneeID)
	if errors.Is(err, pgx.ErrNoRows) {
		logging.Info(ctx, "assignee gone before the email was sent", "issue-email-skipped", logging.IssueID(n.IssueID))
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load assignee: %w", err)
	}
	if !m.team.Has(assignee.Email) {
		logging.Info(ctx, "assignee left the support team before the email was sent", "issue-email-skipped", logging.IssueID(n.IssueID))
		return "", nil
	}
	actor, err := userName(ctx, m.q, n.ActorID)
	if err != nil {
		return "", err
	}
	subject, content := assigneeEmail(m.baseURL, n, row, actor)
	body, err := emaillayout.Render(m.baseURL, content)
	if err != nil {
		return "", fmt.Errorf("render issue email: %w", err)
	}
	thread := issueThread(m.baseURL, row.ID, row.CreatedAt.Time)
	return assignee.Email, m.sender.SendEmail(ctx, assignee.Email, subject, body.InThread(thread))
}

// report logs a failed email and sends it to error tracking: the request's
// path, which has no task error handler. The address stays out of the
// notice: notices go to a third party.
func (m *OperatorMailer) report(ctx context.Context, n assigneeNotice, err error) {
	logging.Error(ctx, err, "sending an issue email failed", "issue-email-send-failed", logging.IssueID(n.IssueID))
	errtrack.Capture(ctx, errtrack.SendFailure(err, ""), errtrack.CaptureOptions{
		Class: errtrack.ClassEmailSendFailed, Message: "failed to send issue " + n.Kind + " email",
		Subsystem: "email", Event: "issue-" + n.Kind,
	})
}

// userName is the name the support team's notices give the user id: its
// display name, or "Someone" for a user who is gone or deactivated.
func userName(ctx context.Context, q issueEmailQuerier, id int64) (string, error) {
	u, err := q.GetActiveUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "Someone", nil
	}
	if err != nil {
		return "", fmt.Errorf("load user %d: %w", id, err)
	}
	return displayName(u), nil
}

// displayName is a user's name, or the email address when the name is empty.
func displayName(u db.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

// assigneeEmail is the subject and body of one assignee email about row.
func assigneeEmail(baseURL string, n assigneeNotice, row db.GetIssueForPlatformRow, actor string) (string, emaillayout.Content) {
	c := emaillayout.Content{
		Body: emaillayout.P("%s", emaillayout.Strong(row.Title)),
		Details: []emaillayout.Row{
			{Label: "Organization", Value: row.OrgName},
			{Label: "Status", Value: statusLabels[row.Status]},
		},
		Button: &emaillayout.Button{Label: "Open the issue", URL: emaillayout.AppURL(baseURL, fmt.Sprintf("/platform/issues/%d", row.ID))},
	}
	quote := func(label string) {
		if n.Text != "" {
			c.Body = emaillayout.Join(c.Body, emaillayout.Callout(label, n.Text))
		}
	}
	switch n.Kind {
	case noticeReply:
		c.Heading = actor + " replied"
		quote("Reply")
	case noticeComment:
		c.Heading = actor + " replied to the reporter"
		quote("Reply")
	case noticeNote:
		c.Heading = actor + " added an internal note"
		quote("Note")
	case noticeStatus:
		c.Heading = fmt.Sprintf("%s changed the status to %s", actor, n.Value)
		quote("Comment")
	case noticeEdit:
		c.Heading = actor + " edited the issue"
		c.Body = emaillayout.Join(c.Body, emaillayout.P("Changed: %s", strings.Join(n.Changes, ", ")))
		c.Details = append(c.Details,
			emaillayout.Row{Label: "Kind", Value: KindSummary(row.Kind, ptr.Deref(row.Severity))},
			emaillayout.Row{Label: "Area", Value: areaLabels[row.Area]})
	case noticeKind:
		c.Heading = fmt.Sprintf("%s changed the kind to %s", actor, n.Value)
	case noticeConfirm:
		c.Heading = actor + " confirmed the fix"
	}
	return notification.IssueSubject(row.ID, row.Title), c
}
