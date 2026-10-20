package notification

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// fakeNotifier records the payloads it is asked to send. While gate is open
// (non-nil and not closed) each send waits on it or its ctx.
type fakeNotifier struct {
	mu       sync.Mutex
	payloads []string
	gate     chan struct{}
	// failFirst, when set, is what the first send returns, sending nothing.
	failFirst error
}

func (f *fakeNotifier) Exec(ctx context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return pgconn.CommandTag{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failFirst; err != nil {
		f.failFirst = nil
		return pgconn.CommandTag{}, err
	}
	f.payloads = append(f.payloads, args[1].(string))
	return pgconn.CommandTag{}, nil
}

func (f *fakeNotifier) sent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.payloads)
}

func closeNow(t *testing.T, b *PGBroker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b.Close(ctx)
}

// A database that does not answer never holds up the publisher: Publish
// queues, and past the queue it drops.
func TestPGBroker_PublishNeverWaitsForTheDatabase(t *testing.T) {
	stuck := &fakeNotifier{gate: make(chan struct{})}
	b := NewPGBroker(stuck)
	defer closeNow(t, b)

	start := time.Now()
	var err error
	for i := 0; i <= noticeQueue+1 && err == nil; i++ {
		err = b.Publish(context.Background(), Event{Type: EventDeadline, TenantID: 1, RecipientID: 2})
	}
	if !errors.Is(err, errQueueFull) {
		t.Fatalf("Publish past the queue = %v, want errQueueFull", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("publishing took %s against a stuck database", took)
	}
}

// Close waits until what is already queued is sent; after it, Publish fails
// instead of panicking on the closed queue. The database answers only once
// Close has begun, so a Close that did not wait, or stopped the sending
// first, sends fewer than three.
func TestPGBroker_CloseSendsTheQueueThenRefuses(t *testing.T) {
	db := &fakeNotifier{gate: make(chan struct{})}
	b := NewPGBroker(db)
	for i := 0; i < 3; i++ {
		if err := b.Publish(context.Background(), Event{Type: EventDeadline, TenantID: 1, RecipientID: 2}); err != nil {
			t.Fatal(err)
		}
	}
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b.Close(ctx)
	}()
	deadline := time.Now().Add(time.Second)
	for !b.isClosed() {
		if time.Now().After(deadline) {
			t.Fatal("Close never began")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-closed:
		t.Fatal("Close returned before the queued notices were sent")
	case <-time.After(50 * time.Millisecond):
	}

	close(db.gate)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return once the database answered")
	}
	if got := db.sent(); got != 3 {
		t.Fatalf("sent %d of 3 queued notices", got)
	}
	if err := b.Publish(context.Background(), Event{Type: EventDeadline, TenantID: 1, RecipientID: 2}); !errors.Is(err, errBrokerClosed) {
		t.Fatalf("Publish after Close = %v, want errBrokerClosed", err)
	}
}

// A notice the database refuses is dropped; the sender goes on with the next.
func TestPGBroker_AFailedSendDoesNotStopTheSender(t *testing.T) {
	db := &fakeNotifier{failFirst: errors.New("connection reset")}
	b := NewPGBroker(db)
	if err := b.Publish(context.Background(), Event{Type: EventDeadline, TenantID: 1, RecipientID: 2, JobID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(context.Background(), Event{Type: EventDeadline, TenantID: 1, RecipientID: 2, JobID: 2}); err != nil {
		t.Fatal(err)
	}
	closeNow(t, b)
	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.payloads) != 1 || !strings.Contains(db.payloads[0], `"job_id":2`) {
		t.Fatalf("sent %q, want the second notice after the first failed", db.payloads)
	}
}

// Close gives up on a database that does not answer once its ctx ends.
func TestPGBroker_CloseStopsAtItsDeadline(t *testing.T) {
	stuck := &fakeNotifier{gate: make(chan struct{})}
	b := NewPGBroker(stuck)
	for i := 0; i < 3; i++ {
		if err := b.Publish(context.Background(), Event{Type: EventDeadline, TenantID: 1, RecipientID: 2}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	b.Close(ctx)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("Close took %s past a 100ms deadline", took)
	}
	if got := stuck.sent(); got != 0 {
		t.Fatalf("sent %d notices to a database that never answered", got)
	}
}

// A notice the listener hands over reaches the recipient's subscribers; one
// without a tenant, user or type is dropped.
func TestPGBroker_ReceiverDeliversWellFormedNotices(t *testing.T) {
	b := NewPGBroker(&fakeNotifier{})
	defer closeNow(t, b)
	r := receiver{b}
	r.Up()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := b.Subscribe(ctx, 42)
	// A notice without a user would go to user 0.
	nobody := b.Subscribe(ctx, 0)

	for _, bad := range []string{
		`not json`,
		`{"user_id":42,"type":"assignment","job_id":1}`,
		`{"tenant_id":1,"type":"assignment","job_id":2}`,
		`{"tenant_id":1,"user_id":42,"job_id":3}`,
	} {
		r.Notify(ctx, bad)
	}
	r.Notify(ctx, `{"tenant_id":1,"user_id":42,"type":"assignment","job_id":4,"project_id":5}`)

	select {
	case ev := <-events:
		if ev.JobID != 4 || ev.ProjectID != 5 || ev.TenantID != 1 || ev.Type != EventAssignment {
			t.Fatalf("first event %+v, want the well-formed notice (job 4)", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("well-formed notice not delivered")
	}
	// Notify delivers before it returns, so anything for user 0 is there now.
	select {
	case ev := <-nobody:
		t.Fatalf("a notice without a user was delivered to user 0: %+v", ev)
	default:
	}
}

// Both edges of the listen connection end the open subscriptions.
func TestPGBroker_ListenerUpAndDownEndSubscriptions(t *testing.T) {
	b := NewPGBroker(&fakeNotifier{})
	defer closeNow(t, b)
	r := receiver{b}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, edge := range []struct {
		name string
		call func()
	}{{"up", r.Up}, {"down", r.Down}} {
		events := b.Subscribe(ctx, 42)
		edge.call()
		select {
		case _, ok := <-events:
			if ok {
				t.Fatalf("%s: got an event, want the subscription ended", edge.name)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: subscription still open", edge.name)
		}
	}
	if n := b.local.SubscriberCount(); n != 0 {
		t.Fatalf("%d subscribers left", n)
	}
}
