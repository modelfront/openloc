package llmdebug

// Reader serves the debug feed in production by reading back what Store wrote.
// Dev reads the in-memory Ring; prod reads object storage,
// behind RequirePlatformAdmin because these entries span every tenant.
//
// Two design points that are not obvious:
//
//   - **The cursor is entry time, not a sequence.** Ring.Seq is assigned
//     per-process, and prod runs replicas=2 with no sticky sessions: consecutive
//     polls land on different pods, so a pod-local counter would produce
//     duplicates and gaps. Entry.Time in microseconds is derived from content,
//     so every pod computes the same cursor for the same entry. Seq is
//     overwritten with it on load, which keeps the existing frontend (and its
//     `key={e.seq}`) working untouched.
//
//   - **Objects are immutable, so parsing is cached by key.** A poll re-lists
//     (cheap) and downloads only keys it hasn't seen. Keys that vanish — pruned
//     — are dropped from the cache, which is also what bounds its growth.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"openloc/internal/logging"
)

// readerStore is the storage seam for reading — list + get. *file.Storage
// satisfies it.
type readerStore interface {
	ListKeys(ctx context.Context, prefix string) ([]string, error)
	Download(ctx context.Context, key string) (io.ReadCloser, error)
}

const (
	// maxPerPoll bounds one response. A first poll (cursor 0) against two days
	// of retained objects could otherwise return tens of thousands of entries,
	// each up to 64 KiB. When more than this is available the NEWEST are
	// returned and the cursor jumps forward — for a live viewer, recent
	// activity is the point, and the older objects are still in the bucket.
	maxPerPoll = 200

	// maxCachedObjects bounds parsed-object memory. Retention keeps ~2 days, so
	// this is a backstop for a burst, not the normal path.
	maxCachedObjects = 500

	// listTimeout / getTimeout bound one poll's storage work so a wedged object store
	// surfaces as an empty feed rather than a hanging request.
	listTimeout = 10 * time.Second
	getTimeout  = 15 * time.Second
)

// Reader implements Source over the objects Store wrote.
type Reader struct {
	store  readerStore
	prefix string

	mu     sync.Mutex
	cache  map[string][]Entry // object key → parsed entries
	merged []Entry            // all cached entries, ascending by Seq
}

// NewReader returns a Reader over prefix (defaults to DefaultKeyPrefix).
func NewReader(store readerStore, prefix string) *Reader {
	if prefix == "" {
		prefix = DefaultKeyPrefix
	}
	return &Reader{store: store, prefix: prefix, cache: map[string][]Entry{}}
}

// Since returns entries with a cursor greater than after, ascending, plus the
// cursor to poll with next. Implements Source.
//
// Storage errors are logged and treated as "nothing new" rather than surfaced:
// the feed is a debug aid, and a failed list should leave the viewer showing
// what it already has instead of erroring the page.
func (r *Reader) Since(after uint64) ([]Entry, uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()

	keys, err := r.store.ListKeys(ctx, r.prefix+"/")
	if err != nil {
		logging.Warn(ctx, err, "list llm debug objects", "llm-debug-list-failed",
			logging.Label("prefix", r.prefix))
		return r.since(after)
	}
	r.refresh(keys)
	return r.since(after)
}

// refresh loads keys not yet cached and forgets keys that no longer exist.
func (r *Reader) refresh(keys []string) {
	// Newest first, so a truncated load keeps the most recent objects. Keys
	// embed a unix-ms component, and lexical order matches time order for a
	// fixed-width millisecond field within the same date prefix.
	sorted := make([]string, len(keys))
	copy(sorted, keys)
	sort.Sort(sort.Reverse(sort.StringSlice(sorted)))
	if len(sorted) > maxCachedObjects {
		sorted = sorted[:maxCachedObjects]
	}

	live := make(map[string]bool, len(sorted))
	for _, k := range sorted {
		live[k] = true
	}

	r.mu.Lock()
	var toFetch []string
	for _, k := range sorted {
		if _, ok := r.cache[k]; !ok {
			toFetch = append(toFetch, k)
		}
	}
	// Drop pruned (or trimmed) keys.
	changed := false
	for k := range r.cache {
		if !live[k] {
			delete(r.cache, k)
			changed = true
		}
	}
	r.mu.Unlock()

	for _, k := range toFetch {
		entries, err := r.load(k)
		if err != nil {
			// A single unreadable object must not blank the whole feed. Cache the
			// empty result so a poll loop doesn't retry it forever.
			logging.Warn(context.Background(), err, "read llm debug object", "llm-debug-read-failed",
				logging.Label("key", k))
			entries = []Entry{}
		}
		r.mu.Lock()
		r.cache[k] = entries
		r.mu.Unlock()
		changed = true
	}

	if changed {
		r.remerge()
	}
}

// load downloads one object and parses its NDJSON lines.
func (r *Reader) load(key string) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), getTimeout)
	defer cancel()

	rc, err := r.store.Download(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var out []Entry
	sc := bufio.NewScanner(rc)
	// Entry bodies are capped at 32 KiB each, so a line can exceed bufio's
	// default 64 KiB limit once request + response are both near the cap.
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip a corrupt line rather than lose the object
		}
		// The cursor: content-derived, so every replica agrees.
		e.Seq = uint64(e.Time.UnixMicro())
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// remerge rebuilds the flat ascending view. Called under no lock; takes its own.
func (r *Reader) remerge() {
	r.mu.Lock()
	defer r.mu.Unlock()
	merged := make([]Entry, 0, len(r.merged))
	for _, entries := range r.cache {
		merged = append(merged, entries...)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Seq < merged[j].Seq })
	r.merged = merged
}

// since filters the merged view against the cursor.
func (r *Reader) since(after uint64) ([]Entry, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	last := after
	if n := len(r.merged); n > 0 && r.merged[n-1].Seq > last {
		last = r.merged[n-1].Seq
	}

	idx := sort.Search(len(r.merged), func(i int) bool { return r.merged[i].Seq > after })
	fresh := r.merged[idx:]
	if len(fresh) > maxPerPoll {
		fresh = fresh[len(fresh)-maxPerPoll:] // keep the newest
	}
	out := make([]Entry, len(fresh))
	copy(out, fresh)
	return out, last
}
