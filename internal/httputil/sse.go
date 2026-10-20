package httputil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SSEKeepalive is how often a long-lived stream sends a comment so idle
// proxies keep it open: under every common proxy default (nginx and AWS ALB
// 60 s, Heroku 55 s).
const SSEKeepalive = 25 * time.Second

// SSE writes a server-sent event stream. Writes are not synchronized: a
// stream written from several goroutines needs its own lock.
type SSE struct {
	w http.ResponseWriter
	f http.Flusher
	// retry is sent with the next event; zero sends none.
	retry time.Duration
}

// StartSSE answers 200 with the event-stream headers. When the writer cannot
// flush (a buffering middleware would hold every event back) it answers 500
// and returns false.
func StartSSE(w http.ResponseWriter) (*SSE, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		ServerError(w, "stream", "response")
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	// nginx: pass each event through instead of buffering the response.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &SSE{w: w, f: f}, true
}

// Event writes one event with a JSON payload and flushes it. An error means
// the client is gone.
func (s *SSE) Event(name string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal SSE payload: %w", err)
	}
	if s.retry > 0 {
		if _, err := fmt.Fprintf(s.w, "retry: %d\n", s.retry.Milliseconds()); err != nil {
			return err
		}
		s.retry = 0
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, body); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

// Retry sets how long EventSource waits before it reconnects once the stream
// ends. It goes out with the next Event.
func (s *SSE) Retry(d time.Duration) {
	s.retry = d
}

// Comment writes a comment line, which EventSource ignores, and flushes it.
func (s *SSE) Comment(text string) error {
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

// MaxStreamsPerUser is how many streams of one kind one user may hold open on
// one process. It sits well above one stream per browser tab, and it bounds
// the goroutines and subscriptions a client that keeps opening streams can
// pile up.
const MaxStreamsPerUser = 50

// StreamCap counts the open streams of one kind per user. Each stream handler
// keeps its own.
type StreamCap struct {
	mu   sync.Mutex
	open map[int64]int
}

// NewStreamCap returns a StreamCap with no open streams.
func NewStreamCap() *StreamCap {
	return &StreamCap{open: map[int64]int{}}
}

// Open reserves a stream for userID and returns the function that gives it
// back, to defer for the life of the stream. When the user already holds
// MaxStreamsPerUser it answers 429 and returns false.
func (c *StreamCap) Open(w http.ResponseWriter, userID int64) (release func(), ok bool) {
	c.mu.Lock()
	full := c.open[userID] >= MaxStreamsPerUser
	if !full {
		c.open[userID]++
	}
	c.mu.Unlock()
	if full {
		TooManyRequests(w, 30*time.Second, "too many open event streams")
		return nil, false
	}
	var once sync.Once
	return func() { once.Do(func() { c.close(userID) }) }, true
}

func (c *StreamCap) close(userID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open[userID]--; c.open[userID] <= 0 {
		delete(c.open, userID)
	}
}
