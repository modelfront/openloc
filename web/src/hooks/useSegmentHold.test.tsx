import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

const mockAcquire = vi.fn()
const mockRelease = vi.fn()
const mockList = vi.fn()
vi.mock('../api/holds', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/holds')>()
  return {
    ...actual,
    acquireHold: (...a: unknown[]) => mockAcquire(...a),
    releaseHold: (...a: unknown[]) => mockRelease(...a),
    listHolds: (...a: unknown[]) => mockList(...a),
  }
})

import { ApiError } from '../api/request'
import { HOLD_POLL_MS, HOLD_RENEW_MS, useSegmentHold } from './useSegmentHold'
import type { SegmentHold } from '../api/holds'
import { MockEventSource, installMockEventSource, uninstallMockEventSource } from '../test/mock-event-source'

const ME = 1
const ANA = 2
const row = (id: number) => ({
  id, segment_id: id, target_language: 'de', status: 'needs_review', tm_match_percent: null,
  predict_outcome: { predict_outcome: '', valid: false }, active_text: 'x', updated_at: '2026-10-01T10:00:00Z',
})
const heldByAna = () => new ApiError(409, 'Ana is editing this segment.', {
  error: { code: 'segment_held', message: 'Ana is editing this segment.' }, holder: { id: ANA, name: 'Ana' },
})
const anaHolds = (id: number): SegmentHold =>
  ({ translation_id: id, user_id: ANA, holder_name: 'Ana', acquired_at: '', expires_at: '' })

function setup(initial: { translationId: number | null; canHold?: boolean }) {
  const onIdle = vi.fn()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  const view = renderHook(
    (p: { translationId: number | null; canHold?: boolean }) => useSegmentHold({
      jobId: 7, translationId: p.translationId, canHold: p.canHold ?? true, currentUserId: ME, onIdle,
    }),
    { wrapper, initialProps: initial },
  )
  return { ...view, onIdle }
}

// React Query delivers results on a zero-delay timer, so let a few run.
const flush = () => act(async () => { for (let i = 0; i < 3; i++) await vi.advanceTimersByTimeAsync(1) })

beforeEach(() => {
  vi.useFakeTimers()
  mockAcquire.mockReset().mockImplementation((_j: number, id: number) =>
    Promise.resolve({ translation_id: id, expires_at: '', translation: row(id) }))
  mockRelease.mockReset().mockResolvedValue(undefined)
  mockList.mockReset().mockResolvedValue([])
})
afterEach(() => { vi.useRealTimers() })

describe('useSegmentHold', () => {
  it('holds the active segment, moves with it, and releases on close', async () => {
    const { rerender } = setup({ translationId: 11 })
    await flush()
    expect(mockAcquire).toHaveBeenLastCalledWith(7, 11, expect.any(String))
    const tab = mockAcquire.mock.calls[0][2]

    rerender({ translationId: 12 })
    await flush()
    expect(mockAcquire).toHaveBeenLastCalledWith(7, 12, tab)
    expect(mockRelease).not.toHaveBeenCalled() // the move releases segment 11 on the server

    rerender({ translationId: null })
    await flush()
    expect(mockRelease).toHaveBeenCalledWith(7, 12, tab)
  })

  it('a renewal that fails on the network keeps the hold known, so closing the row releases it', async () => {
    const { rerender } = setup({ translationId: 11 })
    await flush()
    mockAcquire.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await act(async () => { await vi.advanceTimersByTimeAsync(HOLD_RENEW_MS) })
    rerender({ translationId: null })
    await flush()
    expect(mockRelease).toHaveBeenCalledWith(7, 11, expect.any(String))
  })

  it('retries a failed acquire within seconds, not at the next renewal', async () => {
    mockAcquire.mockRejectedValueOnce(new ApiError(502, 'Bad gateway'))
    setup({ translationId: 11 })
    await flush()
    expect(mockAcquire).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000) })
    expect(mockAcquire).toHaveBeenCalledTimes(2)
  })

  it('does not retry an acquire the server refused', async () => {
    mockAcquire.mockRejectedValueOnce(new ApiError(403, 'task completed'))
    setup({ translationId: 11 })
    await flush()
    await act(async () => { await vi.advanceTimersByTimeAsync(HOLD_RENEW_MS - 1_000) })
    expect(mockAcquire).toHaveBeenCalledTimes(1)
  })

  it('takes no hold on a segment the user cannot edit', async () => {
    setup({ translationId: 11, canHold: false })
    await flush()
    expect(mockAcquire).not.toHaveBeenCalled()
  })

  it('renews while the segment stays open', async () => {
    setup({ translationId: 11 })
    await flush()
    expect(mockAcquire).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(HOLD_RENEW_MS) })
    expect(mockAcquire).toHaveBeenCalledTimes(2)
  })

  it('sends one request at a time, then only the latest wanted segment', async () => {
    let finish: () => void = () => {}
    mockAcquire.mockImplementationOnce((_j: number, id: number) =>
      new Promise(r => { finish = () => r({ translation_id: id, expires_at: '', translation: row(id) }) }))
    const { rerender } = setup({ translationId: 11 })
    await flush()
    rerender({ translationId: 12 })
    rerender({ translationId: 13 })
    await flush()
    expect(mockAcquire).toHaveBeenCalledTimes(1)
    await act(async () => { finish(); await vi.advanceTimersByTimeAsync(0) })
    expect(mockAcquire).toHaveBeenCalledTimes(2)
    expect(mockAcquire).toHaveBeenLastCalledWith(7, 13, expect.any(String))
  })

  it('a segment held by someone else is reported, then retaken when they leave', async () => {
    mockAcquire.mockRejectedValueOnce(heldByAna())
    mockList.mockResolvedValueOnce([anaHolds(11)])
    const { result } = setup({ translationId: 11 })
    await flush()
    expect(result.current.heldByOthers.get(11)).toBe('Ana')

    // The poll shows Ana gone: the hook takes the segment.
    await act(async () => { await vi.advanceTimersByTimeAsync(HOLD_POLL_MS) })
    await flush()
    expect(mockAcquire).toHaveBeenCalledTimes(2)
    expect(result.current.heldByOthers.has(11)).toBe(false)
  })

  it("shows other users' holds from the poll, not the user's own", async () => {
    mockList.mockResolvedValue([anaHolds(21), { ...anaHolds(22), user_id: ME, holder_name: 'Me' }])
    const { result } = setup({ translationId: null })
    await flush()
    expect([...result.current.heldByOthers.entries()]).toEqual([[21, 'Ana']])
  })

  it('releases with keepalive when the page is hidden for good', async () => {
    setup({ translationId: 11 })
    await flush()
    act(() => { window.dispatchEvent(new Event('pagehide')) })
    expect(mockRelease).toHaveBeenCalledWith(7, 11, expect.any(String), { keepalive: true })
  })

  it('closes the segment after 5 minutes without input', async () => {
    const fiveMinutes = 5 * 60_000
    const { onIdle } = setup({ translationId: 11 })
    await flush()
    // Input just before the limit starts it again.
    await act(async () => { await vi.advanceTimersByTimeAsync(fiveMinutes - HOLD_RENEW_MS) })
    act(() => { window.dispatchEvent(new KeyboardEvent('keydown')) })
    await act(async () => { await vi.advanceTimersByTimeAsync(fiveMinutes - HOLD_RENEW_MS) })
    expect(onIdle).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(HOLD_RENEW_MS * 2) })
    expect(onIdle).toHaveBeenCalled()
  })

  it('releases on unmount', async () => {
    const { unmount } = setup({ translationId: 11 })
    await flush()
    unmount()
    expect(mockRelease).toHaveBeenCalledWith(7, 11, expect.any(String))
  })

  it('works without crypto.randomUUID (plain-HTTP deployments)', async () => {
    // Shadow the secure-context-only method; deleting the own property
    // afterwards brings the prototype's back.
    Object.defineProperty(crypto, 'randomUUID', { value: undefined, configurable: true, writable: true })
    try {
      setup({ translationId: 11 })
      await flush()
      expect(mockAcquire).toHaveBeenCalledWith(7, 11, expect.stringMatching(/^[0-9a-f]{32}$/))
    } finally {
      delete (crypto as { randomUUID?: unknown }).randomUUID
    }
  })

  it('a refusal while the poll has not changed does not retry', async () => {
    mockAcquire.mockReset().mockRejectedValue(heldByAna())
    mockList.mockResolvedValue([])
    setup({ translationId: 11 })
    await flush()
    await flush()
    const after = mockAcquire.mock.calls.length
    expect(after).toBeLessThanOrEqual(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(HOLD_POLL_MS - 100) })
    expect(mockAcquire.mock.calls.length).toBe(after)
  })

  it('an acquire that resolves after unmount is released at once', async () => {
    let finish: () => void = () => {}
    mockAcquire.mockImplementationOnce((_j: number, id: number) =>
      new Promise(r => { finish = () => r({ translation_id: id, expires_at: '', translation: row(id) }) }))
    const { unmount } = setup({ translationId: 11 })
    await flush()
    unmount()
    expect(mockRelease).not.toHaveBeenCalled()
    await act(async () => { finish(); await vi.advanceTimersByTimeAsync(1) })
    expect(mockRelease).toHaveBeenCalledWith(7, 11, expect.any(String), { keepalive: true })
    await act(async () => { await vi.advanceTimersByTimeAsync(HOLD_RENEW_MS * 2) })
    expect(mockAcquire).toHaveBeenCalledTimes(1)
  })

  it('names the holder from the refusal while no poll shows them', async () => {
    mockAcquire.mockReset().mockRejectedValue(heldByAna())
    mockList.mockResolvedValue([])
    const { result } = setup({ translationId: 11 })
    await flush()
    expect(result.current.heldByOthers.get(11)).toBe('Ana')
  })

  it("says 'Someone' when the refusal names nobody", async () => {
    mockAcquire.mockReset().mockRejectedValue(new ApiError(409, 'held', { error: { code: 'segment_held', message: 'held' } }))
    mockList.mockResolvedValue([])
    const { result } = setup({ translationId: 11 })
    await flush()
    expect(result.current.heldByOthers.get(11)).toBe('Someone')
  })
})

// The editor refetches holds the moment the server announces a change, and
// polls only as a safety net while the stream is live.
describe('useSegmentHold with the live hold stream', () => {
  // The editor window has focus; jsdom reports none, and no HTTP/2.
  beforeEach(() => {
    installMockEventSource()
    vi.spyOn(document, 'hasFocus').mockReturnValue(true)
  })
  afterEach(() => {
    vi.restoreAllMocks()
    uninstallMockEventSource()
  })
  const stream = () => MockEventSource.instances[0]
  const polls = () => mockList.mock.calls.length
  const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms) })

  it('polls every 5 s without the stream and every 30 s while it is live', async () => {
    const { result } = setup({ translationId: null })
    await flush()
    expect(result.current.streamLive).toBe(false)
    const start = polls()
    await advance(5_000)
    expect(polls()).toBe(start + 1)

    act(() => stream().emit('connected', {}))
    await flush()
    expect(result.current.streamLive).toBe(true)
    const live = polls()
    await advance(25_000)
    expect(polls()).toBe(live)
    await advance(5_000)
    expect(polls()).toBe(live + 1)

    act(() => stream().fail())
    await flush()
    expect(result.current.streamLive).toBe(false)
    const down = polls()
    await advance(5_000)
    expect(polls()).toBe(down + 1)
  })

  it('shows a segment as held the moment the server announces it', async () => {
    const { result } = setup({ translationId: null })
    await flush()
    act(() => stream().emit('connected', {}))
    await flush()
    expect(result.current.heldByOthers.size).toBe(0)

    mockList.mockResolvedValue([anaHolds(21)])
    act(() => stream().emit('holds', {}))
    await flush()
    expect(result.current.heldByOthers.get(21)).toBe('Ana')
  })

  it('takes a blocked segment the moment its holder leaves', async () => {
    mockAcquire.mockRejectedValueOnce(heldByAna())
    mockList.mockResolvedValue([anaHolds(11)])
    const { result } = setup({ translationId: 11 })
    await flush()
    act(() => stream().emit('connected', {}))
    await flush()
    expect(result.current.heldByOthers.get(11)).toBe('Ana')

    mockList.mockResolvedValue([])
    act(() => stream().emit('holds', {}))
    await flush()
    expect(mockAcquire).toHaveBeenCalledTimes(2)
    expect(result.current.heldByOthers.has(11)).toBe(false)
  })
})
