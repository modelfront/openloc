// useNotificationStream — subscribes to the SSE
// endpoint at /api/notifications/stream and invalidates the React Query
// caches that back the bell when events arrive.
//
// Design:
//   - One EventSource per browser tab. The backend's broker handles
//     multiple subscribers per user (each tab gets its own channel).
//   - On any event (`connected` or `notification`), invalidate the unread
//     count + the recent-list queries. React Query refetches lazily (only
//     if a consumer is mounted), so the cost of an over-eager invalidate
//     is tiny.
//   - On `connected`, the backend includes the live unread count — we
//     could write directly to the cache, but invalidating + letting React
//     Query refetch keeps the data flow uniform and avoids drift.
//   - BroadcastChannel: when a `notification` arrives, post a tiny
//     {type:'invalidate'} message so OTHER tabs in the same origin also
//     invalidate (without each tab needing its own active SSE event).
//     Tabs that don't have the bell mounted don't open an SSE; they still
//     get notified via the channel from any tab that does. Every server
//     sends each notification to every open stream, so a tab gets the frame
//     and the other tabs' echoes within milliseconds: invalidations that
//     arrive within INVALIDATE_MERGE_MS make one refetch, which also covers a
//     burst of notifications.
//   - The stream itself (cookie auth, reconnect, hidden tabs on HTTP/1.1)
//     is lib/event-stream.ts.

import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { queryKeys } from '../lib/query-keys'
import { openEventStream } from '../lib/event-stream'

const BROADCAST_CHANNEL_NAME = 'openloc-notifications'
const SSE_PATH = '/notifications/stream'
export const INVALIDATE_MERGE_MS = 250

/**
 * Subscribe to live notifications via SSE + cross-tab BroadcastChannel.
 * Mount once at the bell (or app root). Tears down on unmount.
 *
 * Returns nothing — invalidations flow through React Query's cache and
 * any consumer that holds the count or list query gets refetched.
 */
export function useNotificationStream(): void {
  const qc = useQueryClient()

  useEffect(() => {
    // BroadcastChannel for cross-tab invalidate signals. Tabs without the
    // bell mounted won't open an SSE; they still receive invalidate
    // signals from any tab that does.
    let channel: BroadcastChannel | null = null
    try {
      channel = new BroadcastChannel(BROADCAST_CHANNEL_NAME)
    } catch {
      // BroadcastChannel not supported (older Safari < 15.4). Skip the
      // cross-tab fanout; this tab still gets SSE.
      channel = null
    }

    // The refetch runs at the end of the window, after every invalidation
    // it merges.
    let mergeTimer: ReturnType<typeof setTimeout> | null = null
    const invalidate = () => {
      if (mergeTimer != null) return
      mergeTimer = setTimeout(() => {
        mergeTimer = null
        qc.invalidateQueries({ queryKey: queryKeys.notifications.count })
        qc.invalidateQueries({ queryKey: queryKeys.notifications.all })
      }, INVALIDATE_MERGE_MS)
    }

    // Listener for messages from OTHER tabs.
    const onChannelMsg = (e: MessageEvent) => {
      if (e.data?.type === 'invalidate') {
        invalidate()
      }
    }
    channel?.addEventListener('message', onChannelMsg)

    // A new notification is echoed to other tabs so their bell stays in sync
    // without each needing its own SSE connection. `connected` is not: every
    // tab with a stream rereads on its own, and when a server ends all its
    // streams at once the echoes would make every tab refetch once per tab.
    const onNotification = () => {
      invalidate()
      try {
        channel?.postMessage({ type: 'invalidate' })
      } catch {
        // BroadcastChannel can be closed mid-event in race conditions —
        // swallow and move on; the SSE invalidate already happened.
      }
    }
    const closeStream = openEventStream(SSE_PATH, {
      connected: invalidate,
      notification: onNotification,
    })

    return () => {
      if (mergeTimer != null) clearTimeout(mergeTimer)
      closeStream()
      channel?.removeEventListener('message', onChannelMsg)
      channel?.close()
    }
  }, [qc])
}
