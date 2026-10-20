// useTMMatches is shared by the sidebar's TM section (which renders the hits)
// and CATEditor (which diffs the segment's source against the best one). Both
// depend on `stale` being honest: a stale hit inserted is the previous
// segment's translation, and a stale hit diffed against is two unrelated
// sentences compared.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useTMMatches } from './useTMMatches'
import type { TMMatch } from '../api'

const mockGetTMMatches = vi.fn()
vi.mock('../api', () => ({
  getTMMatches: (...args: unknown[]) => mockGetTMMatches(...args),
}))

const hit = (source: string): TMMatch => ({
  score: 0.9,
  percent: 90,
  source_text: source,
  target_text: 'irrelevant',
  source_rich_text: null,
  target_rich_text: null,
  tm_name: 'Demo TM',
  tm_id: 1,
})

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
}

beforeEach(() => {
  mockGetTMMatches.mockReset()
  vi.useRealTimers()
})

describe('useTMMatches', () => {
  it('fetches after the debounce and reports the hits as fresh', async () => {
    mockGetTMMatches.mockResolvedValue([hit('Reset your admin password.')])
    const { result } = renderHook(() => useTMMatches(7, 'Reset your password.', null), { wrapper: wrapper() })

    // The debounce seeds with its initial value, so mounting on a segment
    // fetches straight away — the delay only applies to later changes.
    expect(result.current.matches).toEqual([])

    await waitFor(() => expect(result.current.matches.length).toBe(1))
    expect(mockGetTMMatches).toHaveBeenCalledWith(7, 'Reset your password.', null)
    expect(result.current.stale).toBe(false)
  })

  it('reports stale through the debounce and while the new source request is in flight', async () => {
    let resolveSecond: (hits: TMMatch[]) => void = () => {}
    mockGetTMMatches
      .mockResolvedValueOnce([hit('Reset your admin password.')])
      .mockImplementationOnce(() => new Promise<TMMatch[]>(res => { resolveSecond = res }))
    const { result, rerender } = renderHook(
      ({ src }: { src: string }) => useTMMatches(7, src, null),
      { wrapper: wrapper(), initialProps: { src: 'Reset your password.' } },
    )
    await waitFor(() => expect(result.current.matches.length).toBe(1))
    expect(result.current.stale).toBe(false)

    rerender({ src: 'Something else entirely.' })
    // Inside the debounce: the hits on screen still belong to the previous segment.
    expect(result.current.stale).toBe(true)
    expect(result.current.usable).toEqual([])

    // Debounce elapsed and the request is pending: the previous segment's rows
    // are still served, so they must stay inert.
    await waitFor(() => expect(mockGetTMMatches).toHaveBeenLastCalledWith(7, 'Something else entirely.', null))
    expect(result.current.stale).toBe(true)
    expect(result.current.usable).toEqual([])
    expect(result.current.matches.map(m => m.source_text)).toEqual(['Reset your admin password.'])

    act(() => resolveSecond([hit('Something else entirely, fresh.')]))
    await waitFor(() => expect(result.current.stale).toBe(false))
    expect(result.current.usable.map(m => m.source_text)).toEqual(['Something else entirely, fresh.'])
  })

  // A gated query never resolves, and keepPreviousData
  // happily serves the last segment's rows into that pending state forever.
  it('blanks the hits for a source under the lookup gate', async () => {
    mockGetTMMatches.mockResolvedValue([hit('Reset your admin password.')])
    const { result, rerender } = renderHook(
      ({ src }: { src: string }) => useTMMatches(7, src, null),
      { wrapper: wrapper(), initialProps: { src: 'Reset your password.' } },
    )
    await waitFor(() => expect(result.current.matches.length).toBe(1))

    rerender({ src: '1' })
    await waitFor(() => expect(result.current.matches).toEqual([]))
    expect(mockGetTMMatches).toHaveBeenCalledTimes(1)
  })

  it('blanks the hits when the segment is deactivated (empty source)', async () => {
    mockGetTMMatches.mockResolvedValue([hit('Reset your admin password.')])
    const { result, rerender } = renderHook(
      ({ src }: { src: string }) => useTMMatches(7, src, null),
      { wrapper: wrapper(), initialProps: { src: 'Reset your password.' } },
    )
    await waitFor(() => expect(result.current.matches.length).toBe(1))

    rerender({ src: '' })
    await waitFor(() => expect(result.current.matches).toEqual([]))
  })
})

// `usable` is the "may I act on this?" list — it backs CATEditor's Mod+1..5
// insert ref and the source cell's diff target. Acting on a stale hit inserts
// the previous segment's translation, or diffs against its sentence.
describe('useTMMatches usable list', () => {
  it('carries the hits while they are fresh', async () => {
    mockGetTMMatches.mockResolvedValue([hit('Reset your admin password.')])
    const { result } = renderHook(() => useTMMatches(7, 'Reset your password.', null), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.usable.length).toBe(1))
    expect(result.current.usable).toEqual(result.current.matches)
  })

  it('empties while stale, even though the panel still lists the hits', async () => {
    mockGetTMMatches.mockResolvedValue([hit('Reset your admin password.')])
    const { result, rerender } = renderHook(
      ({ src }: { src: string }) => useTMMatches(7, src, null),
      { wrapper: wrapper(), initialProps: { src: 'Reset your password.' } },
    )
    await waitFor(() => expect(result.current.usable.length).toBe(1))

    rerender({ src: 'An entirely different source segment.' })
    expect(result.current.usable).toEqual([])
    // Still displayed — the panel dims them rather than flashing to empty.
    expect(result.current.matches.length).toBe(1)

    await waitFor(() => expect(result.current.usable.length).toBe(1))
  })
})

// Two segments can share a source but not their neighbours, so the lookup is
// per segment: an in-context 101% (and its insert token) belongs to one.
describe('useTMMatches per segment', () => {
  it('looks up again for another segment with the same source, stale until it lands', async () => {
    let resolveSecond: (hits: TMMatch[]) => void = () => {}
    mockGetTMMatches
      .mockResolvedValueOnce([{ ...hit('Reset your password.'), percent: 101 }])
      .mockImplementationOnce(() => new Promise<TMMatch[]>(res => { resolveSecond = res }))
    const { result, rerender } = renderHook(
      ({ id }: { id: number }) => useTMMatches(7, 'Reset your password.', id),
      { wrapper: wrapper(), initialProps: { id: 1 } },
    )
    await waitFor(() => expect(result.current.usable.map(m => m.percent)).toEqual([101]))
    expect(mockGetTMMatches).toHaveBeenLastCalledWith(7, 'Reset your password.', 1)

    rerender({ id: 2 })
    // Segment 1's 101% must not be acted on from segment 2.
    expect(result.current.usable).toEqual([])
    await waitFor(() => expect(mockGetTMMatches).toHaveBeenLastCalledWith(7, 'Reset your password.', 2))
    expect(result.current.stale).toBe(true)

    act(() => resolveSecond([{ ...hit('Reset your password.'), percent: 100 }]))
    await waitFor(() => expect(result.current.usable.map(m => m.percent)).toEqual([100]))
  })
})
