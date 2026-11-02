package llmdebug

import (
	"context"
	"slices"
	"testing"
	"time"
)

// searchLog is a newest-first snapshot: seq 6 down to 1, one second apart.
func searchLog() []Entry {
	t0 := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	es := []Entry{
		{Seq: 1, Kind: KindMT, JobID: 0, FileID: 10, Project: "P", Status: 200},
		{Seq: 2, Kind: KindQE, JobID: 100, FileID: 10, Project: "P", Status: 200},
		{Seq: 3, Kind: KindAPE, JobID: 100, FileID: 10, Project: "P", Status: 500},
		{Seq: 4, Kind: KindMT, JobID: 200, FileID: 20, Project: "Q", Status: 200, Retry: true},
		{Seq: 5, Kind: KindExtension, Provider: "ext:x", Status: 0},
		{Seq: 6, Kind: KindQE, JobID: 100, FileID: 10, Project: "P", Status: 200},
	}
	for i := range es {
		es[i].Time = t0.Add(time.Duration(es[i].Seq) * time.Second)
	}
	slices.Reverse(es)
	return es
}

func seqs(es []Entry) []uint64 {
	out := make([]uint64, len(es))
	for i, e := range es {
		out[i] = e.Seq
	}
	return out
}

func TestFilter_Match(t *testing.T) {
	log := searchLog()
	t0 := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		f    Filter
		want []uint64
	}{
		"everything":        {Filter{}, []uint64{6, 5, 4, 3, 2, 1}},
		"job":               {Filter{JobID: 100}, []uint64{6, 3, 2}},
		"file":              {Filter{FileID: 20}, []uint64{4}},
		"file set skips 0":  {Filter{FileIDs: map[int64]bool{10: true}}, []uint64{6, 3, 2, 1}},
		"empty file set":    {Filter{FileIDs: map[int64]bool{}}, nil},
		"kind":              {Filter{Kind: KindQE}, []uint64{6, 2}},
		"from is inclusive": {Filter{From: t0.Add(5 * time.Second)}, []uint64{6, 5}},
		"to is exclusive":   {Filter{To: t0.Add(2 * time.Second)}, []uint64{1}},
		"before the cursor": {Filter{Before: 4}, []uint64{3, 2, 1}},
		"filters add up":    {Filter{JobID: 100, Kind: KindQE, Before: 6}, []uint64{2}},
	} {
		var got []uint64
		for _, e := range log {
			if c.f.Match(e) {
				got = append(got, e.Seq)
			}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

func TestPage_WalksEveryMatchOnceWithTheCursor(t *testing.T) {
	log := searchLog()
	var all []uint64
	f := Filter{FileIDs: map[int64]bool{10: true, 20: true}}
	for range 10 {
		page, next := Page(log, f, 2)
		all = append(all, seqs(page)...)
		if next == 0 {
			break
		}
		f.Before = next
	}
	if want := []uint64{6, 4, 3, 2, 1}; !slices.Equal(all, want) {
		t.Errorf("pages %v, want %v", all, want)
	}

	// An exact last page says there is no more.
	if page, next := Page(log, Filter{JobID: 100}, 3); len(page) != 3 || next != 0 {
		t.Errorf("exact page: %v, next %d", seqs(page), next)
	}
}

func TestGroups_CountsPerJobAndFile(t *testing.T) {
	log := searchLog()
	t0 := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

	byJob, total := Groups(log, Filter{FileIDs: map[int64]bool{10: true, 20: true}}, GroupByJob, 10)
	want := []Group{
		{JobID: 100, FileID: 10, Project: "P", Calls: 3, Errors: 1, First: t0.Add(2 * time.Second), Last: t0.Add(6 * time.Second)},
		{JobID: 200, FileID: 20, Project: "Q", Calls: 1, Retries: 1, First: t0.Add(4 * time.Second), Last: t0.Add(4 * time.Second)},
		{JobID: 0, FileID: 0, Project: "", Calls: 1, First: t0.Add(time.Second), Last: t0.Add(time.Second)},
	}
	if total != 3 || !slices.Equal(byJob, want) {
		t.Errorf("by job (%d): %+v\nwant %+v", total, byJob, want)
	}

	byFile, _ := Groups(log, Filter{}, GroupByFile, 10)
	calls := map[int64]int{}
	for _, g := range byFile {
		calls[g.FileID] = g.Calls
		if g.FileID == 0 && g.Project != "" {
			t.Errorf("the no-file group is labelled with project %q", g.Project)
		}
	}
	if calls[10] != 4 || calls[20] != 1 || calls[0] != 1 || len(byFile) != 3 {
		t.Errorf("by file: %+v", byFile)
	}

	cut, total := Groups(log, Filter{}, GroupByJob, 1)
	if len(cut) != 1 || total != 3 || cut[0].JobID != 100 {
		t.Errorf("cut: %+v of %d", cut, total)
	}
}

func TestRing_SnapshotIsNewestFirst(t *testing.T) {
	r := NewRing(3)
	for range 4 {
		r.Record(context.Background(), Entry{})
	}
	if snap, complete := r.Snapshot(context.Background()); !complete || !slices.Equal(seqs(snap), []uint64{4, 3, 2}) {
		t.Errorf("snapshot %v, complete %v", seqs(snap), complete)
	}
}

// Since caps a poll at maxPerPoll; a snapshot holds every cached entry.
func TestReader_SnapshotIsNotCappedLikeAPoll(t *testing.T) {
	store := newFakeReaderStore()
	var es []Entry
	for i := range maxPerPoll + 50 {
		es = append(es, Entry{Time: at(i), JobID: 1})
	}
	store.put(key("a", 1), es...)
	r := NewReader(store, "")

	snap, complete := r.Snapshot(context.Background())
	if !complete {
		t.Error("a healthy store reported a partial snapshot")
	}
	if len(snap) != maxPerPoll+50 {
		t.Fatalf("snapshot holds %d entries, want %d", len(snap), maxPerPoll+50)
	}
	if !snap[0].Time.Equal(at(maxPerPoll+49)) || !snap[len(snap)-1].Time.Equal(at(0)) {
		t.Errorf("not newest first: %v … %v", snap[0].Time, snap[len(snap)-1].Time)
	}
	if polled, _ := r.Since(0); len(polled) != maxPerPoll {
		t.Errorf("a poll returned %d", len(polled))
	}
}

// A wedged object store must not hold a search past the caller's deadline:
// Snapshot answers with what it could read, says it is partial, and loads the
// missing object on a later call instead of caching it as empty.
func TestReader_SnapshotStopsAtTheCallersDeadline(t *testing.T) {
	store := newFakeReaderStore()
	store.put(key("a", 2), Entry{Time: at(2), JobID: 2})
	store.put(key("a", 1), Entry{Time: at(1), JobID: 1})
	store.block[key("a", 1)] = true
	r := NewReader(store, "")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	snap, complete := r.Snapshot(ctx)
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("snapshot took %v past a 100ms deadline", took)
	}
	if complete || len(snap) != 1 || snap[0].JobID != 2 {
		t.Errorf("wedged store: %d entries, complete %v", len(snap), complete)
	}

	store.mu.Lock()
	store.block[key("a", 1)] = false
	store.mu.Unlock()
	if snap, complete := r.Snapshot(context.Background()); !complete || len(snap) != 2 {
		t.Errorf("after the store recovered: %d entries, complete %v", len(snap), complete)
	}
}
