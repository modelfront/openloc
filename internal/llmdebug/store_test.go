package llmdebug

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStore records every Put so tests can assert on keys and bodies. Mirrors
// the narrow objectStore seam, which is the whole reason the Store doesn't
// import internal/file.
type fakeStore struct {
	mu   sync.Mutex
	puts map[string]string
	err  error
	// block, when non-nil, holds every Put until closed — used to simulate a
	// storage stall.
	block chan struct{}
}

func newFakeStore() *fakeStore { return &fakeStore{puts: map[string]string{}} }

func (f *fakeStore) Put(_ context.Context, key string, body io.Reader) error {
	if f.block != nil {
		<-f.block
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.puts[key] = string(b)
	return nil
}

func (f *fakeStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.puts)
}

func (f *fakeStore) all() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.puts))
	for k, v := range f.puts {
		out[k] = v
	}
	return out
}

// fixedNow keeps key assertions deterministic.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func testStore(t *testing.T, fs *fakeStore, opts StoreOpts) *Store {
	t.Helper()
	s := pausedStore(t, fs, opts)
	go s.run()
	return s
}

// pausedStore is testStore without the flusher running: the caller loads the
// buffer with Record and then starts run() itself. Use it when the assertion
// is about how a burst is batched — with the flusher live, it drains the
// queue the moment it goes quiet, so entries recorded a few microseconds apart
// may or may not share an object.
func pausedStore(t *testing.T, fs *fakeStore, opts StoreOpts) *Store {
	t.Helper()
	if opts.Now == nil {
		opts.Now = fixedNow(time.Date(2026, 8, 21, 13, 45, 6, 0, time.UTC))
	}
	if opts.Host == "" {
		opts.Host = "pod-a"
	}
	s := newStore(fs, opts)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Close(ctx)
	})
	return s
}

// waitFor polls every 5ms until cond or a generous deadline, because the flush
// runs on another goroutine and a loaded CI machine is slow.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func decodeNDJSON(t *testing.T, body string) []Entry {
	t.Helper()
	var out []Entry
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func TestStore_FlushesByBatchSizeAsNDJSON(t *testing.T) {
	fs := newFakeStore()
	// The burst is queued before the flusher starts, so every entry is waiting
	// when it drains — the only way the split below is a property of the
	// batching and not of goroutine scheduling.
	//
	// FOUR entries at BatchSize 3, not three: three would coalesce into one
	// object whatever the size is (the drain writes what it has when the queue
	// goes quiet), so the test would pass with the size check deleted. The
	// 3-then-1 split is what only a size-triggered flush produces.
	s := pausedStore(t, fs, StoreOpts{BatchSize: 3, FlushInterval: time.Hour})

	for i, kind := range []string{KindMT, KindQE, KindAPE, KindMT} {
		s.Record(context.Background(), Entry{Kind: kind, Provider: "claude", Status: 200, DurationMs: int64(i)})
	}
	go s.run()

	waitFor(t, "two flushed objects", func() bool { return fs.count() == 2 })
	sizes := map[int]string{}
	for _, body := range fs.all() {
		sizes[len(decodeNDJSON(t, body))] = body
	}
	full, ok := sizes[3]
	if !ok {
		t.Fatalf("no object holds a full batch of 3; got sizes %v", keysOf(sizes))
	}
	if _, ok := sizes[1]; !ok {
		t.Fatalf("no object holds the 1-entry remainder; got sizes %v", keysOf(sizes))
	}
	entries := decodeNDJSON(t, full)
	if entries[0].Kind != KindMT || entries[2].Kind != KindAPE {
		t.Errorf("order not preserved: %s … %s", entries[0].Kind, entries[2].Kind)
	}
}

func keysOf(m map[int]string) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestStore_FlushesByTimerWhenBatchNeverFills(t *testing.T) {
	fs := newFakeStore()
	s := testStore(t, fs, StoreOpts{BatchSize: 100, FlushInterval: 20 * time.Millisecond})

	s.Record(context.Background(), Entry{Kind: KindMT})

	// A single entry must not sit in the buffer forever waiting for 99 friends.
	waitFor(t, "timer flush", func() bool { return fs.count() == 1 })
}

func TestStore_NeverWritesAnEmptyObject(t *testing.T) {
	fs := newFakeStore()
	testStore(t, fs, StoreOpts{BatchSize: 10, FlushInterval: 10 * time.Millisecond})

	// Idle service: several tick intervals pass with nothing recorded.
	time.Sleep(60 * time.Millisecond)
	if got := fs.count(); got != 0 {
		t.Fatalf("idle store wrote %d objects, want 0", got)
	}
}

// The load-bearing property: a stalled or dead object store must never add latency to
// a translation job. Record drops on a full buffer instead of blocking.
//
// Anti-tautology: remove the `default:` branch from Record's select and this
// test hangs until its own deadline instead of passing.
func TestStore_RecordNeverBlocksWhenBufferIsFull(t *testing.T) {
	fs := newFakeStore()
	fs.block = make(chan struct{})
	defer close(fs.block)

	// Buffer of 2, batch of 1: the first record is picked up by the flusher and
	// wedges in the blocked Put, so everything after it queues then overflows.
	s := testStore(t, fs, StoreOpts{BatchSize: 1, FlushInterval: time.Hour, BufferSize: 2})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			s.Record(context.Background(), Entry{Kind: KindMT, RequestBody: "x"})
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Record blocked while the store was stalled — a storage outage would stall the pipeline")
	}

	if dropped := s.Stats().Dropped; dropped == 0 {
		t.Error("expected drops once the buffer overflowed, got 0 — is the buffer actually bounded?")
	}
}

func TestStore_PutErrorIsCountedAndDoesNotWedgeTheFlusher(t *testing.T) {
	fs := newFakeStore()
	fs.err = errors.New("object store is having a day")
	s := testStore(t, fs, StoreOpts{BatchSize: 1, FlushInterval: time.Hour})

	s.Record(context.Background(), Entry{Kind: KindMT})
	waitFor(t, "first failed put", func() bool { return s.Stats().Failed == 1 })

	// The flusher must still be alive for the next batch.
	s.Record(context.Background(), Entry{Kind: KindQE})
	waitFor(t, "second failed put", func() bool { return s.Stats().Failed == 2 })
}

func TestStore_CloseFlushesTheRemainder(t *testing.T) {
	fs := newFakeStore()
	s := NewStore(fs, StoreOpts{
		BatchSize: 100, FlushInterval: time.Hour,
		Host: "pod-a", Now: fixedNow(time.Date(2026, 8, 21, 1, 2, 3, 0, time.UTC)),
	})
	s.Record(context.Background(), Entry{Kind: KindMT})
	s.Record(context.Background(), Entry{Kind: KindQE})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A deploy must not DISCARD what's buffered — that is the contract, and the
	// only thing worth asserting here.
	//
	// Deliberately NOT asserting the object count: run() flushes whenever the
	// queue goes quiet, so a flusher wake-up between these two Record calls
	// legally splits them into two objects.
	total := 0
	for _, body := range fs.all() {
		total += len(decodeNDJSON(t, body))
	}
	if total != 2 {
		t.Fatalf("Close persisted %d entries across %d objects, want 2 entries", total, fs.count())
	}
}

func TestStore_CloseIsIdempotent(t *testing.T) {
	fs := newFakeStore()
	s := NewStore(fs, StoreOpts{BatchSize: 1, Host: "pod-a"})
	ctx := context.Background()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("second Close must be a no-op, got %v", err)
	}
	// Recording after Close must not panic on a closed channel.
	s.Record(ctx, Entry{Kind: KindMT})
}

// Both prod pods write to ONE bucket, and S3 PutObject replaces whole objects,
// so the per-pod key component is what stops them clobbering each other.
func TestStore_KeyIsDatePrefixedAndPodScoped(t *testing.T) {
	when := time.Date(2026, 8, 21, 13, 45, 6, 0, time.UTC)
	fsA, fsB := newFakeStore(), newFakeStore()
	a := testStore(t, fsA, StoreOpts{BatchSize: 1, FlushInterval: time.Hour, Host: "pod-a", Now: fixedNow(when)})
	b := testStore(t, fsB, StoreOpts{BatchSize: 1, FlushInterval: time.Hour, Host: "pod-b", Now: fixedNow(when)})

	a.Record(context.Background(), Entry{Kind: KindMT})
	b.Record(context.Background(), Entry{Kind: KindMT})
	waitFor(t, "both puts", func() bool { return fsA.count() == 1 && fsB.count() == 1 })

	var keyA, keyB string
	for k := range fsA.all() {
		keyA = k
	}
	for k := range fsB.all() {
		keyB = k
	}
	for _, k := range []string{keyA, keyB} {
		if !strings.HasPrefix(k, "llm-debug/2026-08-21/") {
			t.Errorf("key %q must live under the UTC date prefix (the prune works by prefix)", k)
		}
		if !strings.HasSuffix(k, ".ndjson") {
			t.Errorf("key %q should end .ndjson", k)
		}
	}
	if keyA == keyB {
		t.Fatalf("both pods produced key %q — one would overwrite the other", keyA)
	}
	if !strings.Contains(keyA, "pod-a") || !strings.Contains(keyB, "pod-b") {
		t.Errorf("keys must identify the writing pod: %q / %q", keyA, keyB)
	}
}

// Two batches from the SAME pod in the same millisecond must not collide either.
func TestStore_ConsecutiveBatchesGetDistinctKeys(t *testing.T) {
	fs := newFakeStore()
	s := testStore(t, fs, StoreOpts{
		BatchSize: 1, FlushInterval: time.Hour,
		Now: fixedNow(time.Date(2026, 8, 21, 13, 45, 6, 0, time.UTC)),
	})
	for i := 0; i < 5; i++ {
		s.Record(context.Background(), Entry{Kind: KindMT})
	}
	waitFor(t, "five distinct objects", func() bool { return fs.count() == 5 })
}

// Store shares Ring's normalization: ctx Meta lands on the entry and oversized
// bodies are truncated before they're persisted.
func TestStore_NormalizesLikeTheRing(t *testing.T) {
	fs := newFakeStore()
	s := testStore(t, fs, StoreOpts{BatchSize: 1, FlushInterval: time.Hour})

	ctx := WithMeta(context.Background(), Meta{JobID: 42, FileID: 7, Project: "Demo", Step: "Review", Engine: "claude"})
	s.Record(ctx, Entry{Kind: KindQE, RequestBody: strings.Repeat("x", maxBodyCapture+500)})

	waitFor(t, "put", func() bool { return fs.count() == 1 })
	for _, body := range fs.all() {
		e := decodeNDJSON(t, body)[0]
		if e.JobID != 42 || e.FileID != 7 || e.Project != "Demo" || e.Step != "Review" || e.Engine != "claude" {
			t.Errorf("ctx Meta not applied: %+v", e)
		}
		if e.Time.IsZero() {
			t.Error("Time must be stamped")
		}
		if !strings.HasSuffix(e.RequestBody, "…[truncated]") {
			t.Error("oversized body should be truncated, marked as such")
		}
	}
}

func TestMulti(t *testing.T) {
	t.Run("nil when nothing live", func(t *testing.T) {
		if got := Multi(); got != nil {
			t.Errorf("Multi() = %v, want nil", got)
		}
		// A typed nil must not become a non-nil interface — that would defeat
		// the recorder==nil fast path at every capture site.
		var nilRing *Ring
		if got := Multi(nilRing); got != nil {
			t.Errorf("Multi(typed nil) = %v, want nil", got)
		}
	})

	t.Run("single recorder is not wrapped", func(t *testing.T) {
		ring := NewRing(4)
		if got := Multi(ring); got != Recorder(ring) {
			t.Errorf("Multi(one) should return it unwrapped, got %T", got)
		}
	})

	t.Run("fans out to every recorder", func(t *testing.T) {
		ring := NewRing(4)
		fs := newFakeStore()
		store := testStore(t, fs, StoreOpts{BatchSize: 1, FlushInterval: time.Hour})

		Multi(ring, store).Record(context.Background(), Entry{Kind: KindMT})

		if entries, _ := ring.Since(0); len(entries) != 1 {
			t.Errorf("ring holds %d entries, want 1 — the viewer would look empty", len(entries))
		}
		waitFor(t, "store put", func() bool { return fs.count() == 1 })
	})
}

// Pins that an entry reaches storage once the hand-off queue goes quiet,
// without waiting for FlushInterval or Close, so a process that ends (rolling
// deploy, SIGKILL, eviction) does not lose the buffer.
// FlushInterval is an hour and nothing calls Close, so only quiescence can
// trigger the write.
func TestStore_FlushesOnceTheQueueGoesQuiet(t *testing.T) {
	fs := newFakeStore()
	// An interval long enough that ONLY quiescence can trigger a write, and a
	// batch size far above what we record, so the count trigger can't either.
	s := testStore(t, fs, StoreOpts{BatchSize: 100, FlushInterval: time.Hour})

	s.Record(context.Background(), Entry{Kind: KindMT})
	s.Record(context.Background(), Entry{Kind: KindQE})
	s.Record(context.Background(), Entry{Kind: KindAPE})

	// The live flusher may split the burst across objects, so wait on the
	// entries persisted, not on the first object.
	persisted := func() int {
		var total int
		for _, body := range fs.all() {
			total += len(decodeNDJSON(t, body))
		}
		return total
	}
	waitFor(t, "all 3 entries written without waiting for the interval or Close", func() bool {
		return persisted() == 3
	})
}

// With replicas=2 an upload's MT and AI-step tasks can land on different pods,
// so "which pod wrote the half that survived" is the first question when half a
// job is missing. The ring leaves Host empty — it has no write side.
func TestStore_StampsTheWritingHost(t *testing.T) {
	fs := newFakeStore()
	s := testStore(t, fs, StoreOpts{BatchSize: 1, FlushInterval: time.Hour, Host: "pod-b"})
	s.Record(context.Background(), Entry{Kind: KindAPE})
	waitFor(t, "put", func() bool { return fs.count() == 1 })
	for _, body := range fs.all() {
		if got := decodeNDJSON(t, body)[0].Host; got != "pod-b" {
			t.Errorf("Host = %q, want the writing pod", got)
		}
	}

	ring := NewRing(2)
	ring.Record(context.Background(), Entry{Kind: KindMT})
	if got, _ := ring.Since(0); got[0].Host != "" {
		t.Errorf("ring Host = %q, want empty (no write side)", got[0].Host)
	}
}
