package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// noFlush hides httptest.ResponseRecorder's Flush, like a buffering middleware.
type noFlush struct{ http.ResponseWriter }

func TestStartSSE_WritesEventsAndComments(t *testing.T) {
	rec := httptest.NewRecorder()
	sse, ok := StartSSE(rec)
	if !ok {
		t.Fatal("StartSSE refused a flushing writer")
	}
	// StartSSE flushed the header; each write must flush again.
	rec.Flushed = false
	if err := sse.Event("holds", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if !rec.Flushed {
		t.Error("the event was not flushed")
	}
	rec.Flushed = false
	if err := sse.Comment("keepalive"); err != nil {
		t.Fatal(err)
	}
	if !rec.Flushed {
		t.Error("the comment was not flushed")
	}
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("answered %d %q, want 200 text/event-stream", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got, want := rec.Body.String(), "event: holds\ndata: {\"n\":1}\n\n: keepalive\n\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestStartSSE_RefusesAWriterThatCannotFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	if _, ok := StartSSE(noFlush{rec}); ok {
		t.Fatal("StartSSE accepted a writer that cannot flush")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSSE_RetryGoesOutWithTheNextEventOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	sse, ok := StartSSE(rec)
	if !ok {
		t.Fatal("StartSSE refused a flushing writer")
	}
	sse.Retry(2500 * time.Millisecond)
	if err := sse.Event("connected", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := sse.Event("holds", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if got, want := rec.Body.String(), "retry: 2500\nevent: connected\ndata: {}\n\nevent: holds\ndata: {}\n\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// One user's streams stop at MaxStreamsPerUser with a 429. Another user's are
// counted apart, and a closed stream frees its place exactly once.
func TestStreamCap_RefusesAUsersStreamsPastTheCap(t *testing.T) {
	c := NewStreamCap()
	releases := make([]func(), 0, MaxStreamsPerUser)
	for i := range MaxStreamsPerUser {
		release, ok := c.Open(httptest.NewRecorder(), 1)
		if !ok {
			t.Fatalf("stream %d refused under the cap", i+1)
		}
		releases = append(releases, release)
	}
	rec := httptest.NewRecorder()
	if _, ok := c.Open(rec, 1); ok || rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("stream past the cap: ok %v, status %d, Retry-After %q; want refused with 429 and a Retry-After",
			ok, rec.Code, rec.Header().Get("Retry-After"))
	}
	if _, ok := c.Open(httptest.NewRecorder(), 2); !ok {
		t.Fatal("another user's stream was refused")
	}

	releases[0]()
	releases[0]()
	if _, ok := c.Open(httptest.NewRecorder(), 1); !ok {
		t.Fatal("a closed stream did not free its place")
	}
	if _, ok := c.Open(httptest.NewRecorder(), 1); ok {
		t.Fatal("one stream given back twice freed two places")
	}
}
