package llmdebug

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRing_SeqMonotonicFromOne(t *testing.T) {
	r := NewRing(10)
	for i := 0; i < 3; i++ {
		r.Record(context.Background(), Entry{Kind: KindMT})
	}
	entries, lastSeq := r.Since(0)
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	for i, e := range entries {
		if e.Seq != uint64(i+1) {
			t.Errorf("entry %d: want seq %d, got %d", i, i+1, e.Seq)
		}
	}
	if lastSeq != 3 {
		t.Errorf("want lastSeq 3, got %d", lastSeq)
	}
}

func TestRing_EvictionKeepsSeqsValid(t *testing.T) {
	r := NewRing(200)
	for i := 0; i < 250; i++ {
		r.Record(context.Background(), Entry{Kind: KindMT})
	}
	entries, lastSeq := r.Since(0)
	if len(entries) != 200 {
		t.Fatalf("want capacity 200 entries after 250 records, got %d", len(entries))
	}
	if entries[0].Seq != 51 || entries[len(entries)-1].Seq != 250 {
		t.Errorf("want seqs 51..250, got %d..%d", entries[0].Seq, entries[len(entries)-1].Seq)
	}
	if lastSeq != 250 {
		t.Errorf("want lastSeq 250, got %d", lastSeq)
	}
}

func TestRing_SinceCursor(t *testing.T) {
	r := NewRing(10)
	for i := 0; i < 5; i++ {
		r.Record(context.Background(), Entry{Kind: KindQE})
	}
	entries, lastSeq := r.Since(3)
	if len(entries) != 2 {
		t.Fatalf("want 2 entries after cursor 3, got %d", len(entries))
	}
	if entries[0].Seq != 4 || entries[1].Seq != 5 {
		t.Errorf("want seqs 4,5, got %d,%d", entries[0].Seq, entries[1].Seq)
	}

	// Cursor at the tip: nothing new, lastSeq unchanged.
	entries, lastSeq = r.Since(lastSeq)
	if len(entries) != 0 {
		t.Errorf("want 0 entries at tip, got %d", len(entries))
	}
	if lastSeq != 5 {
		t.Errorf("want lastSeq 5, got %d", lastSeq)
	}
}

func TestRing_TruncatesBodies(t *testing.T) {
	r := NewRing(2)
	big := strings.Repeat("x", maxBodyCapture+100)
	r.Record(context.Background(), Entry{RequestBody: big, ResponseBody: big})
	entries, _ := r.Since(0)
	for _, body := range []string{entries[0].RequestBody, entries[0].ResponseBody} {
		if len(body) != maxBodyCapture+len("…[truncated]") {
			t.Errorf("want truncated body of %d bytes, got %d", maxBodyCapture+len("…[truncated]"), len(body))
		}
		if !strings.HasSuffix(body, "…[truncated]") {
			t.Error("want truncation marker suffix")
		}
	}
}

func TestRing_MetaFromContextWithMerge(t *testing.T) {
	r := NewRing(2)
	// Outer caller sets FileID+Project, inner closure adds Engine — all must land.
	ctx := WithMeta(context.Background(), Meta{FileID: 7, Project: "Demo Project"})
	ctx = WithMeta(ctx, Meta{Engine: "my-engine"})
	r.Record(ctx, Entry{Kind: KindMT})
	entries, _ := r.Since(0)
	e := entries[0]
	if e.FileID != 7 || e.Project != "Demo Project" || e.Engine != "my-engine" {
		t.Errorf("want merged meta FileID=7 Project=%q Engine=my-engine, got FileID=%d Project=%q Engine=%q",
			"Demo Project", e.FileID, e.Project, e.Engine)
	}

	// Explicit Entry fields win over ctx meta.
	r.Record(ctx, Entry{Kind: KindMT, FileID: 99})
	entries, _ = r.Since(1)
	if entries[0].FileID != 99 {
		t.Errorf("want explicit FileID=99 to win over ctx meta, got %d", entries[0].FileID)
	}
}

func TestRing_ConcurrentRecord(t *testing.T) {
	r := NewRing(50)
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				r.Record(context.Background(), Entry{Kind: KindAPE, Provider: fmt.Sprintf("g%d", g)})
			}
		}(g)
	}
	wg.Wait()
	entries, lastSeq := r.Since(0)
	if lastSeq != 200 {
		t.Errorf("want lastSeq 200 after 200 records, got %d", lastSeq)
	}
	if len(entries) != 50 {
		t.Errorf("want 50 entries (capacity), got %d", len(entries))
	}
	// Seqs must be strictly increasing with no duplicates.
	for i := 1; i < len(entries); i++ {
		if entries[i].Seq <= entries[i-1].Seq {
			t.Fatalf("seqs not strictly increasing at %d: %d then %d", i, entries[i-1].Seq, entries[i].Seq)
		}
	}
}

// Pins that Seq order and Time order agree under concurrent Record: the viewer
// orders by Seq and renders Time, so a disagreement shows an older entry ABOVE
// a newer one. A guard, not a reproduction — the failure
// needs a specific interleaving. Run with -race.
func TestRing_SeqOrderMatchesTimeOrderUnderConcurrency(t *testing.T) {
	const (
		goroutines = 8
		perRoutine = 50
	)
	r := NewRing(goroutines * perRoutine)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perRoutine; i++ {
				r.Record(context.Background(), Entry{Kind: KindMT})
			}
		}(g)
	}
	wg.Wait()

	entries, _ := r.Since(0)
	if len(entries) != goroutines*perRoutine {
		t.Fatalf("recorded %d entries, want %d", len(entries), goroutines*perRoutine)
	}
	// Since returns ascending by Seq; Time must be non-decreasing across it.
	for i := 1; i < len(entries); i++ {
		if entries[i].Time.Before(entries[i-1].Time) {
			t.Fatalf("entry %d (seq %d, %s) is older than entry %d (seq %d, %s) — the feed would show them out of order",
				i, entries[i].Seq, entries[i].Time.Format("15:04:05.000000000"),
				i-1, entries[i-1].Seq, entries[i-1].Time.Format("15:04:05.000000000"))
		}
	}
}
