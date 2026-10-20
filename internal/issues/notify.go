package issues

import (
	"context"
	"fmt"
	"net/url"
	"time"
	"unicode/utf8"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/logging"
	"openloc/internal/notification"
	"openloc/internal/ptr"
)

// The bell and email show at most this much of a comment.
const previewLength = 300

// orgNotifyTimeout bounds the org's resolve bells, which outlive the request.
const orgNotifyTimeout = 10 * time.Second

func (s *Service) link(path string) string { return emaillayout.AppURL(s.baseURL, path) }

// issueThread is the email thread of an issue: every email about it, to the
// support team or the reporter, is in it, and the new-issue email starts it.
// The id holds the app's host and the issue's creation time besides its
// number: two deployments, or one whose numbers start over after a database
// reset, never share a thread, and a new-issue email never repeats a message
// id a mailbox has already seen (it would be dropped).
func issueThread(baseURL string, id int64, filed time.Time, root bool) emaillayout.Thread {
	host := "openloc"
	if u, err := url.Parse(baseURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return emaillayout.Thread{ID: fmt.Sprintf("issue-%d-%d.%s", id, filed.UnixMicro(), host), Root: root}
}

// emailOperatorsNewIssue tells every platform admin about a new issue.
func (s *Service) emailOperatorsNewIssue(ctx context.Context, issue db.Issue) {
	s.emailOperators(ctx, issue.ID, emailNewIssue, "")
}

// emailOperatorsReply tells every platform admin that the reporter commented.
func (s *Service) emailOperatorsReply(ctx context.Context, issue db.Issue, body *string) {
	text := "(attachments)"
	if body != nil {
		text = truncate(plainText(*body), 2000)
	}
	s.emailOperators(ctx, issue.ID, emailReply, text)
}

// operatorEmail is the subject and body of one operator email about row.
func operatorEmail(baseURL, kind string, row db.GetIssueForPlatformRow, reply string) (string, emaillayout.Content, error) {
	button := &emaillayout.Button{Label: "Open the issue", URL: emaillayout.AppURL(baseURL, fmt.Sprintf("/platform/issues/%d", row.ID))}
	switch kind {
	case emailNewIssue:
		return notification.IssueSubject(row.ID, row.Title), emaillayout.Content{
			Heading: "A new issue was reported",
			Body: emaillayout.Join(
				emaillayout.P("%s", emaillayout.Strong(row.Title)),
				emaillayout.Callout("Description", truncate(plainText(row.Description), 2000)),
			),
			Details: []emaillayout.Row{
				{Label: "Organization", Value: row.OrgName},
				{Label: "Reporter", Value: fmt.Sprintf("%s (%s)", row.ReporterName, row.ReporterEmail)},
				{Label: "Kind", Value: KindSummary(row.Kind, ptr.Deref(row.Severity))},
				{Label: "Area", Value: areaLabels[row.Area]},
			},
			Button: button,
		}, nil
	case emailReply:
		return notification.IssueSubject(row.ID, row.Title), emaillayout.Content{
			Heading: "The reporter replied",
			Body: emaillayout.Join(
				emaillayout.P("%s", emaillayout.Strong(row.Title)),
				emaillayout.Callout("Reply", reply),
			),
			Details: []emaillayout.Row{
				{Label: "Organization", Value: row.OrgName},
				{Label: "Reporter", Value: row.ReporterName},
			},
			Button: button,
		}, nil
	}
	return "", emaillayout.Content{}, fmt.Errorf("unknown issue email kind %q", kind)
}

// emailOperators emails every platform admin about the issue. With a queue,
// each email is a worker task, so the request does not wait on the mail
// server; without one, or when the queue refuses a task, it is sent here.
// Best-effort: a failed email never fails the request.
func (s *Service) emailOperators(ctx context.Context, issueID int64, kind, reply string) {
	if s.sender == nil || len(s.operators) == 0 {
		return
	}
	mailer := NewOperatorMailer(db.New(s.pool), s.sender, s.baseURL)
	for _, to := range s.operators {
		p := operatorEmailPayload{IssueID: issueID, Kind: kind, To: to, Reply: reply}
		if s.queue != nil {
			err := s.enqueueOperatorEmail(p)
			if err == nil {
				continue
			}
			logging.Warn(ctx, err, "queueing an issue email failed; sending it now", "issue-email-enqueue-failed", logging.IssueID(issueID))
		}
		if err := mailer.deliver(ctx, p); err != nil {
			mailer.report(ctx, p, err)
		}
	}
}

// notifyReporter tells the reporter about an operator's comment or status
// change: the bell, and an email unless they muted issue updates.
func (s *Service) notifyReporter(ctx context.Context, issue db.Issue, op Operator, status, body *string) {
	if s.notifier == nil {
		return
	}
	e := notification.Event{
		Type: notification.EventIssueUpdated, TenantID: issue.TenantID, RecipientID: issue.ReporterID,
		ActorID: op.UserID, IssueID: issue.ID, IssueTitle: issue.Title,
		Link:   s.link(fmt.Sprintf("/issues/%d", issue.ID)),
		Thread: issueThread(s.baseURL, issue.ID, issue.CreatedAt.Time, false),
	}
	if status != nil {
		e.IssueStatus = statusLabels[*status]
	}
	if body != nil {
		e.Reason = truncate(plainText(*body), previewLength)
	}
	if err := s.notifier.Send(ctx, e); err != nil {
		logging.Error(ctx, err, "notifying the reporter of an issue update failed", "issue-notify-failed", logging.IssueID(issue.ID))
	}
}

// notifyOrgResolved rings the bell of the org's owners, admins and project
// managers when one of its issues is resolved. The reporter hears it through
// notifyReporter, and the operator who resolved it needs no bell. It runs after
// the commit on its own bounded context, so a request cancelled meanwhile
// drops no bell.
func (s *Service) notifyOrgResolved(reqCtx context.Context, issue db.Issue, op Operator) {
	if s.notifier == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), orgNotifyTimeout)
	defer cancel()
	managers, err := db.New(s.pool).ListTenantManagerIDs(ctx, issue.TenantID)
	if err != nil {
		logging.Error(ctx, err, "loading the org's managers for a resolved issue failed", "issue-notify-org-failed", logging.IssueID(issue.ID))
		return
	}
	recipients := make([]int64, 0, len(managers))
	for _, userID := range managers {
		if userID != issue.ReporterID && userID != op.UserID {
			recipients = append(recipients, userID)
		}
	}
	if err := s.notifier.SendBells(ctx, notification.Event{
		Type: notification.EventIssueResolved, TenantID: issue.TenantID,
		ActorID: op.UserID, IssueID: issue.ID, IssueTitle: issue.Title,
		Link: s.link(fmt.Sprintf("/issues/%d", issue.ID)),
	}, recipients); err != nil {
		logging.Error(ctx, err, "notifying the org of a resolved issue failed", "issue-notify-org-failed", logging.IssueID(issue.ID))
	}
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

// KindSummary is a kind's label, with the severity when set: "Bug (Critical)".
func KindSummary(kind, severity string) string {
	if severity == "" {
		return kindLabels[kind]
	}
	return fmt.Sprintf("%s (%s)", kindLabels[kind], severityLabels[severity])
}

// AreaLabel is an area's label: "Translation memory".
func AreaLabel(area string) string { return areaLabels[area] }
