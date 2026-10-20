package llmdebug

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func doList(t *testing.T, h *Handler, query string) (*httptest.ResponseRecorder, struct {
	Data struct {
		Entries []Entry `json:"entries"`
		LastSeq uint64  `json:"last_seq"`
	} `json:"data"`
}) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/debug/llm/calls"+query, nil)
	h.HandleList(rec, req)
	var resp struct {
		Data struct {
			Entries []Entry `json:"entries"`
			LastSeq uint64  `json:"last_seq"`
		} `json:"data"`
	}
	if rec.Code == http.StatusOK {
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec, resp
}

func TestHandleList_All(t *testing.T) {
	ring := NewRing(10)
	for i := 0; i < 3; i++ {
		ring.Record(context.Background(), Entry{Kind: KindMT})
	}
	rec, resp := doList(t, NewHandler(ring), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if len(resp.Data.Entries) != 3 || resp.Data.LastSeq != 3 {
		t.Errorf("want 3 entries lastSeq 3, got %d entries lastSeq %d", len(resp.Data.Entries), resp.Data.LastSeq)
	}
}

func TestHandleList_AfterCursor(t *testing.T) {
	ring := NewRing(10)
	for i := 0; i < 5; i++ {
		ring.Record(context.Background(), Entry{Kind: KindQE})
	}
	_, resp := doList(t, NewHandler(ring), "?after=4")
	if len(resp.Data.Entries) != 1 || resp.Data.Entries[0].Seq != 5 {
		t.Errorf("want just seq 5 after cursor 4, got %+v", resp.Data.Entries)
	}
}

func TestHandleList_BadCursor(t *testing.T) {
	rec, _ := doList(t, NewHandler(NewRing(10)), "?after=banana")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for garbage cursor, got %d", rec.Code)
	}
}

func TestHandleList_EmptyRingIsArrayNotNull(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/debug/llm/calls", nil)
	NewHandler(NewRing(10)).HandleList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var raw struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw.Data["entries"]) != "[]" {
		t.Errorf(`want "entries":[] (never null), got %s`, raw.Data["entries"])
	}
}

type fakeStats struct{ s StoreStats }

func (f fakeStats) Stats() StoreStats { return f.s }

// An entry that never reaches storage looks identical to one never captured, so
// the write-side counters have to be readable from the feed itself
func TestHandleList_ReportsWriteSideCountersWhenAvailable(t *testing.T) {
	ring := NewRing(4)
	ring.Record(context.Background(), Entry{Kind: KindMT})

	h := NewHandler(ring).WithStats(fakeStats{StoreStats{
		Written: 7, Entries: 42, Dropped: 3, Failed: 1,
	}})
	rec := httptest.NewRecorder()
	h.HandleList(rec, httptest.NewRequest(http.MethodGet, "/api/debug/llm/calls?after=0", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Data struct {
			Entries []Entry `json:"entries"`
			Stats   struct {
				ObjectsWritten int64 `json:"objects_written"`
				EntriesWritten int64 `json:"entries_written"`
				EntriesDropped int64 `json:"entries_dropped"`
				BatchesFailed  int64 `json:"batches_failed"`
			} `json:"stats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	if body.Data.Stats.EntriesDropped != 3 || body.Data.Stats.BatchesFailed != 1 {
		t.Errorf("stats = %+v, want dropped=3 failed=1 — these are the two numbers that explain a missing entry", body.Data.Stats)
	}
	if len(body.Data.Entries) != 1 {
		t.Errorf("entries = %d, want 1 (stats must not displace the feed)", len(body.Data.Entries))
	}
}

// The dev ring has no write side, so the key is simply absent rather than zeroed
// — a zero would read as "nothing dropped", which is a claim we can't make.
func TestHandleList_OmitsStatsWhenThereIsNoWriteSide(t *testing.T) {
	h := NewHandler(NewRing(2))
	rec := httptest.NewRecorder()
	h.HandleList(rec, httptest.NewRequest(http.MethodGet, "/api/debug/llm/calls", nil))

	var raw map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := raw["data"]["stats"]; present {
		t.Error("stats must be absent without a Store, not reported as zero")
	}
}

// A typed nil must not become a non-nil interface that panics on first use.
func TestWithStats_IgnoresNil(t *testing.T) {
	h := NewHandler(NewRing(1)).WithStats(nil)
	rec := httptest.NewRecorder()
	h.HandleList(rec, httptest.NewRequest(http.MethodGet, "/api/debug/llm/calls", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
