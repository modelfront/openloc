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
	"openloc/internal/ptr"
	"openloc/internal/slack"
)

// TaskSlackPost is the asynq task type for one Slack post to the support
// team's channel about an issue. Producer: Service.postToSlack. Consumer:
// SlackNotifier.HandleSlackPost.
const TaskSlackPost = "issue:slack-post"

// slackPreviewLength caps the description or reply quoted in a post.
const slackPreviewLength = 1000

// The post kinds: a new issue's card, and in its thread a reply (from the
// reporter's org or the support team) or an internal note.
const (
	slackNewIssue = "issue-new"
	slackReply    = "issue-reply"
	slackNote     = "issue-note"
)

// slackRefresh is the post kind that rewrites an issue's first post, its
// card, to the issue as it is now.
const slackRefresh = "issue-refresh"

// A card refresh waits slackRefreshDelay in the queue, and while it is queued
// a twin for the same issue is refused, so a run of quick changes costs one
// chat.update showing the last of them. slackRefreshWindow bounds how long a
// refresh that keeps failing holds off the next one.
const (
	slackRefreshDelay  = 5 * time.Second
	slackRefreshWindow = time.Minute
)

// slackStatusEmoji leads the card, so the channel reads as a status board.
var slackStatusEmoji = map[string]string{
	StatusOpen:       ":large_blue_circle:",
	StatusInProgress: ":large_yellow_circle:",
	StatusNeedsInfo:  ":question:",
	StatusResolved:   ":white_check_mark:",
	StatusWontFix:    ":no_entry_sign:",
	StatusDuplicate:  ":link:",
}

// SlackPoster posts and edits messages (*slack.Client).
type SlackPoster interface {
	PostMessage(ctx context.Context, m slack.Message) (string, error)
	UpdateMessage(ctx context.Context, channel, ts, text string) error
}

// WithSlack posts each new issue to channel, and every reply and internal
// note in its thread.
func (s *Service) WithSlack(p SlackPoster, channel string) *Service {
	s.slack, s.slackChannel = p, channel
	return s
}

// slackPostPayload is one post: the issue, the kind (slackNewIssue,
// slackReply, slackNote or slackRefresh), and for a reply or a note its text,
// its author and the status it set, if any. A reply with no author is the
// reporter's.
type slackPostPayload struct {
	IssueID  int64  `json:"issue_id"`
	Kind     string `json:"kind"`
	Reply    string `json:"reply,omitempty"`
	AuthorID int64  `json:"author_id,omitempty"`
	Status   string `json:"status,omitempty"`
}

// postToSlack posts about the issue: from the worker with a queue, else here.
func (s *Service) postToSlack(ctx context.Context, p slackPostPayload, opts ...asynq.Option) {
	n := NewSlackNotifier(db.New(s.pool), s.slack, s.slackChannel, s.baseURL)
	s.dispatch(ctx, p.IssueID, TaskSlackPost, p, "issue-slack-enqueue-failed",
		func(ctx context.Context) error { return n.deliver(ctx, p) },
		func(ctx context.Context, err error) { n.report(ctx, p, err) }, opts...)
}

// refreshSlack rewrites the issue's card after a change it shows: status,
// confirmation, assignee, kind, area, or the issue's deletion.
func (s *Service) refreshSlack(ctx context.Context, issueID int64) {
	if s.slack != nil {
		s.postToSlack(ctx, slackPostPayload{IssueID: issueID, Kind: slackRefresh},
			asynq.ProcessIn(slackRefreshDelay), asynq.Unique(slackRefreshWindow))
	}
}

// postWritten posts what authorID wrote on the issue in its card's thread:
// a reply (kind slackReply; status is the status it set, if any) or an
// internal note (slackNote).
func (s *Service) postWritten(ctx context.Context, issueID int64, kind string, authorID int64, status, text string) {
	if s.slack != nil {
		s.postToSlack(ctx, slackPostPayload{IssueID: issueID, Kind: kind, Reply: text, AuthorID: authorID, Status: status})
	}
}

type issueSlackQuerier interface {
	issueEmailQuerier
	GetIssueSlackMessage(ctx context.Context, issueID int64) (db.GetIssueSlackMessageRow, error)
	InsertIssueSlackMessage(ctx context.Context, arg db.InsertIssueSlackMessageParams) error
}

// SlackNotifier posts to the support team's channel, from the worker or,
// without a queue, from the request. With no poster it posts nothing.
type SlackNotifier struct {
	q       issueSlackQuerier
	poster  SlackPoster
	channel string
	baseURL string
}

func NewSlackNotifier(q issueSlackQuerier, poster SlackPoster, channel, baseURL string) *SlackNotifier {
	return &SlackNotifier{q: q, poster: poster, channel: channel, baseURL: baseURL}
}

// RegisterHandlers wires the Slack task onto the shared asynq mux.
func (n *SlackNotifier) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskSlackPost, n.HandleSlackPost)
}

// HandleSlackPost is the asynq handler. A failed post is returned, so asynq
// retries it, unless Slack refused it for a reason a retry cannot fix. The
// shared task error handler logs and reports it.
func (n *SlackNotifier) HandleSlackPost(ctx context.Context, t *asynq.Task) error {
	var p slackPostPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("decode issue slack task: %v: %w", err, asynq.SkipRetry)
	}
	err := n.deliver(ctx, p)
	if slack.Permanent(err) {
		return fmt.Errorf("%w: %w", err, asynq.SkipRetry)
	}
	return err
}

// deliver loads the issue and posts the message. The new-issue post becomes
// the issue's card. A reply or a note goes in the card's thread when the card
// is in the current channel, else to the channel; it never becomes the card.
// An issue deleted in the meantime posts nothing.
func (n *SlackNotifier) deliver(ctx context.Context, p slackPostPayload) error {
	if n.poster == nil {
		logging.Info(ctx, "slack is not configured; dropped an issue post", "issue-slack-skipped", logging.IssueID(p.IssueID))
		return nil
	}
	if p.Kind == slackRefresh {
		return n.refresh(ctx, p.IssueID)
	}
	row, err := n.q.GetIssueForPlatform(ctx, p.IssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		logging.Info(ctx, "issue gone before its slack post", "issue-slack-skipped", logging.IssueID(p.IssueID))
		return nil
	}
	if err != nil {
		return fmt.Errorf("load issue for slack: %w", err)
	}
	thread, err := n.q.GetIssueSlackMessage(ctx, p.IssueID)
	hasThread := err == nil && thread.Channel == n.channel
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load issue slack thread: %w", err)
	}
	author := row.ReporterName
	if p.AuthorID != 0 {
		if author, err = userName(ctx, n.q, p.AuthorID); err != nil {
			return err
		}
	}
	text, err := slackText(n.baseURL, p, row, author, hasThread)
	if err != nil {
		return err
	}
	m := slack.Message{Channel: n.channel, Text: text}
	if hasThread {
		m.ThreadTS = thread.Ts
	}
	ts, err := n.poster.PostMessage(ctx, m)
	if err != nil {
		return err
	}
	if p.Kind != slackNewIssue {
		return nil
	}
	if err := n.q.InsertIssueSlackMessage(ctx, db.InsertIssueSlackMessageParams{
		IssueID: p.IssueID, Channel: n.channel, Ts: ts,
	}); err != nil {
		// Posted: a retry would post it twice. The next reply starts a thread.
		logging.Error(ctx, err, "saving an issue's slack thread failed", "issue-slack-thread-save-failed", logging.IssueID(p.IssueID))
	}
	return nil
}

// refresh rewrites the issue's card where it was posted, or, for an issue
// that is gone, replaces it with a line that keeps none of the report. An
// issue never posted has no card.
func (n *SlackNotifier) refresh(ctx context.Context, issueID int64) error {
	card, err := n.q.GetIssueSlackMessage(ctx, issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load issue slack card: %w", err)
	}
	text := slackDeletedText(issueID)
	row, err := n.q.GetIssueForPlatform(ctx, issueID)
	switch {
	case err == nil:
		text = slackCard(n.baseURL, row)
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("load issue for slack: %w", err)
	}
	return n.poster.UpdateMessage(ctx, card.Channel, card.Ts, text)
}

// report logs a failed post and sends it to error tracking: the request's
// path, which has no task error handler. Slack's error is its error code
// alone, never the message.
func (n *SlackNotifier) report(ctx context.Context, p slackPostPayload, err error) {
	logging.Error(ctx, err, "posting an issue to slack failed", "issue-slack-post-failed", logging.IssueID(p.IssueID))
	errtrack.Capture(ctx, err, errtrack.CaptureOptions{
		Class: errtrack.ClassSlackPostFailed, Message: "failed to post " + p.Kind + " to slack",
		Subsystem: "slack", Event: p.Kind,
	})
}

// slackDeletedText is the card of a deleted issue: its number alone.
func slackDeletedText(issueID int64) string {
	return fmt.Sprintf(":wastebasket: ~#%d~ deleted", issueID)
}

// slackIssueLink links to the issue in the operator console.
func slackIssueLink(baseURL string, row db.GetIssueForPlatformRow) string {
	return slack.Link(emaillayout.AppURL(baseURL, fmt.Sprintf("/platform/issues/%d", row.ID)),
		fmt.Sprintf("#%d %s", row.ID, row.Title))
}

// slackCard is the mrkdwn of an issue's first post: its status, a link, who
// reported it, how the team triaged it, and the description.
func slackCard(baseURL string, row db.GetIssueForPlatformRow) string {
	status := "*" + statusLabels[row.Status] + "*"
	if row.ConfirmedAt.Valid {
		status += " (fix confirmed)"
	}
	triage := fmt.Sprintf("*Kind:* %s  ·  *Area:* %s", KindSummary(row.Kind, ptr.Deref(row.Severity)), areaLabels[row.Area])
	if row.AssigneeName != nil {
		triage += "  ·  *Assignee:* " + slack.EscapeLine(*row.AssigneeName)
	}
	return fmt.Sprintf("%s %s  %s\n*Organization:* %s  ·  *Reporter:* %s\n%s\n%s",
		slackStatusEmoji[row.Status], status, slackIssueLink(baseURL, row),
		slack.EscapeLine(row.OrgName), slack.EscapeLine(row.ReporterName), triage,
		slack.Quote(truncate(plainText(row.Description), slackPreviewLength)))
}

// slackText is the mrkdwn of one post about row, a reply or a note under
// author's name. inThread: a post in the issue's thread, which needs no link
// back to the issue.
func slackText(baseURL string, p slackPostPayload, row db.GetIssueForPlatformRow, author string, inThread bool) (string, error) {
	var action string
	switch {
	case p.Kind == slackNewIssue:
		return slackCard(baseURL, row), nil
	case p.Kind == slackNote:
		action = "added an internal note"
	case p.Kind == slackReply && p.Status != "":
		action = "changed the status to " + statusLabels[p.Status]
	case p.Kind == slackReply:
		action = "replied"
	default:
		return "", fmt.Errorf("unknown issue slack kind %q", p.Kind)
	}
	head := "*" + slack.EscapeLine(author) + " " + action
	quoted := slack.Quote(truncate(p.Reply, slackPreviewLength))
	if inThread {
		return head + ":*\n" + quoted, nil
	}
	return head + " on* " + slackIssueLink(baseURL, row) + "\n" + quoted, nil
}
