import { afterEach, describe, it, expect, vi } from 'vitest'
import { trackPropagation, awaitPropagation, reconcilePropagationBaseline } from './propagation-guard'

describe('propagation-guard', () => {
  it('returns an empty map when nothing is in flight (zero cost for normal saves)', async () => {
    const result = await awaitPropagation(1, 'no propagation here')
    expect(result.size).toBe(0)
  })

  it('a sibling awaiting the same source gets the propagated updated_at', async () => {
    let resolve!: (m: Map<number, string>) => void
    trackPropagation(7, 'shared source', new Promise((r) => { resolve = r }))

    const pending = awaitPropagation(7, 'shared source')
    resolve(new Map([[42, '2026-06-01T10:00:00Z']]))

    const result = await pending
    expect(result.get(42)).toBe('2026-06-01T10:00:00Z')
  })

  it('isolates by jobId and source — an unrelated key is unaffected', async () => {
    trackPropagation(7, 'source A', Promise.resolve(new Map([[1, 'x']])))
    expect((await awaitPropagation(7, 'source B')).size).toBe(0)
    expect((await awaitPropagation(8, 'source A')).size).toBe(0)
  })

  it('cleans up after settle so a later save does not await a stale promise', async () => {
    trackPropagation(9, 'transient', Promise.resolve(new Map([[1, 'y']])))
    // First await drains the settled propagation.
    expect((await awaitPropagation(9, 'transient')).get(1)).toBe('y')
    // After settle the entry is gone — a fresh await is empty, not the old map.
    await Promise.resolve()
    expect((await awaitPropagation(9, 'transient')).size).toBe(0)
  })

  it('a rejected propagation resolves to empty — never fails the sibling save', async () => {
    trackPropagation(11, 'fails', Promise.reject(new Error('network')))
    const result = await awaitPropagation(11, 'fails')
    expect(result.size).toBe(0)
  })

  it('reconcilePropagationBaseline adopts the post-propagation updated_at for the row', async () => {
    trackPropagation(20, 'src', Promise.resolve(new Map([[99, '2026-06-02T09:00:00Z']])))
    const baselineRef = { current: 'stale-updated-at' as string | null }
    await reconcilePropagationBaseline(20, 'src', 99, baselineRef)
    expect(baselineRef.current).toBe('2026-06-02T09:00:00Z')
  })

  it('reconcilePropagationBaseline leaves the baseline untouched when nothing is in flight', async () => {
    const baselineRef = { current: 'mine' as string | null }
    await reconcilePropagationBaseline(22, 'no-propagation', 99, baselineRef)
    expect(baselineRef.current).toBe('mine')
  })

  it('reconcilePropagationBaseline leaves the baseline untouched for a row not in the sibling set', async () => {
    trackPropagation(21, 'src', Promise.resolve(new Map([[1, 'other-row']])))
    const baselineRef = { current: 'mine' as string | null }
    await reconcilePropagationBaseline(21, 'src', 99, baselineRef) // id 99 not propagated
    expect(baselineRef.current).toBe('mine')
  })

  // The row was activated while the propagation was in flight, so it holds the
  // version from before it; typing started before the response, so the row did
  // not follow the newer version. Its save comes after the propagation settled
  // and must still not 409 against the user's own propagation.
  it('reconciles a save that comes after the propagation settled, for a row with an older baseline', async () => {
    trackPropagation(30, 'dup', Promise.resolve(new Map([[401, '2026-10-19T15:09:57.000Z']])))
    await awaitPropagation(30, 'dup')
    await Promise.resolve()
    expect((await awaitPropagation(30, 'dup')).size).toBe(0)
    const baselineRef = { current: '2026-10-19T15:09:56.300Z' as string | null }
    await reconcilePropagationBaseline(30, 'dup', 401, baselineRef)
    expect(baselineRef.current).toBe('2026-10-19T15:09:57.000Z')
  })

  it('a settled propagation is adopted once, and never moves a newer baseline back', async () => {
    trackPropagation(31, 'dup', Promise.resolve(new Map([[7, '2026-10-19T10:00:00.000Z']])))
    await awaitPropagation(31, 'dup')
    await Promise.resolve()
    const newer = { current: '2026-10-19T11:00:00.000Z' as string | null }
    await reconcilePropagationBaseline(31, 'dup', 7, newer)
    expect(newer.current).toBe('2026-10-19T11:00:00.000Z')

    trackPropagation(32, 'dup', Promise.resolve(new Map([[8, '2026-10-19T10:00:00.000Z']])))
    await awaitPropagation(32, 'dup')
    await Promise.resolve()
    const first = { current: '2026-10-19T09:00:00.000Z' as string | null }
    await reconcilePropagationBaseline(32, 'dup', 8, first)
    expect(first.current).toBe('2026-10-19T10:00:00.000Z')
    const again = { current: '2026-10-19T09:00:00.000Z' as string | null }
    await reconcilePropagationBaseline(32, 'dup', 8, again)
    expect(again.current).toBe('2026-10-19T09:00:00.000Z')
  })

  it('an adoption while in flight also uses up the settled version: a later save does not take it again', async () => {
    let resolve!: (m: Map<number, string>) => void
    trackPropagation(33, 'dup', new Promise((r) => { resolve = r }))
    const inFlight = { current: '2026-10-19T09:00:00.000Z' as string | null }
    const pending = reconcilePropagationBaseline(33, 'dup', 5, inFlight)
    resolve(new Map([[5, '2026-10-19T10:00:00.000Z']]))
    await pending
    expect(inFlight.current).toBe('2026-10-19T10:00:00.000Z')
    await Promise.resolve()

    const later = { current: '2026-10-19T09:30:00.000Z' as string | null }
    await reconcilePropagationBaseline(33, 'dup', 5, later)
    expect(later.current).toBe('2026-10-19T09:30:00.000Z')
  })

  describe('settled versions expire', () => {
    afterEach(() => { vi.useRealTimers() })

    it('a settled version older than a minute is not adopted', async () => {
      vi.useFakeTimers({ toFake: ['Date'] })
      vi.setSystemTime(new Date('2026-10-19T12:00:00Z'))
      trackPropagation(34, 'dup', Promise.resolve(new Map([[6, '2026-10-19T12:00:00.000Z']])))
      await awaitPropagation(34, 'dup')
      await Promise.resolve()
      vi.setSystemTime(new Date('2026-10-19T12:01:01Z'))
      const baselineRef = { current: '2026-10-19T11:00:00.000Z' as string | null }
      await reconcilePropagationBaseline(34, 'dup', 6, baselineRef)
      expect(baselineRef.current).toBe('2026-10-19T11:00:00.000Z')
    })

    it('a settled version within the minute is still adopted', async () => {
      vi.useFakeTimers({ toFake: ['Date'] })
      vi.setSystemTime(new Date('2026-10-19T12:00:00Z'))
      trackPropagation(35, 'dup', Promise.resolve(new Map([[6, '2026-10-19T12:00:00.000Z']])))
      await awaitPropagation(35, 'dup')
      await Promise.resolve()
      vi.setSystemTime(new Date('2026-10-19T12:00:59Z'))
      const baselineRef = { current: '2026-10-19T11:00:00.000Z' as string | null }
      await reconcilePropagationBaseline(35, 'dup', 6, baselineRef)
      expect(baselineRef.current).toBe('2026-10-19T12:00:00.000Z')
    })
  })
})
