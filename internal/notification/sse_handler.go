// Package notification, sse_handler.go — SSE (Server-Sent Events) endpoint
// for real-time notification delivery.
//
// Why SSE over WebSocket:
//   - Notifications flow ONE direction (server → client). WebSocket's
//     bidirectional capability is wasted overhead for this use case.
//   - SSE rides on standard HTTP, which means the same auth middleware
//     (cookie-based JWT), the same chi router, the same proxy/gateway
//     config. WebSocket needs Upgrade negotiation + ping/pong machinery.
//   - EventSource reconnects by itself after a dropped connection; a stream
//     the browser gives up on (any answer but 200) is reopened by
//     web/src/lib/event-stream.ts.
//
// Why cookie auth specifically:
//   EventSource cannot set custom headers (no Authorization: Bearer ...).
//   The HttpOnly `openloc_token` cookie set at login flows through the
//   normal `auth.Middleware` chain, so the SSE endpoint slots into the
//   existing authenticated route group with no special handling.
//
// Lifecycle:
//   1. Client opens EventSource('/api/notifications/stream')
//   2. Handler registers a subscriber on the broker (per-user channel)
//   3. Sends a `connected` event with the current unread count so the
//      bell badge can hydrate without a separate REST call
//   4. Streams the session org's events from the broker; each is JSON-encoded with
//      `event: notification\ndata: {...}\n\n` — the event type and ids, no text
//   5. Sends `: keepalive\n\n` comment every 25s to defeat idle proxy
//      timeouts (heroku/nginx default ~30-60s)
//   6. Client disconnect → ctx cancel → broker drops subscriber → handler
//      returns. EventSource reconnects after the random 1–20 s `retry:` sent
//      with `connected`.

package notification

import (
	"context"
	"math/rand/v2"
	"net/http"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// UnreadCounter is the minimal queries surface SSEHandler needs — kept
// narrow so tests can pass a stub without implementing the full Querier
// (which has 200+ methods). The real db.Querier satisfies it for free.
type UnreadCounter interface {
	CountUnreadNotifications(ctx context.Context, arg db.CountUnreadNotificationsParams) (int64, error)
}

// SSEHandler exposes the streaming-notifications endpoint and depends on
// the broker (for live events) and the unread-count source (to hydrate
// the bell badge on connect).
type SSEHandler struct {
	broker  Broker
	counter UnreadCounter
	// draining is closed when the process begins shutting down.
	//
	// http.Server.Shutdown waits for every connection to become IDLE and never
	// cancels request contexts — Go's docs put the onus on the caller to notify
	// long-lived connections. This stream is open for every logged-in user at
	// all times, so without this signal Shutdown never finishes and the process
	// exits non-zero at the deadline (see cmd/server/main.go).
	//
	// A nil channel blocks forever in a select, which is the right default: a
	// test or embedder that supplies none simply never drains.
	draining <-chan struct{}
	streams  *httputil.StreamCap
}

func NewSSEHandler(broker Broker, counter UnreadCounter, draining <-chan struct{}) *SSEHandler {
	return &SSEHandler{broker: broker, counter: counter, draining: draining, streams: httputil.NewStreamCap()}
}

// HandleStream is GET /api/notifications/stream — long-lived SSE.
// Lives inside the authenticated route group so the user is already
// resolved by the time we land here (cookie-based JWT, see auth.Middleware).
// It answers 429 when the user already holds httputil.MaxStreamsPerUser
// notification streams.
func (h *SSEHandler) HandleStream(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	if userID == 0 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	release, ok := h.streams.Open(w, userID)
	if !ok {
		return
	}
	defer release()

	sse, ok := httputil.StartSSE(w)
	if !ok {
		return
	}

	// Subscribe to the broker BEFORE writing `connected`. Once the client
	// observes `connected` it assumes live delivery is active — any event
	// published between that flush and a later Subscribe would be silently
	// dropped by the broker ("no subscribers" is not an error). Events
	// published during the hydrate below park in the subscriber's buffered
	// channel and are delivered right after `connected`.
	// Subscribe returns a channel that closes when r.Context() is
	// cancelled (client disconnect) — see broker.go.
	events := h.broker.Subscribe(r.Context(), userID)

	// Hydrate the bell badge: send the current unread count immediately
	// so the FE doesn't need a separate REST call for it.
	unread, err := h.counter.CountUnreadNotifications(r.Context(), db.CountUnreadNotificationsParams{
		UserID: userID, TenantID: tenantID,
	})
	if err != nil {
		// Non-fatal — start with 0 and let the FE refresh on demand.
		logging.Warn(r.Context(), err, "unread notification count failed", "notification-sse-count-failed", logging.UserID(userID))
		unread = 0
	}
	// Every stream on a process ends at once on a deploy or when the broker
	// loses its feed. A random reconnect delay, sent with `connected`, spreads
	// the reconnects and their refetches out.
	sse.Retry(reconnectJitter())
	if err := sse.Event("connected", map[string]any{"unread": unread}); err != nil {
		// Client likely disconnected before we wrote the first byte.
		// The broker subscription is cleaned up by ctx cancellation when
		// this handler returns.
		return
	}

	keepalive := time.NewTicker(httputil.SSEKeepalive)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			// Client disconnected. Broker cleanup goroutine handles its
			// side (closes `events` chan); we just return.
			return

		case <-h.draining:
			// The process is shutting down. Close the stream so the connection
			// goes idle and http.Server.Shutdown can finish; EventSource
			// reconnects on its own, landing on a replica that is still up.
			// Without this the server waits for a stream that never ends.
			return

		case ev, ok := <-events:
			if !ok {
				// The broker ended the subscription: ctx ended, or it cannot
				// promise live delivery any more. EventSource reconnects, and the
				// `connected` event makes the bell reread the DB.
				return
			}
			// The broker routes by user, and one user can belong to several
			// orgs. The stream is scoped like the list: the session's org only.
			if ev.TenantID != tenantID {
				continue
			}
			if err := sse.Event("notification", eventToWire(ev)); err != nil {
				logging.Warn(r.Context(), err, "notification stream write failed", "notification-sse-write-failed", logging.UserID(userID))
				return
			}

		case <-keepalive.C:
			// A comment, ignored by EventSource, keeps idle-detecting
			// proxies from closing the stream.
			if err := sse.Comment("keepalive"); err != nil {
				return
			}
		}
	}
}

// reconnectJitter is how long a browser waits before it reopens an ended
// stream: 1 to 20 s, at random.
func reconnectJitter() time.Duration {
	return time.Second + rand.N(19*time.Second)
}

// eventToWire shapes the in-memory Event for the SSE wire: the event type and
// ids, never text — all a notice between processes holds. The frame only tells
// the bell to refetch the list, the source of truth.
func eventToWire(e Event) map[string]any {
	return map[string]any{
		"type":       string(e.Type),
		"job_id":     e.JobID,
		"project_id": e.ProjectID,
	}
}
