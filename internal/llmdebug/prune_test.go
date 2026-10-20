package llmdebug

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// The retention boundary is the whole point of this function, so it's tested as
// a pure unit — no storage, no clock.
func TestStaleKeys(t *testing.T) {
	keys := []string{
		"llm-debug/2026-08-21/pod-a-1-1.ndjson", // today
		"llm-debug/2026-08-21/pod-b-2-1.ndjson", // today, other pod
		"llm-debug/2026-08-20/pod-a-3-1.ndjson", // yesterday
		"llm-debug/2026-08-19/pod-a-4-1.ndjson", // older
		"llm-debug/2026-07-31/pod-a-5-1.ndjson", // last month
		"llm-debug/2025-12-31/pod-a-6-1.ndjson", // last year
	}

	tests := []struct {
		name       string
		today      time.Time
		retainDays int
		want       []string
	}{
		{
			// The documented default: today + yesterday survive. Deleting
			// "anything older than 24h" would bin this morning's log at 00:01.
			name: "retain 1 keeps today and yesterday", today: day(2026, 8, 21), retainDays: 1,
			want: []string{
				"llm-debug/2025-12-31/pod-a-6-1.ndjson",
				"llm-debug/2026-07-31/pod-a-5-1.ndjson",
				"llm-debug/2026-08-19/pod-a-4-1.ndjson",
			},
		},
		{
			name: "retain 0 keeps only today", today: day(2026, 8, 21), retainDays: 0,
			want: []string{
				"llm-debug/2025-12-31/pod-a-6-1.ndjson",
				"llm-debug/2026-07-31/pod-a-5-1.ndjson",
				"llm-debug/2026-08-19/pod-a-4-1.ndjson",
				"llm-debug/2026-08-20/pod-a-3-1.ndjson",
			},
		},
		{
			name: "retain 7 keeps everything within the week", today: day(2026, 8, 21), retainDays: 7,
			want: []string{
				"llm-debug/2025-12-31/pod-a-6-1.ndjson",
				"llm-debug/2026-07-31/pod-a-5-1.ndjson",
			},
		},
		{
			// Month boundary: the cutoff has to walk back across it, not clamp.
			name: "cutoff crosses a month boundary", today: day(2026, 8, 1), retainDays: 1,
			want: []string{
				"llm-debug/2025-12-31/pod-a-6-1.ndjson",
			},
		},
		{
			name: "cutoff crosses a year boundary", today: day(2026, 1, 1), retainDays: 1,
			want: []string{},
		},
		{
			// A future-dated key (clock skew between pods) must never be deleted.
			name: "nothing stale yet", today: day(2025, 1, 1), retainDays: 1,
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := staleKeys(keys, DefaultKeyPrefix, tc.today, tc.retainDays)
			sort.Strings(got)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("staleKeys =\n%v\nwant\n%v", got, tc.want)
			}
		})
	}
}

// Anything we can't confidently date is left alone. Deleting an unparseable key
// risks binning something that isn't ours.
func TestStaleKeys_IgnoresKeysItCannotDate(t *testing.T) {
	keys := []string{
		"llm-debug/not-a-date/pod-a-1-1.ndjson",
		"llm-debug/2026-13-45/pod-a-1-1.ndjson", // parses as a layout but isn't a real date
		"llm-debug/README",                      // no date component at all
		"llm-debug/",
		"projects/12/1234_source.docx", // another namespace entirely
		"llm-debug/2026-08-19/pod-a-4-1.ndjson",
	}
	got := staleKeys(keys, DefaultKeyPrefix, day(2026, 8, 21), 1)
	want := []string{"llm-debug/2026-08-19/pod-a-4-1.ndjson"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("staleKeys = %v, want only the datable stale key %v", got, want)
	}
}

// Round-trip against the writer: whatever Store.key() produces must be datable
// by the prune. These two agreeing is the contract that keeps objects from
// accumulating forever.
func TestStaleKeys_MatchesTheKeysStoreWrites(t *testing.T) {
	fs := newFakeStore()
	s := testStore(t, fs, StoreOpts{
		BatchSize: 1, FlushInterval: time.Hour, Host: "pod-a",
		Now: fixedNow(time.Date(2026, 8, 19, 23, 59, 59, 0, time.UTC)),
	})
	s.Record(context.Background(), Entry{Kind: KindMT})
	waitFor(t, "put", func() bool { return fs.count() == 1 })

	var key string
	for k := range fs.all() {
		key = k
	}
	if got := staleKeys([]string{key}, DefaultKeyPrefix, day(2026, 8, 21), 1); len(got) != 1 {
		t.Fatalf("the prune could not date the writer's own key %q", key)
	}
	if got := staleKeys([]string{key}, DefaultKeyPrefix, day(2026, 8, 19), 1); len(got) != 0 {
		t.Errorf("key %q written today must not be considered stale", key)
	}
}

type fakePruneStore struct {
	keys     []string
	deleted  []string
	listErr  error
	delErr   error
	listCall int
}

func (f *fakePruneStore) ListKeys(_ context.Context, _ string) ([]string, error) {
	f.listCall++
	return f.keys, f.listErr
}

func (f *fakePruneStore) DeleteKeys(_ context.Context, keys []string) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = append(f.deleted, keys...)
	return nil
}

func TestPruner_DeletesOnlyStaleKeys(t *testing.T) {
	fs := &fakePruneStore{keys: []string{
		"llm-debug/2026-08-21/pod-a-1-1.ndjson",
		"llm-debug/2026-08-20/pod-a-2-1.ndjson",
		"llm-debug/2026-08-18/pod-a-3-1.ndjson",
		"llm-debug/2026-08-01/pod-b-4-1.ndjson",
	}}
	p := NewPruner(fs, PrunerOpts{RetainDays: 1, Now: fixedNow(day(2026, 8, 21))})

	n, err := p.Prune(context.Background())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted %d, want 2", n)
	}
	sort.Strings(fs.deleted)
	want := []string{"llm-debug/2026-08-01/pod-b-4-1.ndjson", "llm-debug/2026-08-18/pod-a-3-1.ndjson"}
	if !reflect.DeepEqual(fs.deleted, want) {
		t.Errorf("deleted %v, want %v", fs.deleted, want)
	}
}

func TestPruner_NoStaleKeysSkipsTheDeleteCall(t *testing.T) {
	fs := &fakePruneStore{keys: []string{"llm-debug/2026-08-21/pod-a-1-1.ndjson"}}
	p := NewPruner(fs, PrunerOpts{RetainDays: 1, Now: fixedNow(day(2026, 8, 21))})

	n, err := p.Prune(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("Prune = (%d, %v), want (0, nil)", n, err)
	}
	if fs.deleted != nil {
		t.Errorf("nothing was stale, so DeleteKeys should not have been called; got %v", fs.deleted)
	}
}

func TestPruner_PropagatesErrors(t *testing.T) {
	listBoom := errors.New("list exploded")
	p := NewPruner(&fakePruneStore{listErr: listBoom}, PrunerOpts{RetainDays: 1})
	if _, err := p.Prune(context.Background()); !errors.Is(err, listBoom) {
		t.Errorf("list error should propagate, got %v", err)
	}

	delBoom := errors.New("delete exploded")
	fs := &fakePruneStore{
		keys:   []string{"llm-debug/2026-08-01/pod-a-1-1.ndjson"},
		delErr: delBoom,
	}
	p = NewPruner(fs, PrunerOpts{RetainDays: 1, Now: fixedNow(day(2026, 8, 21))})
	if _, err := p.Prune(context.Background()); !errors.Is(err, delBoom) {
		t.Errorf("delete error should propagate, got %v", err)
	}
}

// A negative RetainDays would compute a cutoff in the future and delete today's
// log — clamp instead.
func TestPruner_NegativeRetainDaysIsClampedToZero(t *testing.T) {
	fs := &fakePruneStore{keys: []string{"llm-debug/2026-08-21/pod-a-1-1.ndjson"}}
	p := NewPruner(fs, PrunerOpts{RetainDays: -5, Now: fixedNow(day(2026, 8, 21))})
	n, err := p.Prune(context.Background())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 0 {
		t.Errorf("today's objects must survive a negative retention, deleted %d", n)
	}
}
