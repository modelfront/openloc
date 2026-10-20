package hold_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/hold"
	"openloc/internal/httputil/ssetest"
	"openloc/internal/testutil"
)

// streamServer serves the API with a listening hold feed.
func streamServer(t *testing.T, f *holdFixture) *httptest.Server {
	t.Helper()
	return drainableStreamServer(t, f, nil)
}

func drainableStreamServer(t *testing.T, f *holdFixture, draining <-chan struct{}) *httptest.Server {
	t.Helper()
	feed := startListening(t, hold.NewFeed(), f.pool.Config().ConnConfig)
	subscribe(t, feed, f.job.ID)
	srv := httptest.NewServer(testutil.NewTestRouterWithHoldFeed(f.pool, feed, draining))
	t.Cleanup(srv.Close)
	return srv
}

func streamRequest(t *testing.T, ctx context.Context, srv *httptest.Server, f *holdFixture, u db.User, jobID int64) *http.Response {
	t.Helper()
	tenant := f.tenant
	if u.ID == f.otherUser.ID {
		tenant = f.otherTenant
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/jobs/%d/holds/stream", srv.URL, jobID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", testutil.AuthHeaderWithRole(u.ID, tenant, f.roles[u.ID]))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// eventNames reads the stream's event names until it ends.
func eventNames(resp *http.Response) <-chan string {
	out := make(chan string, 16)
	go func() {
		defer close(out)
		for frame := range ssetest.Frames(resp.Body) {
			if name := ssetest.Event(frame); name != "" {
				out <- name
			}
		}
	}()
	return out
}

func expectEvent(t *testing.T, events <-chan string, want string) {
	t.Helper()
	if got := testutil.Receive(t, events, "event "+want); got != want {
		t.Fatalf("event = %q, want %q", got, want)
	}
}

func TestIntegrationHoldStream_TellsTheJobsEditorsWhenHoldsChange(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	srv := streamServer(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp := streamRequest(t, ctx, srv, f, f.pm, f.job.ID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream answered %d %q, want 200 text/event-stream", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	events := eventNames(resp)
	expectEvent(t, events, "connected")

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-ana"), http.StatusOK)
	expectEvent(t, events, "holds")

	testutil.AssertStatus(t, f.do(t, f.linguist, http.MethodDelete, holdPath(f.job.ID, f.t1.ID)+"?tab_id=tab-ana", nil), http.StatusNoContent)
	expectEvent(t, events, "holds")
}

// The stream is gated like the hold list: the job's organization, and a
// linguist only on a job assigned to them.
func TestIntegrationHoldStream_IsScopedLikeTheList(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	srv := streamServer(t, f)

	for _, tc := range []struct {
		name  string
		user  db.User
		jobID int64
		want  int
	}{
		{"unassigned linguist", f.outsider, f.job.ID, http.StatusForbidden},
		{"another organization", f.otherUser, f.job.ID, http.StatusNotFound},
		{"assigned linguist", f.linguist, f.job.ID, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testutil.Within)
			defer cancel()
			resp := streamRequest(t, ctx, srv, f, tc.user, tc.jobID)
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

// A process whose feed is not listening tells no editor it is live: the editor
// keeps its short poll. The refusal is 204, which tells EventSource to stop
// (the editor reopens later); it is expected during a deploy or a database
// restart, so it must not count as a server fault the way a 5xx does.
func TestIntegrationHoldStream_RefusedWhileTheFeedIsNotListening(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	srv := httptest.NewServer(testutil.NewTestRouterWithHoldFeed(f.pool, hold.NewFeed(), nil))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.Within)
	defer cancel()
	resp := streamRequest(t, ctx, srv, f, f.pm, f.job.ID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

// When the feed loses its database connection, open streams end, so their
// editors fall back to the short poll until the feed is back.
func TestIntegrationHoldStream_EndsWhenTheFeedLosesItsConnection(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	srv := streamServer(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp := streamRequest(t, ctx, srv, f, f.pm, f.job.ID)
	defer resp.Body.Close()
	events := eventNames(resp)
	expectEvent(t, events, "connected")

	terminateListener(t, f)
	testutil.Closed(t, events, "the stream after the feed lost its connection")
}

// The stream ends when the server starts shutting down, so open editors do
// not hold the shutdown to its deadline; they reconnect to a replica that is
// still up.
func TestIntegrationHoldStream_EndsOnDrain(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	draining := make(chan struct{})
	srv := drainableStreamServer(t, f, draining)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp := streamRequest(t, ctx, srv, f, f.pm, f.job.ID)
	defer resp.Body.Close()
	events := eventNames(resp)
	expectEvent(t, events, "connected")

	close(draining)
	testutil.Closed(t, events, "the stream after draining began")
}
