package notification_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil/ssetest"
	notif "openloc/internal/notification"
	"openloc/internal/testutil"
)

// openStream opens the bell's stream on process p for the user, in the
// user's tenant, and returns its frames; the channel closes when the stream
// ends.
func openStream(t *testing.T, p *process, userID, tenantID int64) <-chan string {
	t.Helper()
	handler := notif.NewSSEHandler(p.broker, db.New(p.pool), nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithClaims(r.Context(), &auth.Claims{UserID: userID, TenantID: tenantID})
		handler.HandleStream(w, r.WithContext(ctx))
	}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		server.Close()
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return ssetest.Frames(resp.Body)
}

// openConnectedStream is openStream, failing the test unless the stream
// opens with `connected`.
func openConnectedStream(t *testing.T, p *process, userID, tenantID int64) <-chan string {
	t.Helper()
	frames := openStream(t, p, userID, tenantID)
	if got := testutil.Receive(t, frames, "stream opened"); !strings.Contains(got, "event: connected") {
		t.Fatalf("stream did not open with connected: %q", got)
	}
	return frames
}

// gatedQuerier holds CreateNotification until release closes, and closes
// entered when the call arrives.
type gatedQuerier struct {
	db.Querier
	entered, release chan struct{}
}

func (g gatedQuerier) CreateNotification(ctx context.Context, arg db.CreateNotificationParams) (db.Notification, error) {
	close(g.entered)
	<-g.release
	return g.Querier.CreateNotification(ctx, arg)
}

// The real path: Service.Send on one process writes the row, then publishes;
// the bell's stream on the other process gets no frame while the row is not
// written, and the frame once it is.
func TestIntegrationBroker_SendOnOneProcessReachesTheStreamOnTheOther(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenant := testutil.SeedTenant(t, q, "CrossPodCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "member@crosspod.test", "pw")
	a, b := startProcess(t, pool, "a"), startProcess(t, pool, "b")
	frames := openConnectedStream(t, b, user.ID, tenant.ID)

	gate := gatedQuerier{Querier: db.New(a.pool), entered: make(chan struct{}), release: make(chan struct{})}
	svc := notif.NewServiceWithBroker(gate, &spySender{}, a.broker, "")
	sent := make(chan error, 1)
	go func() {
		sent <- svc.Send(context.Background(), notif.Event{
			Type: notif.EventDeadline, TenantID: tenant.ID, RecipientID: user.ID, JobID: 77, ProjectName: "Visible only in the DB",
		})
	}()
	select {
	case <-gate.entered:
	case <-time.After(testutil.Within):
		t.Fatal("Send never wrote the row")
	}
	testutil.ReceiveNone(t, frames, "a frame before the row is written")
	close(gate.release)

	got := testutil.Receive(t, frames, "the notification frame")
	if !strings.Contains(got, "event: notification") || !strings.Contains(got, `"job_id":77`) {
		t.Fatalf("expected the notification frame for job 77, got %q", got)
	}
	if strings.Contains(got, "Visible only in the DB") {
		t.Fatalf("the frame carries text: %q", got)
	}
	unread, err := db.New(b.pool).CountUnreadNotifications(context.Background(), db.CountUnreadNotificationsParams{UserID: user.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	if unread != 1 {
		t.Fatalf("unread = %d when the frame arrived, want the row (1)", unread)
	}
	if err := <-sent; err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// A lost listen connection ends the bell's open streams on that process, so
// the browser reconnects; once the process listens again, a new stream opens
// with `connected`, which makes the bell reread the DB.
func TestIntegrationBroker_ALostListenerEndsTheStream(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenant := testutil.SeedTenant(t, q, "CrossPodCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "member@crosspod.test", "pw")
	b := startProcess(t, pool, "b")
	frames := openConnectedStream(t, b, user.ID, tenant.ID)

	b.terminateListener(t)
	testutil.Closed(t, frames, "the stream on the process that lost its listener")

	b.waitLive(t)
	openConnectedStream(t, b, user.ID, tenant.ID)
}

// The tenant travels with the notice: a stream signed in to one org gets the
// user's notices from that org only, whichever process published them. The
// other org's notice goes first, and the sender keeps order, so the first
// frame is that one unless the stream drops it.
func TestIntegrationBroker_AStreamOnTheOtherProcessKeepsToItsOrg(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	own := testutil.SeedTenant(t, q, "OwnOrg")
	other := testutil.SeedTenant(t, q, "OtherOrg")
	user := testutil.SeedUser(t, q, own.ID, "member@crosspod.test", "pw")
	a, b := startProcess(t, pool, "a"), startProcess(t, pool, "b")
	frames := openConnectedStream(t, b, user.ID, own.ID)

	for _, ev := range []notif.Event{
		{Type: notif.EventAssignment, TenantID: other.ID, RecipientID: user.ID, JobID: 200},
		{Type: notif.EventAssignment, TenantID: own.ID, RecipientID: user.ID, JobID: 100},
	} {
		if err := a.broker.Publish(context.Background(), ev); err != nil {
			t.Fatalf("Publish job %d: %v", ev.JobID, err)
		}
	}
	if got := testutil.Receive(t, frames, "the own org's notification"); !strings.Contains(got, `"job_id":100`) {
		t.Fatalf("the stream got %q first, want job 100's frame: the other org's notice reached it", got)
	}
}
