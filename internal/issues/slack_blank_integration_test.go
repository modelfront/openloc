package issues_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"openloc/internal/issues"
	"openloc/internal/slack"
	"openloc/internal/testutil"
)

// queuedBlanks is how many cards wait for the sweep.
func (c *cardFixture) queuedBlanks(t *testing.T) int64 {
	t.Helper()
	return testutil.CountRows(t, c.pool, "SELECT count(*) FROM issue_slack_blanks")
}

// deleteOrg hard-deletes the fixture's org, which queues its issues' cards.
func (c *cardFixture) deleteOrg(t *testing.T, cards int64) {
	t.Helper()
	if err := c.fixture.q.DeleteTenant(context.Background(), c.tenant.ID); err != nil {
		t.Fatalf("delete org: %v", err)
	}
	if n := c.queuedBlanks(t); n != cards {
		t.Fatalf("queued blanks = %d after the org delete, want its %d cards", n, cards)
	}
}

// An org delete removes its issues with their cards' rows. The sweep then
// blanks each card in Slack and forgets it.
func TestIntegrationSlackBlanksTheCardsOfADeletedOrg(t *testing.T) {
	c := newCardFixture(t)
	c.deleteOrg(t, 1)
	s := &fakeSlack{}
	n, err := issues.NewSlackBlankSweeper(c.fixture.q, s).Sweep(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v; want the org's one card blanked", n, err)
	}
	want := slackUpdate{slackChannel, "1700000000.000001", fmt.Sprintf(":wastebasket: ~#%d~ deleted", c.issue.ID)}
	if len(s.updates) != 1 || s.updates[0] != want {
		t.Errorf("updates = %+v, want %+v", s.updates, want)
	}
	if q := c.queuedBlanks(t); q != 0 {
		t.Errorf("queued blanks = %d after the sweep, want 0", q)
	}
}

// What a failed edit does to the queued card: a card Slack no longer has
// leaves the queue; a refusal of this one message or its channel skips the
// card, which stays queued; any other failure (no answer, Slack's own errors,
// rate limiting, a refused token, a code nobody knows) ends the run and keeps
// it.
func TestIntegrationSlackBlankSweepFailures(t *testing.T) {
	for _, tc := range []struct {
		err     error
		wantErr bool
		queued  int64
	}{
		{errors.New("connection reset"), true, 1},
		{&slack.APIError{Code: "ratelimited"}, true, 1},
		{&slack.APIError{Code: "invalid_auth"}, true, 1},
		{&slack.APIError{Code: "token_revoked"}, true, 1},
		{&slack.APIError{Code: "missing_scope"}, true, 1},
		{&slack.APIError{Code: "not_allowed_token_type"}, true, 1},
		{&slack.APIError{Code: "internal_error"}, true, 1},
		{&slack.APIError{Code: "service_unavailable"}, true, 1},
		{&slack.APIError{Code: "an_unknown_code"}, true, 1},
		{&slack.APIError{Code: "not_in_channel"}, false, 1},
		{&slack.APIError{Code: "channel_not_found"}, false, 1},
		{&slack.APIError{Code: "is_inactive"}, false, 1},
		{&slack.APIError{Code: "invalid_arguments"}, false, 1},
		{&slack.APIError{Code: "message_not_found"}, false, 0},
		{&slack.APIError{Code: "cant_update_message"}, false, 0},
		{&slack.APIError{Code: "edit_window_closed"}, false, 0},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			c := newCardFixture(t)
			c.deleteOrg(t, 1)
			n, err := issues.NewSlackBlankSweeper(c.fixture.q, &fakeSlack{err: tc.err}).Sweep(context.Background())
			if (err != nil) != tc.wantErr {
				t.Errorf("sweep error = %v, want error: %v", err, tc.wantErr)
			}
			if q := c.queuedBlanks(t); q != tc.queued {
				t.Errorf("queued blanks = %d, want %d", q, tc.queued)
			}
			if want := int(1 - tc.queued); n != want {
				t.Errorf("sweep = %d cards done, want %d", n, want)
			}
		})
	}
}

// A card Slack refuses for its own reasons does not hold up the cards
// after it: the sweep skips it, keeps it queued and blanks the next.
func TestIntegrationSlackBlankSweepSkipsARefusedCard(t *testing.T) {
	for _, code := range []string{"not_in_channel", "invalid_arguments"} {
		t.Run(code, func(t *testing.T) {
			c := newCardFixture(t)
			second, err := c.svc.Create(context.Background(), c.member(), newIssue("Second"))
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			c.work(t)
			c.deleteOrg(t, 2)
			s := &fakeSlack{errFor: map[string]error{"1700000000.000001": &slack.APIError{Code: code}}}
			n, err := issues.NewSlackBlankSweeper(c.fixture.q, s).Sweep(context.Background())
			if err != nil || n != 1 {
				t.Fatalf("sweep = %d, %v; want the second card blanked past the refused first", n, err)
			}
			want := slackUpdate{slackChannel, "1700000000.000002", fmt.Sprintf(":wastebasket: ~#%d~ deleted", second.ID)}
			if len(s.updates) != 1 || s.updates[0] != want {
				t.Errorf("updates = %+v, want %+v", s.updates, want)
			}
			if q := testutil.CountRows(t, c.pool, "SELECT count(*) FROM issue_slack_blanks WHERE issue_id = $1", c.issue.ID); q != 1 {
				t.Errorf("the refused card is queued %d times, want it kept once", q)
			}
		})
	}
}

// A failure that is not about one card ends the run at once: the cards after
// it are not touched and stay queued, and the error names the issue whose
// card stopped the run.
func TestIntegrationSlackBlankSweepStopsAtAHaltingFailure(t *testing.T) {
	for _, code := range []string{"internal_error", "an_unknown_code"} {
		t.Run(code, func(t *testing.T) {
			c := newCardFixture(t)
			if _, err := c.svc.Create(context.Background(), c.member(), newIssue("Second")); err != nil {
				t.Fatalf("create: %v", err)
			}
			c.work(t)
			c.deleteOrg(t, 2)
			s := &fakeSlack{errFor: map[string]error{"1700000000.000001": &slack.APIError{Code: code}}}
			n, err := issues.NewSlackBlankSweeper(c.fixture.q, s).Sweep(context.Background())
			if err == nil || n != 0 {
				t.Fatalf("sweep = %d, %v; want the run stopped at the first card", n, err)
			}
			if want := fmt.Sprintf("issue %d", c.issue.ID); !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to name %s", err, want)
			}
			if len(s.updates) != 0 {
				t.Errorf("updates = %+v: the run went on past a failure that is not about one card", s.updates)
			}
			if q := c.queuedBlanks(t); q != 2 {
				t.Errorf("queued blanks = %d, want both cards kept", q)
			}
		})
	}
}
