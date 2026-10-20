// useNotificationStream — contract pins.
//
// What we pin (mocked EventSource because jsdom doesn't ship one):
//   1. Hook opens an EventSource at /api/notifications/stream on mount.
//   2. When the EventSource emits a `notification` event, the count
//      query is invalidated (so the bell badge refetches).
//   3. When the EventSource emits `connected`, same invalidation.
//   4. Hook closes the EventSource on unmount (no leak).
//   5. Cross-tab: posting an `{type: invalidate}` message to the
//      BroadcastChannel ALSO invalidates the count (so other tabs sync
//      without each opening their own SSE).
//
// Anti-tautology: dropping the `notification` handler the hook passes to
// openEventStream makes test #2 fail: the count is never invalidated.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { INVALIDATE_MERGE_MS, useNotificationStream } from './useNotificationStream'
import { queryKeys } from '../lib/query-keys'
import type { ReactNode } from 'react'
import { MockEventSource, installMockEventSource, uninstallMockEventSource } from '../test/mock-event-source'

beforeEach(installMockEventSource)
afterEach(uninstallMockEventSource)

// How many times the bell's count query was invalidated.
function countInvalidations(spy: { mock: { calls: unknown[][] } }): number {
  return spy.mock.calls.filter(args =>
    JSON.stringify((args[0] as { queryKey?: unknown })?.queryKey) === JSON.stringify(queryKeys.notifications.count),
  ).length
}

function wrap(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('useNotificationStream', () => {
  it('opens an EventSource at /api/notifications/stream on mount', () => {
    const qc = new QueryClient()
    renderHook(() => useNotificationStream(), { wrapper: wrap(qc) })
    expect(MockEventSource.instances).toHaveLength(1)
    expect(MockEventSource.instances[0].url).toMatch(/\/notifications\/stream$/)
    expect(MockEventSource.instances[0].withCredentials).toBe(true)
  })

  it('invalidates the count query when a notification event arrives', async () => {
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    renderHook(() => useNotificationStream(), { wrapper: wrap(qc) })

    MockEventSource.instances[0].emit('notification', { job_id: 7 })

    await waitFor(() => expect(countInvalidations(spy)).toBeGreaterThan(0))
  })

  it('invalidates the count on connected too (covers initial hydrate)', async () => {
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    renderHook(() => useNotificationStream(), { wrapper: wrap(qc) })

    MockEventSource.instances[0].emit('connected', { unread: 3 })

    await waitFor(() => expect(countInvalidations(spy)).toBeGreaterThan(0))
  })

  it('closes the EventSource on unmount', () => {
    const qc = new QueryClient()
    const { unmount } = renderHook(() => useNotificationStream(), { wrapper: wrap(qc) })
    const es = MockEventSource.instances[0]
    expect(es.closed).toBe(false)
    unmount()
    expect(es.closed).toBe(true)
  })

  // Cross-tab sync: when ANOTHER tab posts an `invalidate` message via
  // BroadcastChannel, this tab also invalidates — so tabs without an
  // open SSE (or that just landed) stay in lockstep without each
  // needing a server round-trip.
  it('invalidates the count when another tab posts via BroadcastChannel', async () => {
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    renderHook(() => useNotificationStream(), { wrapper: wrap(qc) })

    // Open a SECOND BroadcastChannel as if from another tab and post.
    const otherTab = new BroadcastChannel('openloc-notifications')
    otherTab.postMessage({ type: 'invalidate' })

    // BroadcastChannel delivers asynchronously.
    await waitFor(() => expect(countInvalidations(spy)).toBeGreaterThan(0))
    otherTab.close()
  })

  // Every server sends each notification to every open stream, so a tab gets
  // the frame and the other tabs' echoes within milliseconds, and a bulk
  // action sends many notifications at once. One refetch covers all of them
  // (echoes take the same path); the next notification after the window
  // refetches again.
  it('merges a burst of notifications into one refetch', async () => {
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    renderHook(() => useNotificationStream(), { wrapper: wrap(qc) })
    const es = MockEventSource.instances[0]

    es.emit('notification', { job_id: 1 })
    es.emit('notification', { job_id: 2 })
    es.emit('notification', { job_id: 3 })
    await waitFor(() => expect(countInvalidations(spy)).toBe(1))
    // A negative check: nothing else is due within the next window.
    await new Promise(resolve => setTimeout(resolve, 2 * INVALIDATE_MERGE_MS))
    expect(countInvalidations(spy)).toBe(1)

    es.emit('notification', { job_id: 4 })
    await waitFor(() => expect(countInvalidations(spy)).toBe(2))
  })

  // Cross-tab fanout: when this tab's SSE fires an event, the hook
  // posts to BroadcastChannel so other tabs invalidate too. We verify
  // by listening on a second channel and checking we receive the
  // post-event message.
  it('posts an invalidate message to BroadcastChannel after an SSE event (so other tabs sync)', async () => {
    const qc = new QueryClient()
    const otherTab = new BroadcastChannel('openloc-notifications')
    const received: unknown[] = []
    otherTab.addEventListener('message', e => received.push(e.data))

    renderHook(() => useNotificationStream(), { wrapper: wrap(qc) })
    MockEventSource.instances[0].emit('notification', { job_id: 7 })

    await waitFor(() => {
      const invalidateMsg = received.find(d => (d as { type?: string })?.type === 'invalidate')
      expect(invalidateMsg).toBeDefined()
    })
    otherTab.close()
  })

  // Every tab rereads the bell on its own `connected`. After a server ends
  // every stream at once, each tab reconnecting would otherwise make every
  // other tab of the user refetch as well.
  it('does not echo connected to other tabs', async () => {
    const qc = new QueryClient()
    const otherTab = new BroadcastChannel('openloc-notifications')
    const received: unknown[] = []
    otherTab.addEventListener('message', e => received.push(e.data))

    renderHook(() => useNotificationStream(), { wrapper: wrap(qc) })
    MockEventSource.instances[0].emit('connected', { unread: 3 })
    MockEventSource.instances[0].emit('notification', { job_id: 7 })

    await waitFor(() => expect(received).toHaveLength(1))
    expect(received[0]).toMatchObject({ type: 'invalidate' })
    otherTab.close()
  })
})

// Over HTTP/1.1 a browser allows about 6 connections per host, shared by every
// tab, and each open stream holds one. Hidden tabs give theirs back there.
describe('useNotificationStream connection sharing', () => {
  let visibility: DocumentVisibilityState = 'visible'

  function setProtocol(nextHopProtocol: string) {
    vi.spyOn(performance, 'getEntriesByType').mockReturnValue(
      [{ nextHopProtocol }] as unknown as PerformanceEntryList,
    )
  }
  function setVisibility(v: DocumentVisibilityState) {
    visibility = v
    document.dispatchEvent(new Event('visibilitychange'))
  }
  const openStreams = () => MockEventSource.instances.filter(es => !es.closed)

  beforeEach(() => {
    visibility = 'visible'
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility)
  })
  afterEach(() => vi.restoreAllMocks())

  it('closes the stream while the tab is hidden on HTTP/1.1 and reopens it on return', () => {
    setProtocol('http/1.1')
    renderHook(() => useNotificationStream(), { wrapper: wrap(new QueryClient()) })
    expect(openStreams()).toHaveLength(1)

    setVisibility('hidden')
    expect(openStreams()).toHaveLength(0)

    setVisibility('visible')
    expect(openStreams()).toHaveLength(1)
    expect(MockEventSource.instances).toHaveLength(2)
  })

  it('does not open a stream in a tab that starts hidden on HTTP/1.1', () => {
    setProtocol('http/1.1')
    visibility = 'hidden'
    renderHook(() => useNotificationStream(), { wrapper: wrap(new QueryClient()) })
    expect(MockEventSource.instances).toHaveLength(0)
  })

  it('treats an unknown protocol like HTTP/1.1', () => {
    setProtocol('')
    renderHook(() => useNotificationStream(), { wrapper: wrap(new QueryClient()) })
    setVisibility('hidden')
    expect(openStreams()).toHaveLength(0)
  })

  it('keeps the stream open in a hidden tab on HTTP/2', () => {
    setProtocol('h2')
    renderHook(() => useNotificationStream(), { wrapper: wrap(new QueryClient()) })
    setVisibility('hidden')
    expect(openStreams()).toHaveLength(1)
  })

  it('stops listening for visibility after unmount', () => {
    setProtocol('http/1.1')
    const { unmount } = renderHook(() => useNotificationStream(), { wrapper: wrap(new QueryClient()) })
    unmount()
    setVisibility('hidden')
    setVisibility('visible')
    expect(openStreams()).toHaveLength(0)
  })
})
