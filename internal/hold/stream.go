package hold

import (
	"net/http"
	"time"

	"openloc/internal/auth"
	"openloc/internal/httputil"
)

// StreamHandler serves GET /api/jobs/{jobID}/holds/stream (and the editor
// scope's twin): an event each time the holds of its jobs change, so an open
// editor refetches GET /holds at once rather than at its next poll. The events carry no data; the list stays the
// one tenant-scoped read.
type StreamHandler struct {
	feed *Feed
	// draining is closed when the process shuts down, so the stream ends and
	// http.Server.Shutdown can finish. nil never drains.
	draining <-chan struct{}
	streams  *httputil.StreamCap
}

// NewStreamHandler returns a StreamHandler over a listening feed.
func NewStreamHandler(feed *Feed, draining <-chan struct{}) *StreamHandler {
	return &StreamHandler{feed: feed, draining: draining, streams: httputil.NewStreamCap()}
}

// HandleStream is Stream for the route's {jobID}. The job route group gates
// it like the list: members of the job's organization, and a linguist only on
// a job assigned to them.
func (h *StreamHandler) HandleStream(w http.ResponseWriter, r *http.Request) {
	h.Stream(JobFromURL)(w, r)
}

// Stream returns a handler that sends `connected` once subscribed to the jobs
// `jobs` resolves, then `holds` per change and a keepalive comment every
// httputil.SSEKeepalive. It answers 204 while the feed is not receiving, and
// ends when the feed stops, so the editor never takes a stream that hears
// nothing for a live one. It answers 429 when the caller already holds
// httputil.MaxStreamsPerUser hold streams.
func (h *StreamHandler) Stream(jobs Jobs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobIDs, ok := jobs(w, r)
		if !ok {
			return
		}
		h.serve(w, r, jobIDs)
	}
}

func (h *StreamHandler) serve(w http.ResponseWriter, r *http.Request, jobIDs []int64) {
	release, ok := h.streams.Open(w, auth.UserIDFromContext(r.Context()))
	if !ok {
		return
	}
	defer release()
	// Subscribed before `connected`: the editor refetches on it, so a change
	// from then on is never missed.
	changes, feedDown, ok := h.feed.Subscribe(r.Context(), jobIDs)
	if !ok {
		// 204 tells EventSource to stop retrying; the editor polls and opens
		// the stream again later. Expected during a deploy or a database
		// restart, so it is not a 5xx.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	sse, ok := httputil.StartSSE(w)
	if !ok {
		return
	}
	if sse.Event("connected", struct{}{}) != nil {
		return
	}

	keepalive := time.NewTicker(httputil.SSEKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.draining:
			return
		case <-feedDown:
			return
		case _, open := <-changes:
			if !open || sse.Event("holds", struct{}{}) != nil {
				return
			}
		case <-keepalive.C:
			if sse.Comment("keepalive") != nil {
				return
			}
		}
	}
}
