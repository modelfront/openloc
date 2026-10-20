package llmdebug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeReaderStore serves canned objects and counts downloads, so tests can
// assert that immutable objects are parsed once rather than on every poll.
type fakeReaderStore struct {
	mu        sync.Mutex
	objects   map[string]string
	listErr   error
	getErr    map[string]error
	downloads map[string]int
}

func newFakeReaderStore() *fakeReaderStore {
	return &fakeReaderStore{
		objects:   map[string]string{},
		getErr:    map[string]error{},
		downloads: map[string]int{},
	}
}

func (f *fakeReaderStore) put(key string, entries ...Entry) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			panic(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = buf.String()
}

func (f *fakeReaderStore) ListKeys(_ context.Context, prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeReaderStore) Download(_ context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.getErr[key]; err != nil {
		return nil, err
	}
	body, ok := f.objects[key]
	if !ok {
		return nil, fmt.Errorf("no such key %q", key)
	}
	f.downloads[key]++
	return io.NopCloser(strings.NewReader(body)), nil
}

func (f *fakeReaderStore) downloadCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloads[key]
}

func (f *fakeReaderStore) remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
}

func at(sec int) time.Time {
	return time.Date(2026, 8, 21, 10, 0, sec, 0, time.UTC)
}

func key(pod string, ms int) string {
	return fmt.Sprintf("llm-debug/2026-08-21/%s-%013d-1.ndjson", pod, ms)
}

func TestReader_MergesBothPodsInTimeOrder(t *testing.T) {
	fs := newFakeReaderStore()
	// Interleaved in time but written by different pods into different objects —
	// the prod shape. The feed has to present one ordered stream.
	fs.put(key("pod-a", 1), Entry{Kind: KindMT, Time: at(1)}, Entry{Kind: KindMT, Time: at(3)})
	fs.put(key("pod-b", 2), Entry{Kind: KindQE, Time: at(2)}, Entry{Kind: KindAPE, Time: at(4)})

	got, last := NewReader(fs, "").Since(0)
	if len(got) != 4 {
		t.Fatalf("got %d entries, want 4", len(got))
	}
	wantKinds := []string{KindMT, KindQE, KindMT, KindAPE} // by time: 1,2,3,4
	for i, want := range wantKinds {
		if got[i].Kind != want {
			t.Errorf("entry %d kind = %s, want %s (ordering is wrong)", i, got[i].Kind, want)
		}
	}
	if last != uint64(at(4).UnixMicro()) {
		t.Errorf("last cursor = %d, want the newest entry's time %d", last, at(4).UnixMicro())
	}
}

// The cursor must be derived from content, not from a per-process counter:
// replicas=2 with no sticky sessions means consecutive polls hit different pods.
func TestReader_CursorIsEntryTimeSoAnyReplicaAgrees(t *testing.T) {
	fs := newFakeReaderStore()
	fs.put(key("pod-a", 1), Entry{Kind: KindMT, Time: at(1)}, Entry{Kind: KindQE, Time: at(2)})

	// Two independent Readers = two pods serving alternating polls.
	podA, podB := NewReader(fs, ""), NewReader(fs, "")

	first, cursor := podA.Since(0)
	if len(first) != 2 {
		t.Fatalf("first poll returned %d, want 2", len(first))
	}

	// Same cursor handed to the OTHER pod: nothing new, no duplicates.
	second, cursor2 := podB.Since(cursor)
	if len(second) != 0 {
		t.Errorf("second pod re-served %d entries for the same cursor — the viewer would duplicate them", len(second))
	}
	if cursor2 != cursor {
		t.Errorf("cursor drifted between replicas: %d vs %d", cursor2, cursor)
	}

	// New object appears; either pod returns exactly the new entry.
	fs.put(key("pod-b", 5), Entry{Kind: KindAPE, Time: at(9)})
	third, _ := podB.Since(cursor)
	if len(third) != 1 || third[0].Kind != KindAPE {
		t.Errorf("after a new object, got %+v, want exactly the APE entry", third)
	}
}

func TestReader_PollsDoNotRedownloadImmutableObjects(t *testing.T) {
	fs := newFakeReaderStore()
	k := key("pod-a", 1)
	fs.put(k, Entry{Kind: KindMT, Time: at(1)})
	r := NewReader(fs, "")

	_, cursor := r.Since(0)
	for i := 0; i < 5; i++ {
		_, cursor = r.Since(cursor)
	}
	if n := fs.downloadCount(k); n != 1 {
		t.Errorf("object downloaded %d times across 6 polls, want 1 — the cache isn't working", n)
	}
}

// Pruned objects must leave the cache, which is also what bounds its growth.
func TestReader_ForgetsPrunedObjects(t *testing.T) {
	fs := newFakeReaderStore()
	oldKey, newKey := key("pod-a", 1), key("pod-a", 9)
	fs.put(oldKey, Entry{Kind: KindMT, Time: at(1)})
	fs.put(newKey, Entry{Kind: KindQE, Time: at(5)})
	r := NewReader(fs, "")

	if got, _ := r.Since(0); len(got) != 2 {
		t.Fatalf("want 2 entries before prune, got %d", len(got))
	}

	fs.remove(oldKey) // the daily prune ran
	got, _ := r.Since(0)
	if len(got) != 1 || got[0].Kind != KindQE {
		t.Errorf("after prune got %+v, want only the surviving entry", got)
	}
	r.mu.Lock()
	cached := len(r.cache)
	r.mu.Unlock()
	if cached != 1 {
		t.Errorf("cache holds %d objects after prune, want 1", cached)
	}
}

// A first poll against two days of retained objects must not return everything.
func TestReader_CapsOnePollAndReturnsTheNewest(t *testing.T) {
	fs := newFakeReaderStore()
	total := maxPerPoll + 50
	entries := make([]Entry, 0, total)
	for i := 0; i < total; i++ {
		entries = append(entries, Entry{Kind: KindMT, Time: at(0).Add(time.Duration(i) * time.Second)})
	}
	fs.put(key("pod-a", 1), entries...)

	got, last := NewReader(fs, "").Since(0)
	if len(got) != maxPerPoll {
		t.Fatalf("returned %d entries, want the %d cap", len(got), maxPerPoll)
	}
	// The NEWEST window, not the oldest — a live viewer wants recent activity.
	wantFirst := at(0).Add(time.Duration(total-maxPerPoll) * time.Second)
	if !got[0].Time.Equal(wantFirst) {
		t.Errorf("window starts at %v, want %v (oldest-first would show stale calls)", got[0].Time, wantFirst)
	}
	if last != uint64(entries[total-1].Time.UnixMicro()) {
		t.Error("cursor must advance to the newest entry even when the window is capped")
	}
}

// A storage failure leaves the viewer showing what it has rather than erroring.
func TestReader_ListErrorKeepsPreviouslyLoadedEntries(t *testing.T) {
	fs := newFakeReaderStore()
	fs.put(key("pod-a", 1), Entry{Kind: KindMT, Time: at(1)})
	r := NewReader(fs, "")
	if got, _ := r.Since(0); len(got) != 1 {
		t.Fatalf("setup: want 1 entry, got %d", len(got))
	}

	fs.mu.Lock()
	fs.listErr = errors.New("object store is down")
	fs.mu.Unlock()

	got, last := r.Since(0)
	if len(got) != 1 {
		t.Errorf("during an outage the feed should still serve cached entries, got %d", len(got))
	}
	if last == 0 {
		t.Error("cursor should still reflect what's cached")
	}
}

// One unreadable object must not blank the feed, and must not be retried on
// every poll.
func TestReader_SkipsUnreadableObject(t *testing.T) {
	fs := newFakeReaderStore()
	good, bad := key("pod-a", 1), key("pod-b", 2)
	fs.put(good, Entry{Kind: KindMT, Time: at(1)})
	fs.put(bad, Entry{Kind: KindQE, Time: at(2)})
	fs.getErr[bad] = errors.New("truncated")

	r := NewReader(fs, "")
	got, _ := r.Since(0)
	if len(got) != 1 || got[0].Kind != KindMT {
		t.Errorf("got %+v, want just the readable entry", got)
	}
	// Cached as empty, so a poll loop doesn't hammer it.
	r.mu.Lock()
	_, cached := r.cache[bad]
	r.mu.Unlock()
	if !cached {
		t.Error("unreadable key should be cached as empty to stop repeated retries")
	}
}

func TestReader_SkipsCorruptLinesWithinAnObject(t *testing.T) {
	fs := newFakeReaderStore()
	k := key("pod-a", 1)
	valid, _ := json.Marshal(Entry{Kind: KindMT, Time: at(1)})
	fs.mu.Lock()
	fs.objects[k] = string(valid) + "\n{not json\n\n" + string(valid) + "\n"
	fs.mu.Unlock()

	got, _ := NewReader(fs, "").Since(0)
	if len(got) != 2 {
		t.Errorf("got %d entries, want the 2 parseable ones", len(got))
	}
}

// Bodies are capped at 32 KiB each, so a single line can exceed bufio.Scanner's
// default 64 KiB limit once request and response are both near the cap.
func TestReader_HandlesLinesLargerThanTheDefaultScannerBuffer(t *testing.T) {
	fs := newFakeReaderStore()
	big := strings.Repeat("x", maxBodyCapture)
	fs.put(key("pod-a", 1), Entry{
		Kind: KindMT, Time: at(1),
		RequestBody:  big,
		ResponseBody: big,
	})

	got, _ := NewReader(fs, "").Since(0)
	if len(got) != 1 {
		t.Fatalf("a maxed-out entry was dropped: got %d entries", len(got))
	}
	if len(got[0].RequestBody) != maxBodyCapture {
		t.Errorf("request body truncated on read: %d bytes", len(got[0].RequestBody))
	}
}

func TestReader_EmptyPrefixYieldsNothing(t *testing.T) {
	got, last := NewReader(newFakeReaderStore(), "").Since(0)
	if len(got) != 0 || last != 0 {
		t.Errorf("empty bucket → (%d entries, cursor %d), want (0, 0)", len(got), last)
	}
}

// Reader must satisfy the handler's Source, same as Ring.
func TestReader_ImplementsSource(t *testing.T) {
	var _ Source = NewReader(newFakeReaderStore(), "")
	var _ Source = NewRing(1)
}
