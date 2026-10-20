package llmdebug

// Store is the PRODUCTION half of llmdebug: the same captured Entries the dev
// Ring holds in memory, written to object storage as NDJSON so a bad
// translation can be investigated after the fact.
//
// Why object storage and not a file or memory: prod runs replicas=2 with the
// asynq worker in-process, so provider calls originate in both pods, and app
// pods have no volume — a local file would be two partial logs, both discarded
// on every deploy. Both pods share one bucket (one object-store Service, one PVC), so
// the bucket is the only place that is both shared and durable.
//
// Three properties that matter more than they look:
//
//   - **Record never blocks.** It hands off to a buffered channel and drops on
//     overflow. The object store runs replicas=1, so it is a single point of failure; a
//     stall there must not add a millisecond to a translation job, let alone
//     fail one.
//   - **One object per flush batch**, not one per day. S3 has no append, so a
//     day-object would mean read-modify-rewrite of a growing blob on every
//     flush (O(n²) bytes/day) and a lost-update race between the two pods.
//     Small immutable objects under a shared date prefix need no coordination.
//   - **The key carries the pod.** A shared bucket means a shared key namespace
//     and PutObject replaces whole objects, so without a per-pod component the
//     two replicas would overwrite each other.
//
// Retention is by date prefix — see prune.go. Store only writes; the
// persisted feed is read back by Reader, which api.go mounts behind
// RequirePlatformAdmin because an Entry has no tenant_id.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"openloc/internal/logging"
)

// objectStore is the narrow storage seam — same idiom as pipeline.FileStorage,
// which keeps llmdebug from importing internal/file and keeps this testable
// with a fake. *file.Storage satisfies it.
type objectStore interface {
	Put(ctx context.Context, key string, body io.Reader) error
}

// DefaultKeyPrefix is the bucket prefix every debug object lives under. The
// prune deletes whole date sub-prefixes below it.
const DefaultKeyPrefix = "llm-debug"

// dateLayout is the date form used in object keys AND parsed by the prune. One
// constant so the writer and the reaper cannot disagree.
const dateLayout = "2006-01-02"

const (
	// defaultBatchSize / defaultFlushInterval trade object count against
	// staleness. 60s (not 5s) is deliberate: an interval that short would mint
	// ~17k tiny objects per pod per day on a lightly-loaded service, while a
	// busy one fills batches by count long before the timer fires. An idle
	// service writes nothing at all.
	defaultBatchSize     = 100
	defaultFlushInterval = 60 * time.Second

	// defaultBufferSize is the hand-off queue depth. 2048 × ~64 KiB worst case
	// is a bounded ceiling, and in practice the flusher drains far faster than
	// the pipeline records (llmConcurrency=5).
	defaultBufferSize = 2048

	// putTimeout bounds one object write. Detached from the caller's context on
	// purpose — a cancelled job (browser refresh) should
	// still get its debug batch persisted.
	putTimeout = 30 * time.Second
)

// StoreOpts configures a Store. Every field has a usable zero value.
type StoreOpts struct {
	// KeyPrefix defaults to DefaultKeyPrefix.
	KeyPrefix string
	// BatchSize flushes as soon as this many entries are buffered.
	BatchSize int
	// FlushInterval flushes a partial batch this often.
	FlushInterval time.Duration
	// BufferSize is the record hand-off queue depth.
	BufferSize int
	// Host identifies the writing pod in object keys. Defaults to the hostname
	// (the pod name under k8s), then $HOSTNAME, then "unknown".
	Host string
	// Now is injectable for tests.
	Now func() time.Time
}

// StoreStats is a cheap health signal: sustained Dropped means the buffer is
// too small or storage too slow; Failed means writes are erroring.
type StoreStats struct {
	Written int64 // objects successfully put
	Entries int64 // entries inside those objects
	Dropped int64 // entries discarded because the buffer was full
	Failed  int64 // batches lost to a Put error
}

// Store implements Recorder by batching entries into NDJSON objects.
type Store struct {
	os     objectStore
	opts   StoreOpts
	ch     chan Entry
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	closed atomic.Bool

	written, entries, dropped, failed atomic.Int64
}

// batchSeq is PACKAGE level, not per-Store, so two Stores in one process (same
// host, same millisecond) never mint the same key and overwrite each other's
// batch. Cross-pod keys are safe either way: pod names are unique.
var batchSeq atomic.Uint64

// NewStore starts the flusher goroutine and returns a ready Store. Call Close
// on shutdown so buffered entries are written rather than discarded.
func NewStore(store objectStore, opts StoreOpts) *Store {
	s := newStore(store, opts)
	go s.run()
	return s
}

// newStore builds the Store without starting its flusher. Record queues into
// the buffer either way, so a test can load a whole burst before run() gets
// to drain it — with the flusher already running, "how many entries share an
// object" depends on scheduling.
func newStore(store objectStore, opts StoreOpts) *Store {
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = DefaultKeyPrefix
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultFlushInterval
	}
	if opts.BufferSize <= 0 {
		opts.BufferSize = defaultBufferSize
	}
	if opts.Host == "" {
		opts.Host = hostname()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Store{
		os:   store,
		opts: opts,
		ch:   make(chan Entry, opts.BufferSize),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// Record normalizes the entry and queues it. Never blocks: on a full buffer the
// entry is dropped and counted. Safe to call after Close (the entry is dropped).
func (s *Store) Record(ctx context.Context, e Entry) {
	if s.closed.Load() {
		s.dropped.Add(1)
		return
	}
	e = normalize(ctx, e)
	if e.Host == "" {
		// Which pod wrote it — the discriminator for a half-missing job.
		e.Host = s.opts.Host
	}
	if e.Time.IsZero() {
		// The Reader derives its cursor from Time, so Time IS the ordering key
		// on this path — no second key to disagree with.
		e.Time = time.Now()
	}
	select {
	case s.ch <- e:
	default:
		// Deliberately lossy. The alternative — blocking — puts object-storage
		// latency directly into the translation path.
		s.dropped.Add(1)
	}
}

// Stats returns a snapshot of the counters.
func (s *Store) Stats() StoreStats {
	return StoreStats{
		Written: s.written.Load(),
		Entries: s.entries.Load(),
		Dropped: s.dropped.Load(),
		Failed:  s.failed.Load(),
	}
}

// Close stops the flusher, writes whatever is buffered, and returns once the
// flusher has exited or ctx expires. Idempotent.
func (s *Store) Close(ctx context.Context) error {
	s.once.Do(func() {
		s.closed.Store(true)
		close(s.stop)
	})
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) run() {
	defer close(s.done)
	ticker := time.NewTicker(s.opts.FlushInterval)
	defer ticker.Stop()

	batch := make([]Entry, 0, s.opts.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return // never write an empty object
		}
		s.flush(batch)
		batch = batch[:0]
	}

	// drain moves everything already queued into the batch, flushing whenever it
	// fills. Returns with the queue empty.
	drain := func() {
		for {
			select {
			case e := <-s.ch:
				batch = append(batch, e)
				if len(batch) >= s.opts.BatchSize {
					flush()
				}
			default:
				return
			}
		}
	}

	for {
		select {
		case e := <-s.ch:
			batch = append(batch, e)
			if len(batch) >= s.opts.BatchSize {
				flush()
				continue
			}
			// Take everything else that's already waiting, then write — the
			// queue going quiet means the burst is over.
			//
			// Durability, not latency, is the point: a process that ends
			// (rolling deploy, SIGKILL, eviction) loses at most the in-flight
			// burst, never up to FlushInterval of buffer.
			// A burst still coalesces into one object, so this costs objects
			// only when calls are sparse, which is when an extra one is cheap.
			drain()
			flush()
		case <-ticker.C:
			// Backstop: a batch that filled to BatchSize mid-burst can leave a
			// remainder behind, and the ticker sweeps it.
			flush()
		case <-s.stop:
			// Still drain on shutdown: an entry can arrive between the last
			// quiescent flush and the stop signal.
			drain()
			flush()
			return
		}
	}
}

// flush writes one batch as a single NDJSON object.
func (s *Store) flush(batch []Entry) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf) // Encode appends '\n' — NDJSON for free
	written := 0
	for i := range batch {
		if err := enc.Encode(batch[i]); err != nil {
			// An Entry is plain strings and ints, so this is unreachable today;
			// skipping one line beats losing the batch if that ever changes.
			continue
		}
		written++
	}
	if written == 0 {
		return
	}

	key := s.key()
	ctx, cancel := context.WithTimeout(context.Background(), putTimeout)
	defer cancel()

	if err := s.os.Put(ctx, key, bytes.NewReader(buf.Bytes())); err != nil {
		s.failed.Add(1)
		// Warn, not Error: losing debug telemetry is not user-visible, and a
		// storage outage would otherwise report one notice per batch.
		logging.Warn(ctx, err, "persist llm debug batch", "llm-debug-put-failed",
			logging.Label("key", key),
			logging.Debugf("entries", written),
		)
		return
	}
	s.written.Add(1)
	s.entries.Add(int64(written))
}

// key is <prefix>/<UTC date>/<host>-<unix-ms>-<n>.ndjson. UTC so the prune's
// date arithmetic can't shift under a pod's local zone; <n> so two batches from
// one pod inside the same millisecond can't collide.
func (s *Store) key() string {
	now := s.opts.Now().UTC()
	return fmt.Sprintf("%s/%s/%s-%d-%d.ndjson",
		s.opts.KeyPrefix,
		now.Format(dateLayout),
		s.opts.Host,
		now.UnixMilli(),
		batchSeq.Add(1),
	)
}

func hostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	if h := os.Getenv("HOSTNAME"); h != "" {
		return h
	}
	return "unknown"
}
