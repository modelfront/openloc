package llmdebug

import (
	"context"
	"sync"
	"time"
)

// Ring is a fixed-capacity, mutex-guarded buffer of Entries with
// monotonically increasing, never-reused sequence numbers — a polling
// client's `after` cursor stays valid across eviction. Safe for concurrent
// use (llmConcurrency=5 goroutines record in parallel).
type Ring struct {
	mu       sync.Mutex
	buf      []Entry
	capacity int
	nextSeq  uint64 // seq assigned to the next entry; first entry gets 1
}

// NewRing returns a Ring holding at most capacity entries.
func NewRing(capacity int) *Ring {
	return &Ring{capacity: capacity, nextSeq: 1}
}

// Record normalizes the entry (see normalize), stamps a fresh Seq, and appends
// — evicting the oldest entry when full. Implements Recorder.
func (r *Ring) Record(ctx context.Context, e Entry) {
	e = normalize(ctx, e)

	r.mu.Lock()
	defer r.mu.Unlock()
	// Time and Seq together, under one lock: the viewer orders by Seq and
	// renders Time, so assigning them separately lets the two disagree under
	// concurrency.
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Seq = r.nextSeq
	r.nextSeq++
	r.buf = append(r.buf, e)
	if len(r.buf) > r.capacity {
		r.buf = r.buf[len(r.buf)-r.capacity:]
	}
}

// Since returns a copy of all entries with Seq > after, ascending, plus the
// latest assigned seq (== after when nothing new). Callers never see the
// internal slice.
func (r *Ring) Since(after uint64) (entries []Entry, lastSeq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	lastSeq = r.nextSeq - 1
	// Entries are stored in ascending seq order; find the first one > after.
	start := len(r.buf)
	for i, e := range r.buf {
		if e.Seq > after {
			start = i
			break
		}
	}
	entries = make([]Entry, len(r.buf)-start)
	copy(entries, r.buf[start:])
	return entries, lastSeq
}
