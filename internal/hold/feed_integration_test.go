package hold_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/hold"
	"openloc/internal/pglisten"
	"openloc/internal/testutil"
)

// settle drops a wake-up still pending from a change that sent two notices
// (a move deletes one hold and inserts another), so the next check counts
// only what follows.
func settle(ch <-chan struct{}) {
	time.Sleep(testutil.QuietFor)
	select {
	case <-ch:
	default:
	}
}

// listenerName finds the test's listen connection in pg_stat_activity.
const listenerName = "hold-feed-test"

// terminateListener ends the feed's listen connection from the server side.
func terminateListener(t *testing.T, f *holdFixture) {
	t.Helper()
	testutil.TerminateConnection(t, f.pool, listenerName)
}

// startListening runs a listener for the feed on cfg; subscribe waits until
// it receives.
func startListening(t *testing.T, feed *hold.Feed, cfg *pgx.ConnConfig) *hold.Feed {
	t.Helper()
	l := pglisten.New()
	feed.ListenOn(l)
	testutil.RunListener(t, l, testutil.ListenerConfig(cfg, listenerName))
	return feed
}

// subscribe waits until the feed receives, then subscribes to the job.
func subscribe(t *testing.T, feed *hold.Feed, jobID int64) (changes, down <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	testutil.WaitFor(t, func() bool {
		var ok bool
		changes, down, ok = feed.Subscribe(ctx, jobID)
		return ok
	}, "the feed receiving")
	return changes, down
}

func startFeed(t *testing.T, f *holdFixture) (jobA, jobB <-chan struct{}) {
	t.Helper()
	feed := startListening(t, hold.NewFeed(), f.pool.Config().ConnConfig)
	jobA, _ = subscribe(t, feed, f.job.ID)
	jobB, _ = subscribe(t, feed, f.otherJob.ID)
	return jobA, jobB
}

func expire(t *testing.T, f *holdFixture, tr db.Translation, ago string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE translation_holds SET expires_at = now() - $2::interval WHERE translation_id = $1`, tr.ID, ago); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationHoldFeed_VisibleChangesWakeTheJobsEditors(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	jobA, jobB := startFeed(t, f)

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-ana"), http.StatusOK)
	testutil.Receive(t, jobA, "a hold taken")
	testutil.ReceiveNone(t, jobB, "another job's editors")

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-ana"), http.StatusOK)
	testutil.ReceiveNone(t, jobA, "a renewal changes nothing others see")

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t2, "tab-ana"), http.StatusOK)
	testutil.Receive(t, jobA, "the tab moved to another segment")
	settle(jobA)

	testutil.AssertStatus(t, f.do(t, f.linguist, http.MethodDelete, holdPath(f.job.ID, f.t2.ID)+"?tab_id=tab-ana", nil), http.StatusNoContent)
	testutil.Receive(t, jobA, "the hold released")
	testutil.ReceiveNone(t, jobB, "another job's editors")
}

func TestIntegrationHoldFeed_ExpiryAndSweepStayQuiet(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-ana"), http.StatusOK)
	jobA, _ := startFeed(t, f)

	// The list shows live holds only, so an expiry, and the sweep of the
	// expired row, tell the editors nothing new.
	expire(t, f, f.t1, "2 minutes")
	testutil.ReceiveNone(t, jobA, "a hold expiring")
	if _, err := f.q.DeleteExpiredHolds(context.Background()); err != nil {
		t.Fatal(err)
	}
	testutil.ReceiveNone(t, jobA, "an expired hold swept")
}

// An expired hold not swept yet is a row the next acquire updates in place:
// visible again, whoever takes it.
func TestIntegrationHoldFeed_TakingAnExpiredHoldIsVisible(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	jobA, _ := startFeed(t, f)
	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-ana"), http.StatusOK)
	testutil.Receive(t, jobA, "a hold taken")

	expire(t, f, f.t1, "5 seconds")
	testutil.AssertStatus(t, acquire(t, f, f.pm, f.t1, "tab-pm"), http.StatusOK)
	testutil.Receive(t, jobA, "another user takes the expired hold")

	expire(t, f, f.t1, "5 seconds")
	testutil.AssertStatus(t, acquire(t, f, f.pm, f.t1, "tab-pm"), http.StatusOK)
	testutil.Receive(t, jobA, "the same user takes their expired hold again")
}

// A lost listen connection tells every subscriber, and the feed receives
// again once it has reconnected.
func TestIntegrationHoldFeed_ALostConnectionEndsSubscriptions(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	feed := startListening(t, hold.NewFeed(), f.pool.Config().ConnConfig)
	_, down := subscribe(t, feed, f.job.ID)

	terminateListener(t, f)
	testutil.Closed(t, down, "subscribers told the feed stopped")

	changes, _ := subscribe(t, feed, f.job.ID)
	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-ana"), http.StatusOK)
	testutil.Receive(t, changes, "a hold taken after the reconnect")
}
