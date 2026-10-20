// SSE handler contract.
//
// What we pin:
//   1. Connection emits an `event: connected` with the current unread count
//      so the bell badge hydrates without a separate REST round-trip.
//   2. A subsequent broker.Publish for the same user produces an
//      `event: notification` with the payload shape the FE expects.
//   3. A publish for a DIFFERENT user does NOT reach this subscriber
//      (per-recipient routing flows through the broker → SSE).
//
// Anti-tautology: removing the `connected` write in HandleStream makes
// test #1 fail with "expected event: connected, got <empty>".

package notification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil/ssetest"
)

// stubCounter satisfies UnreadCounter without dragging in the full
// db.Querier surface (200+ methods).
type stubCounter struct{ unread int64 }

func (s *stubCounter) CountUnreadNotifications(_ context.Context, _ db.CountUnreadNotificationsParams) (int64, error) {
	return s.unread, nil
}

// newSSETestServer wires the SSEHandler behind a test server that
// injects the user ID via the auth context (mimics what the auth
// middleware does in production).
func newSSETestServer(t *testing.T, broker Broker, unread int64, userID int64) *httptest.Server {
	t.Helper()
	return newSSETestServerWithCounter(t, broker, &stubCounter{unread: unread}, userID)
}

// newSSETestServerWithCounter is the same harness with a caller-supplied
// UnreadCounter — used by tests that probe handler-internal ordering via
// the counter hook.
func newSSETestServerWithCounter(t *testing.T, broker Broker, counter UnreadCounter, userID int64) *httptest.Server {
	t.Helper()
	// nil draining never fires: these tests exercise the client-disconnect
	// lifecycle only.
	return newSSETestServerDraining(t, broker, counter, userID, nil)
}

// newSSETestServerDraining is the harness for the shutdown path — the caller
// supplies the channel main.go closes when the process starts draining.
func newSSETestServerDraining(t *testing.T, broker Broker, counter UnreadCounter, userID int64, draining <-chan struct{}) *httptest.Server {
	t.Helper()
	handler := NewSSEHandler(broker, counter, draining)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithClaims(r.Context(), &auth.Claims{UserID: userID, TenantID: 1})
		handler.HandleStream(w, r.WithContext(ctx))
	}))
	t.Cleanup(server.Close)
	return server
}

// getStream GETs the stream on server. The request ends after timeout, and
// the response closes before the server does.
func getStream(t *testing.T, server *httptest.Server, timeout time.Duration) (*http.Response, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, ctx
}

// readSSEFrame returns the stream's next frame, failing the test when none
// comes within timeout or the stream ends first.
func readSSEFrame(t *testing.T, frames <-chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case frame, open := <-frames:
		if !open {
			t.Fatal("the stream ended before its next frame")
		}
		return frame
	case <-time.After(timeout):
		t.Fatalf("no SSE frame within %v", timeout)
	}
	return ""
}

// subscriberCountProbe records the broker's SubscriberCount at the moment
// the handler hydrates the unread badge. The hydrate call runs on the
// handler goroutine strictly BEFORE the `connected` write, so the recorded
// value deterministically distinguishes subscribe-then-connected (count=1)
// from connected-then-subscribe (count=0) — no sleeps, no scheduler races.
type subscriberCountProbe struct {
	broker         *InProcessBroker
	countAtHydrate chan int // buffered(1); receives once per connection
}

func (p *subscriberCountProbe) CountUnreadNotifications(_ context.Context, _ db.CountUnreadNotificationsParams) (int64, error) {
	p.countAtHydrate <- p.broker.SubscriberCount()
	return 0, nil
}

// The handler MUST register its broker
// subscription before the client can observe the `connected` event.
// Otherwise Service.Send can publish for this user in the gap and the
// broker drops the event (no subscriber registered yet).
//
// Anti-tautology: swapping Subscribe back below the `connected` write in
// HandleStream makes this fail deterministically with "count = 0, want 1".
func TestSSEHandler_SubscribesBeforeConnectedEvent(t *testing.T) {
	broker := NewInProcessBroker()
	probe := &subscriberCountProbe{broker: broker, countAtHydrate: make(chan int, 1)}
	server := newSSETestServerWithCounter(t, broker, probe, 42)

	getStream(t, server, 2*time.Second)

	select {
	case got := <-probe.countAtHydrate:
		if got != 1 {
			t.Errorf("SubscriberCount at unread-hydrate = %d, want 1 (handler must Subscribe before writing `connected`; events published in the gap are silently dropped)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never called CountUnreadNotifications within 2s")
	}
}

func TestSSEHandler_EmitsConnectedEventWithUnreadCount(t *testing.T) {
	server := newSSETestServer(t, NewInProcessBroker(), 7, 42)

	resp, _ := getStream(t, server, 2*time.Second)

	got := readSSEFrame(t, ssetest.Frames(resp.Body), 2*time.Second)
	if !strings.Contains(got, "event: connected") {
		t.Errorf("expected 'event: connected' line, got %q", got)
	}
	if !strings.Contains(got, `"unread":7`) {
		t.Errorf("expected unread=7 in connected payload, got %q", got)
	}
}

func TestSSEHandler_DeliversBrokerPublishToSubscriber(t *testing.T) {
	broker := NewInProcessBroker()
	server := newSSETestServer(t, broker, 0, 42)

	resp, ctx := getStream(t, server, 3*time.Second)

	frames := ssetest.Frames(resp.Body)
	// Drain the connected event first.
	_ = readSSEFrame(t, frames, 2*time.Second)

	// Wait for the subscriber to be registered. The handler subscribes
	// before sending the connected event, so by the time we've drained
	// the connected event the subscription is live.
	if err := broker.Publish(ctx, Event{
		Type: EventDeadline, TenantID: 1, RecipientID: 42, JobID: 7,
		ProjectName: "Test", FileName: "doc.txt", TargetLang: "fr",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := readSSEFrame(t, frames, 2*time.Second)
	if !strings.Contains(got, "event: notification") {
		t.Errorf("expected 'event: notification', got %q", got)
	}
	if !strings.Contains(got, `"job_id":7`) {
		t.Errorf("expected job_id=7 in payload, got %q", got)
	}
	// The frame carries what a notice between processes carries: the type and
	// ids, never the names the event holds here.
	data := ssetest.Data(got)
	var fields map[string]any
	if err := json.Unmarshal([]byte(data), &fields); err != nil {
		t.Fatalf("frame data %q: %v", data, err)
	}
	if len(fields) != 3 || fields["type"] == nil || fields["job_id"] == nil || fields["project_id"] == nil {
		t.Errorf("frame fields %v, want exactly type, job_id and project_id", fields)
	}
}

func TestSSEHandler_OtherUsersEventsAreNotDelivered(t *testing.T) {
	broker := NewInProcessBroker()
	server := newSSETestServer(t, broker, 0, 42)

	resp, ctx := getStream(t, server, 3*time.Second)

	frames := ssetest.Frames(resp.Body)
	// Drain the connected event first.
	_ = readSSEFrame(t, frames, 2*time.Second)

	// Publish for a DIFFERENT user in the session's own org, so only the
	// recipient routing can keep it out, then for this user. The stream keeps
	// order, so the first frame is user 99's unless the routing drops it — no
	// timeout decides the result.
	for _, ev := range []Event{
		{Type: EventDeadline, TenantID: 1, RecipientID: 99, JobID: 100},
		{Type: EventDeadline, TenantID: 1, RecipientID: 42, JobID: 101},
	} {
		if err := broker.Publish(ctx, ev); err != nil {
			t.Fatalf("Publish job %d: %v", ev.JobID, err)
		}
	}
	if got := readSSEFrame(t, frames, 2*time.Second); !strings.Contains(got, `"job_id":101`) {
		t.Fatalf("subscriber for user 42 got %q first, want its own job 101 — broker routing leaked user 99's event", got)
	}
}

// Pins that closing `draining` ends the stream so http.Server.Shutdown can
// finish. Shutdown waits for every connection to become
// idle and never cancels request contexts, so without the signal a deploy with
// one browser open burns the full grace period and exits non-zero.
func TestSSEHandler_ClosesOnDrain(t *testing.T) {
	draining := make(chan struct{})
	server := newSSETestServerDraining(t, NewInProcessBroker(), &stubCounter{}, 42, draining)

	// No request-scoped timeout: the point is that the SERVER ends the stream.
	// A ctx deadline here would pass even with the fix reverted.
	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()

	// Wait until the handler is actually inside its select loop, so closing
	// `draining` can't race the connect event.
	frames := ssetest.Frames(resp.Body)
	if got := readSSEFrame(t, frames, 2*time.Second); !strings.Contains(got, "event: connected") {
		t.Fatalf("stream did not open: %q", got)
	}

	close(draining)

	// The handler returning ends the response body, which closes frames (EOF
	// or a read error from the closed connection): the server let go, which
	// is what lets Shutdown complete.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, open := <-frames:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("stream still open 3s after draining — http.Server.Shutdown would hit its deadline")
		}
	}
}

// Every stream on a process ends at once on a deploy or when the broker loses
// its feed. The stream tells the browser, with `connected`, to wait a random
// 1–20 s before it reconnects, so the reconnects and their refetches spread
// out.
func TestSSEHandler_TheStreamSpreadsItsReconnect(t *testing.T) {
	server := newSSETestServer(t, NewInProcessBroker(), 0, 42)

	resp, _ := getStream(t, server, 2*time.Second)
	got := readSSEFrame(t, ssetest.Frames(resp.Body), 2*time.Second)
	if !strings.Contains(got, "event: connected") {
		t.Fatalf("stream did not open: %q", got)
	}
	m := regexp.MustCompile(`retry: (\d+)\n`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("connected carries no retry: %q", got)
	}
	ms, err := strconv.Atoi(m[1])
	if err != nil || ms < 1000 || ms >= 20000 {
		t.Fatalf("retry %q, want 1000 to 19999 ms", m[1])
	}
}

// The delay differs from stream to stream, so the browsers of one process do
// not come back together, and stays inside 1–20 s.
func TestReconnectJitter_SpreadsInsideItsRange(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 50 {
		d := reconnectJitter()
		if d < time.Second || d >= 20*time.Second {
			t.Fatalf("delay %s, want 1s to 20s", d)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Fatal("the delay is the same for every stream")
	}
}
