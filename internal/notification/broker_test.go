// InProcessBroker pub/sub contract.
//
// What we pin:
//   1. A subscriber receives events targeted at its userID.
//   2. A subscriber does NOT receive events for OTHER users (delivery is
//      per-recipient, not broadcast).
//   3. ctx cancellation removes the subscriber + closes the channel
//      (so the SSE handler's range-over-chan terminates cleanly on
//      client disconnect).
//   4. Multiple subscribers for the same user (e.g. multiple browser tabs)
//      all receive the same event.
//   5. Publish is non-blocking even when one subscriber is stuck — slow
//      subscribers get dropped events, NEVER throttle the publisher.
//
// Anti-tautology: removing the per-recipient routing in Publish (e.g.
// fan-out to ALL channels regardless of userID) makes test #2 fail RED.

package notification

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestInProcessBroker_Subscriber_ReceivesOwnEvents(t *testing.T) {
	b := NewInProcessBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := b.Subscribe(ctx, 42)
	if err := b.Publish(ctx, Event{Type: EventDeadline, RecipientID: 42, JobID: 100}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-ch:
		if got.JobID != 100 {
			t.Errorf("got JobID=%d, want 100", got.JobID)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive its event within 1s")
	}
}

func TestInProcessBroker_Subscriber_DoesNotReceiveOtherUsersEvents(t *testing.T) {
	b := NewInProcessBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := b.Subscribe(ctx, 42)
	// Publish for someone else.
	if err := b.Publish(ctx, Event{Type: EventDeadline, RecipientID: 99, JobID: 100}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// 200ms grace period — long enough that a fan-out-to-all bug would
	// reach us, short enough that the test isn't slow.
	select {
	case got := <-ch:
		t.Errorf("subscriber for user 42 should NOT have received event for user 99, got %+v", got)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}

func TestInProcessBroker_CtxCancel_ClosesChannelAndDropsSubscriber(t *testing.T) {
	b := NewInProcessBroker()
	ctx, cancel := context.WithCancel(context.Background())

	ch := b.Subscribe(ctx, 42)
	if got := b.SubscriberCount(); got != 1 {
		t.Fatalf("after Subscribe: count = %d, want 1", got)
	}

	cancel()
	// Cleanup goroutine runs async; give it a moment.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if b.SubscriberCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.SubscriberCount(); got != 0 {
		t.Errorf("after cancel: count = %d, want 0 (cleanup didn't drop subscriber)", got)
	}

	// The channel must be closed (so range-over-chan in the SSE handler
	// terminates instead of blocking forever).
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel should be closed after ctx cancel, got value still arriving")
		}
	case <-time.After(time.Second):
		t.Error("channel was not closed within 1s of cancel")
	}
}

func TestInProcessBroker_MultipleSubscribers_SameUser(t *testing.T) {
	b := NewInProcessBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Two tabs / two SSE connections for the same user.
	ch1 := b.Subscribe(ctx, 42)
	ch2 := b.Subscribe(ctx, 42)

	if err := b.Publish(ctx, Event{Type: EventDeadline, RecipientID: 42, JobID: 7}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case got := <-ch:
			if got.JobID != 7 {
				t.Errorf("subscriber %d got JobID=%d, want 7", i, got.JobID)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d did not receive event within 1s", i)
		}
	}
}

func TestInProcessBroker_Publish_IsNonBlockingWhenSubscriberStuck(t *testing.T) {
	b := NewInProcessBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe but never drain. Fill the buffer + N more.
	_ = b.Subscribe(ctx, 42)

	// Send way more than the buffer capacity. If Publish were blocking
	// on the full chan we'd deadlock; the test would hang.
	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberChanBuffer*5; i++ {
			_ = b.Publish(ctx, Event{Type: EventDeadline, RecipientID: 42, JobID: int64(i)})
		}
		close(done)
	}()

	select {
	case <-done:
		// expected — publish completed despite full buffer
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked when subscriber buffer was full — back-pressure leaked from subscriber to publisher")
	}
}

func TestInProcessBroker_ConcurrentPubSub_DropsAllSubscribers(t *testing.T) {
	// Concurrent Subscribe/Publish/cancel: every cancelled subscriber leaves
	// the map, with no panic from a send on a closed channel.
	b := NewInProcessBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(uid int64) {
			defer wg.Done()
			subCtx, subCancel := context.WithCancel(ctx)
			defer subCancel()
			_ = b.Subscribe(subCtx, uid)
		}(int64(i))
		go func(uid int64) {
			defer wg.Done()
			_ = b.Publish(ctx, Event{Type: EventDeadline, RecipientID: uid})
		}(int64(i))
	}
	wg.Wait()

	// Cleanup goroutines run async after each subCancel.
	deadline := time.Now().Add(time.Second)
	for b.SubscriberCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.SubscriberCount(); got != 0 {
		t.Errorf("after every subscriber cancelled: count = %d, want 0", got)
	}
}
