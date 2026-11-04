package chat

import (
	"context"
	"sync"
	"time"

	"openloc/internal/errtrack"
	"openloc/internal/httputil"
)

// keepaliveInterval is the shared stream keepalive. A var, not a const, so the
// heartbeat integration test can shrink it instead of sleeping 25 real
// seconds.
var keepaliveInterval = httputil.SSEKeepalive

// SetKeepaliveIntervalForTest shrinks the ping cadence so the heartbeat
// integration test doesn't sleep 25 real seconds. Returns a restore func.
// Test-only by contract; production callers have no reason to touch this.
func SetKeepaliveIntervalForTest(d time.Duration) (restore func()) {
	old := keepaliveInterval
	keepaliveInterval = d
	return func() { keepaliveInterval = old }
}

// startKeepalive writes a "ping" comment to the stream every keepaliveInterval,
// holding mu, the stream's write lock. stop ends the pings and waits until the
// pinger has exited. The handler must call it before it returns: a write after
// that lands on a response writer the compression middleware has already
// recycled, a nil dereference that takes the whole process down.
func startKeepalive(ctx context.Context, sse *httputil.SSE, mu *sync.Mutex) (stop func()) {
	quit := make(chan struct{})
	exited := make(chan struct{})
	ticker := time.NewTicker(keepaliveInterval)
	go func() {
		defer close(exited)
		defer errtrack.Monitor(ctx, "chat", "sse-keepalive")
		defer ticker.Stop()
		for {
			select {
			case <-quit:
				return
			case <-ticker.C:
				mu.Lock()
				err := sse.Comment("ping")
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()
	return func() {
		close(quit)
		<-exited
	}
}
