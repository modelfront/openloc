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
)

// The bell and email show at most this much of a comment.
const previewLength = 300

// orgNotifyTimeout bounds the org's resolve bells, which outlive the request.
const orgNotifyTimeout = 10 * time.Second

func (s *Service) link(path string) string { return emaillayout.AppURL(s.baseURL, path) }

// issueThread is the email thread of an issue: every email about it, to the
// support team or the reporter, names it, so mail apps group them. The id
// holds the app's host and the issue's creation time besides its number: two
// deployments, or one whose numbers start over after a database reset, never
// share a thread.
func issueThread(baseURL string, id int64, filed time.Time) emaillayout.Thread {
	host := "openloc"
	if u, err := url.Parse(baseURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return emaillayout.Thread{ID: fmt.Sprintf("issue-%d-%d.%s", id, filed.UnixMicro(), host)}
}

// notifyNewIssue posts a new issue to Slack when it is set up. No email goes
// out: the support team finds new issues in Slack and under Reported issues.
func (s *Service) notifyNewIssue(ctx context.Context, issue db.Issue) {
	if s.slack != nil {
		s.postToSlack(ctx, slackPostPayload{IssueID: issue.ID, Kind: slackNewIssue})
	}
}

// notifyReply tells the support team that someone in the reporter's org
// commented: in the issue's Slack thread, and by email to the assignee.
func (s *Service) notifyReply(ctx context.Context, issue db.Issue, actorID int64, body *string) {
	text := commentPreview(body)
	s.postWritten(ctx, issue.ID, slackReply, actorID, "", text)
	s.emailAssignee(ctx, issue, actorID, assigneeNotice{Kind: noticeReply, Text: text})
}

// commentPreview is a comment's text as the support team's notices quote it.
func commentPreview(body *string) string {
	if body == nil {
		return "(attachments)"
	}
	return truncate(plainText(*body), 2000)
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
		Thread: issueThread(s.baseURL, issue.ID, issue.CreatedAt.Time),
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
