package issues

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/slack"
)

// Blanking of the Slack cards of deleted issues. A delete trigger on
// issue_slack_messages queues each card, so the cards of issues removed by an
// org or account delete (FK cascade) are blanked here; a soft delete queues
// its card too. A card leaves the queue once it is blanked or Slack no longer
// has it. A refusal of that one message or its channel skips the card, which
// stays queued, and the run goes on. Any other failure (no answer, Slack's
// own errors, rate limiting, a refused token, an unknown code) ends the run,
// and what is left waits for the next one.

// TaskSlackBlankSweep is the asynq task type for the hourly sweep.
// Producer: asynq.Scheduler in cmd/server/main.go. Consumer: SlackBlankSweeper.HandleSweep.
const TaskSlackBlankSweep = "scheduler:issue-slack-blank-sweep"

const (
	// slackBlankBatch is how many queued cards one query reads.
	slackBlankBatch = 100
	// slackBlankPace spaces the edits under chat.update's rate limit of
	// about 50 a minute.
	slackBlankPace = 1200 * time.Millisecond
)

// NewSlackBlankSweepTask returns the task the scheduler enqueues. No payload.
func NewSlackBlankSweepTask() *asynq.Task {
	return asynq.NewTask(TaskSlackBlankSweep, nil)
}

type slackBlankQuerier interface {
	ListIssueSlackBlanks(ctx context.Context, arg db.ListIssueSlackBlanksParams) ([]db.ListIssueSlackBlanksRow, error)
	DeleteIssueSlackBlank(ctx context.Context, issueID int64) error
}

var _ slackBlankQuerier = (db.Querier)(nil)

// SlackBlankSweeper blanks the queued cards of deleted issues.
type SlackBlankSweeper struct {
	q      slackBlankQuerier
	poster SlackPoster
	pace   time.Duration
}

// NewSlackBlankSweeper returns a sweeper that edits the cards through poster.
func NewSlackBlankSweeper(q slackBlankQuerier, poster SlackPoster) *SlackBlankSweeper {
	return &SlackBlankSweeper{q: q, poster: poster, pace: slackBlankPace}
}

// Sweep goes once through the queue, in issue order, and returns how many
// cards left it. It stops at the first failure that is not about one card.
func (b *SlackBlankSweeper) Sweep(ctx context.Context) (int, error) {
	done, edits := 0, 0
	var after int64
	for {
		cards, err := b.q.ListIssueSlackBlanks(ctx, db.ListIssueSlackBlanksParams{AfterID: after, BatchSize: slackBlankBatch})
		if err != nil {
			return done, fmt.Errorf("list queued slack cards: %w", err)
		}
		if len(cards) == 0 {
			return done, nil
		}
		for _, card := range cards {
			after = card.IssueID
			if edits > 0 {
				select {
				case <-ctx.Done():
					return done, ctx.Err()
				case <-time.After(b.pace):
				}
			}
			edits++
			err := b.poster.UpdateMessage(ctx, card.Channel, card.Ts, slackDeletedText(card.IssueID))
			switch {
			case err == nil || slack.MessageGone(err):
				if err := b.q.DeleteIssueSlackBlank(ctx, card.IssueID); err != nil {
					return done, fmt.Errorf("dequeue slack card: %w", err)
				}
				done++
			case slack.CardRefused(err):
				logging.Warn(ctx, err, "slack refused to blank a deleted issue's card; it stays queued",
					"issue-slack-blank-skipped", logging.IssueID(card.IssueID))
			default:
				return done, fmt.Errorf("blank slack card of issue %d: %w", card.IssueID, err)
			}
		}
	}
}

// RegisterHandlers wires the sweep onto the shared asynq mux.
func (b *SlackBlankSweeper) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskSlackBlankSweep, b.HandleSweep)
}

// HandleSweep is the asynq handler. A failure is returned so asynq retries;
// what is left stays queued for the next run either way.
func (b *SlackBlankSweeper) HandleSweep(ctx context.Context, _ *asynq.Task) error {
	n, err := b.Sweep(ctx)
	if err != nil {
		logging.Warn(ctx, err, "blanking the slack cards of deleted issues failed", "issue-slack-blank-failed",
			logging.TaskType(TaskSlackBlankSweep), logging.Count(n))
		return err
	}
	logging.Info(ctx, "slack cards of deleted issues blanked", "issue-slack-blanked",
		logging.TaskType(TaskSlackBlankSweep), logging.Count(n))
	return nil
}
