package pglisten_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/pglisten"
	"openloc/internal/testutil"
)

// appName finds the test's listen connection in pg_stat_activity.
const appName = "pglisten-test"

// recorder reports what the listener tells one channel's handler.
type recorder struct {
	ups, downs chan struct{}
	notices    chan string
}

func newRecorder() *recorder {
	return &recorder{ups: make(chan struct{}, 16), downs: make(chan struct{}, 16), notices: make(chan string, 16)}
}

func (r *recorder) Up()                                      { r.ups <- struct{}{} }
func (r *recorder) Down()                                    { r.downs <- struct{}{} }
func (r *recorder) Notify(_ context.Context, payload string) { r.notices <- payload }

// newListener is a listener whose handler for pglisten_a is the returned
// recorder.
func newListener() (*pglisten.Listener, *recorder) {
	h := newRecorder()
	l := pglisten.New()
	l.Handle("pglisten_a", h)
	return l, h
}

// listenerConfig is the pool's connection settings, named so the test finds
// the listen connection in pg_stat_activity.
func listenerConfig(pool *pgxpool.Pool) *pgx.ConnConfig {
	return testutil.ListenerConfig(pool.Config().ConnConfig, appName)
}

func notify(t *testing.T, pool *pgxpool.Pool, channel, payload string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "SELECT pg_notify($1, $2)", channel, payload); err != nil {
		t.Fatal(err)
	}
}

func terminateListener(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	testutil.TerminateConnection(t, pool, appName)
}

// One connection carries every channel; each notice reaches the handler of
// its own channel only.
func TestIntegrationListener_EachChannelReachesItsHandler(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	a, b := newRecorder(), newRecorder()
	l := pglisten.New()
	l.Handle("pglisten_a", a)
	l.Handle("pglisten_b", b)
	testutil.RunListener(t, l, listenerConfig(pool))
	testutil.Receive(t, a.ups, "channel a up")
	testutil.Receive(t, b.ups, "channel b up")

	var conns int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_stat_activity WHERE application_name = $1`, appName).Scan(&conns); err != nil {
		t.Fatal(err)
	}
	if conns != 1 {
		t.Fatalf("%d listen connections, want 1 for both channels", conns)
	}

	notify(t, pool, "pglisten_a", "for a")
	if got := testutil.Receive(t, a.notices, "a's notice"); got != "for a" {
		t.Fatalf("a got %q", got)
	}
	testutil.ReceiveNone(t, b.notices, "b hears a's channel")

	notify(t, pool, "pglisten_b", "for b")
	if got := testutil.Receive(t, b.notices, "b's notice"); got != "for b" {
		t.Fatalf("b got %q", got)
	}
	testutil.ReceiveNone(t, a.notices, "a hears b's channel")
}

// A lost connection tells every handler, and the listener reconnects and
// receives again.
func TestIntegrationListener_ALostConnectionIsReplaced(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	l, h := newListener()
	testutil.RunListener(t, l, listenerConfig(pool))
	testutil.Receive(t, h.ups, "first connection up")

	terminateListener(t, pool)
	testutil.Receive(t, h.downs, "handler told the connection ended")
	testutil.Receive(t, h.ups, "reconnected")

	notify(t, pool, "pglisten_a", "after")
	if got := testutil.Receive(t, h.notices, "a notice on the new connection"); got != "after" {
		t.Fatalf("got %q", got)
	}
}

// A connection that drops right after LISTEN (a database failing over) does
// not reset the pause: the reconnects keep backing off, and each one ends the
// streams its handlers serve. A connection that stayed up for a ping period
// resets it.
func TestIntegrationListener_AFlappingConnectionKeepsBackingOff(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	l, h := newListener()
	pglisten.SetRetryForTest(l, 200*time.Millisecond, 5*time.Second)
	pglisten.SetPingForTest(l, 500*time.Millisecond, time.Second)
	testutil.RunListener(t, l, listenerConfig(pool))
	testutil.Receive(t, h.ups, "first connection up")

	var gaps []time.Duration
	for range 3 {
		terminateListener(t, pool)
		start := time.Now()
		testutil.Receive(t, h.ups, "reconnected")
		gaps = append(gaps, time.Since(start))
	}
	// Pauses of 200, 400 and 800 ms; one that reset after each LISTEN would
	// stay at 200 ms.
	if gaps[2] < 700*time.Millisecond {
		t.Fatalf("reconnect gaps %v: the pause did not keep growing for a flapping connection", gaps)
	}

	time.Sleep(time.Second)
	terminateListener(t, pool)
	start := time.Now()
	testutil.Receive(t, h.ups, "reconnected after a stable connection")
	if gap := time.Since(start); gap > 1200*time.Millisecond {
		t.Fatalf("reconnect after a connection that stayed up took %s, want the pause reset to 200ms", gap)
	}
}

// A connection whose peer vanished without closing the socket (a lost
// database node) hears nothing forever; a ping finds it and the listener
// reconnects.
func TestIntegrationListener_AStalledConnectionIsReplaced(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	cfg := listenerConfig(pool)
	proxy := testutil.NewStallProxy(t, fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	cfg.Host, cfg.Port = "127.0.0.1", proxy.Port()
	l, h := newListener()
	pglisten.SetPingForTest(l, 200*time.Millisecond, time.Second)
	testutil.RunListener(t, l, cfg)
	testutil.Receive(t, h.ups, "first connection up")
	time.Sleep(time.Second)
	testutil.ReceiveNone(t, h.downs, "a healthy connection survives its pings")

	proxy.Stall()
	testutil.Receive(t, h.downs, "the stalled connection given up")
	testutil.Receive(t, h.ups, "reconnected")

	notify(t, pool, "pglisten_a", "after")
	if got := testutil.Receive(t, h.notices, "a notice on the new connection"); got != "after" {
		t.Fatalf("got %q", got)
	}
}

// A database that accepts the connection but never answers (a black-holed
// host after a failover) holds a connect attempt only for the connect
// timeout: the listener gives up, retries, and receives once the database
// answers again.
func TestIntegrationListener_AStalledConnectIsRetried(t *testing.T) {
	expectStallRetried(t, (*testutil.StallProxy).StallNew)
}

// The same for a database that completes the handshake and then never
// answers the LISTEN.
func TestIntegrationListener_AStalledListenIsRetried(t *testing.T) {
	expectStallRetried(t, (*testutil.StallProxy).StallListen)
}

// expectStallRetried starts the listener behind a proxy that stalls (stall
// on), waits until a connect attempt is stuck in it, lifts the stall, and
// expects the listener to retry and receive.
func expectStallRetried(t *testing.T, stall func(*testutil.StallProxy, bool)) {
	t.Helper()
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	cfg := listenerConfig(pool)
	proxy := testutil.NewStallProxy(t, fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	cfg.Host, cfg.Port = "127.0.0.1", proxy.Port()
	stall(proxy, true)
	l, h := newListener()
	pglisten.SetConnectTimeoutForTest(l, 300*time.Millisecond)
	testutil.RunListener(t, l, cfg)
	testutil.WaitFor(t, func() bool { return proxy.Accepted() >= 1 }, "a connect attempt reaching the stalled proxy")
	testutil.ReceiveNone(t, h.ups, "up through a stalled connection")

	stall(proxy, false)
	testutil.Receive(t, h.ups, "up once the database answers")
	if n := proxy.Accepted(); n < 2 {
		t.Errorf("the proxy saw %d connections, want a retry after the stalled one", n)
	}
	notify(t, pool, "pglisten_a", "after")
	if got := testutil.Receive(t, h.notices, "a notice once the database answers"); got != "after" {
		t.Fatalf("got %q", got)
	}
}

// The path a quiet server process takes every ping period: the wait runs out,
// the ping is answered, and the same connection keeps receiving.
func TestIntegrationListener_AQuietHealthyConnectionIsKept(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	l, h := newListener()
	pglisten.SetPingForTest(l, 100*time.Millisecond, time.Second)
	testutil.RunListener(t, l, listenerConfig(pool))
	testutil.Receive(t, h.ups, "connection up")

	time.Sleep(time.Second)
	testutil.ReceiveNone(t, h.downs, "the connection given up after its pings")
	notify(t, pool, "pglisten_a", "after pings")
	if got := testutil.Receive(t, h.notices, "a notice after several pings"); got != "after pings" {
		t.Fatalf("got %q", got)
	}
}

// The handler set is fixed once Run starts: it walks the map without a lock.
func TestListener_HandleAfterRunPanics(t *testing.T) {
	l := pglisten.New()
	l.Handle("pglisten_a", newRecorder())
	cfg, err := pgx.ParseConfig("host=127.0.0.1 port=1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l.Run(ctx, cfg)
	defer func() {
		if recover() == nil {
			t.Fatal("Handle after Run did not panic")
		}
	}()
	l.Handle("pglisten_b", newRecorder())
}
