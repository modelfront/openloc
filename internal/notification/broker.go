// Package notification, broker.go — pub/sub abstraction for real-time
// notification delivery.
//
// Broker is an interface: PGBroker reaches the subscribers of every server
// process, InProcessBroker those of its own process only.
//
// Subscriber lifecycle:
//   - Caller (SSE handler) calls Subscribe(ctx, userID) and ranges over
//     the returned channel.
//   - When ctx is cancelled (client disconnect), the broker drops the
//     subscriber and closes the channel. A broker may also end a
//     subscription on its own when it cannot promise live delivery any more.
//   - Slow subscribers are dropped (publish is non-blocking) so one stuck
//     EventSource can't back-pressure the whole notification system.
//
// Publish lifecycle:
//   - Service.Send calls Publish(ctx, recipientUserID, event) AFTER the
//     in-app row + email step succeed (so subscribers don't see events
//     for notifications that aren't in the DB yet — keeps history queries
//     and the live stream consistent).
//   - Publish is non-blocking by design: each subscriber chan has a small
//     buffer, and a full chan = drop the event for that subscriber. The
//     subscriber will catch up via the next page-load REST query
//     (`GET /api/notifications`). SSE is "best-effort live", not "exactly
//     once". The DB row is the source of truth.

package notification

import (
	"context"
	"sync"

	"openloc/internal/errtrack"
)

// Broker delivers notification Events from publishers (Service.Send) to
// subscribers (SSE connections, in-process tests). Implementations must
// be safe for concurrent use across many goroutines.
type Broker interface {
	// Subscribe returns a channel that receives Events for the given user,
	// from every org the user belongs to; the subscriber scopes them to its
	// own org. The channel is closed when ctx is cancelled, or earlier when
	// the broker can no longer promise that events reach it; the subscriber
	// then reads the DB to catch up. Multiple concurrent subscribers for the
	// same user are allowed (one per browser tab).
	Subscribe(ctx context.Context, userID int64) <-chan Event

	// Publish delivers the event to every active subscriber for the
	// recipient (event.RecipientID). MUST be non-blocking — slow
	// subscribers are dropped, never throttle the publisher.
	// Returns an error only on hard failures (e.g. broker shut down);
	// "no subscribers" is NOT an error (caller may publish to users who
	// happen to have no tabs open, and the notification still lands in
	// the DB for next page-load).
	Publish(ctx context.Context, event Event) error
}

// subscriberChanBuffer caps each subscriber's channel buffer. A subscriber
// that doesn't drain within the buffer's worth of events is "slow" and we
// drop new events on the floor.
//
// 64 covers the realistic worst-case bulk op: a PM bulk-assigning 50 jobs to
// one translator fires 50 events. Dropped events are not lost (the DB row is
// read on the next REST poll). Memory cost: 64 events × ~200 bytes = ~13KB
// per subscriber.
const subscriberChanBuffer = 64

// InProcessBroker delivers to the subscribers of its own process: a map of
// userID → set of active subscriber channels. PGBroker fans out through one;
// on its own it serves tests and a single process.
type InProcessBroker struct {
	mu          sync.RWMutex
	subscribers map[int64]map[chan Event]struct{}
}

func NewInProcessBroker() *InProcessBroker {
	return &InProcessBroker{
		subscribers: make(map[int64]map[chan Event]struct{}),
	}
}

func (b *InProcessBroker) Subscribe(ctx context.Context, userID int64) <-chan Event {
	ch := make(chan Event, subscriberChanBuffer)

	b.mu.Lock()
	if b.subscribers[userID] == nil {
		b.subscribers[userID] = make(map[chan Event]struct{})
	}
	b.subscribers[userID][ch] = struct{}{}
	b.mu.Unlock()

	// Cleanup goroutine: when ctx is cancelled (client disconnect),
	// remove the subscriber from the map and close the channel so the
	// caller's range-over-chan terminates.
	go func() {
		defer errtrack.Monitor(ctx, "notification", "broker-unsubscribe")
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		// endAll may have closed and removed it already.
		if _, ok := b.subscribers[userID][ch]; !ok {
			return
		}
		delete(b.subscribers[userID], ch)
		if len(b.subscribers[userID]) == 0 {
			delete(b.subscribers, userID)
		}
		close(ch)
	}()

	return ch
}

func (b *InProcessBroker) Publish(_ context.Context, event Event) error {
	b.deliver(event)
	return nil
}

// deliver hands the event to every subscriber of its recipient.
func (b *InProcessBroker) deliver(event Event) {
	// Hold the lock across the sends: releasing it lets the cleanup
	// goroutine close a subscriber's channel mid-send, which panics with
	// "send on closed channel".
	//
	// The lock is held only for microseconds — non-blocking channel
	// sends + a map lookup. Even at 1k publishes/sec across all users,
	// the broker won't be a contention bottleneck. If profiling later
	// shows it is, the proper fix is per-subscriber synchronization
	// (closed-flag + sync.Once on close), not lock release.
	b.mu.Lock()
	defer b.mu.Unlock()

	for ch := range b.subscribers[event.RecipientID] {
		// Non-blocking send. If the subscriber's buffer is full, we drop
		// this event for that subscriber. The DB row is the source of
		// truth — they'll see it on next REST poll / page load.
		select {
		case ch <- event:
		default:
			// dropped — acceptable per the broker contract
		}
	}
}

// endAll closes and removes every subscriber's channel.
func (b *InProcessBroker) endAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, set := range b.subscribers {
		for ch := range set {
			close(ch)
		}
	}
	b.subscribers = make(map[int64]map[chan Event]struct{})
}

// SubscriberCount returns the total number of active subscriber channels
// across all users. Test-only probe; not part of the Broker interface.
func (b *InProcessBroker) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := 0
	for _, set := range b.subscribers {
		n += len(set)
	}
	return n
}
