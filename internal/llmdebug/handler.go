package llmdebug

import (
	"net/http"
	"strconv"

	"openloc/internal/httputil"
)

// Source is where the feed reads entries from. Two implementations:
//
//   - *Ring    — dev, in-memory, filled by the capture sites as calls happen.
//   - *Reader  — production, reads back the NDJSON objects the Store wrote
//
// The cursor is opaque to the handler; each Source defines it (the Ring uses its
// own sequence, the Reader uses entry time — see Reader.Since for why).
type Source interface {
	Since(after uint64) (entries []Entry, lastSeq uint64)
}

// StatsSource reports write-side counters. *Store implements it.
//
// Surfaced on the feed because an entry that never reaches storage is otherwise
// indistinguishable from one that was never captured: the UI just shows a gap,
// and the only evidence is a warning line in a pod log nobody is tailing. A
// non-zero Dropped means the hand-off buffer overflowed; a non-zero Failed means
// PUTs are erroring. Both are silent by design (capture must never disturb a
// translation job) — so they have to be readable somewhere.
type StatsSource interface {
	Stats() StoreStats
}

// Handler serves the debug feed. Exactly one route is mounted, and WHERE it is
// mounted depends on the Source (see internal/api/api.go): the dev ring sits in
// the normal authenticated group, while the production object-storage reader
// sits behind RequirePlatformAdmin because its entries span every tenant.
type Handler struct {
	src   Source
	stats StatsSource
}

func NewHandler(src Source) *Handler { return &Handler{src: src} }

// WithStats attaches write-side counters to the feed response. Optional: the dev
// ring has no write side.
func (h *Handler) WithStats(s StatsSource) *Handler {
	if s != nil {
		h.stats = s
	}
	return h
}

// HandleList is GET /api/debug/llm/calls?after=N — cursor poll returning
// entries with seq > after (ascending) and the latest seq for the next poll.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	var after uint64
	if s := r.URL.Query().Get("after"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			httputil.BadRequest(w, "invalid 'after' cursor")
			return
		}
		after = v
	}
	entries, lastSeq := h.src.Since(after)
	if entries == nil {
		entries = []Entry{}
	}
	payload := map[string]any{"entries": entries, "last_seq": lastSeq}
	if h.stats != nil {
		st := h.stats.Stats()
		payload["stats"] = map[string]any{
			"objects_written": st.Written,
			"entries_written": st.Entries,
			"entries_dropped": st.Dropped, // buffer overflow — capture is lossy by design
			"batches_failed":  st.Failed,  // PUT errors — entries lost after capture
		}
	}
	httputil.Data(w, payload)
}
