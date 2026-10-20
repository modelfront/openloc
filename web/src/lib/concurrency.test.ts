import { describe, it, expect } from 'vitest'
import { mapWithConcurrency } from './concurrency'

describe('mapWithConcurrency', () => {
  it('runs every item and preserves input order in results', async () => {
    const items = [1, 2, 3, 4, 5]
    const seen: number[] = []
    const results = await mapWithConcurrency(items, 2, async (n) => {
      seen.push(n)
    })
    expect(seen.sort()).toEqual([1, 2, 3, 4, 5])
    expect(results.map(r => r.item)).toEqual(items)
    expect(results.every(r => r.ok)).toBe(true)
  })

  it('never exceeds the concurrency limit of in-flight workers', async () => {
    let inFlight = 0
    let peak = 0
    const release: Array<() => void> = []
    const items = Array.from({ length: 10 }, (_, i) => i)

    const run = mapWithConcurrency(items, 3, async () => {
      inFlight++
      peak = Math.max(peak, inFlight)
      await new Promise<void>(resolve => release.push(() => { inFlight--; resolve() }))
    })

    // Let the first wave schedule, then drain in ticks so peak reflects the cap.
    while (release.length < 3) await Promise.resolve()
    expect(peak).toBe(3)
    while (release.length > 0 || inFlight > 0) {
      release.shift()?.()
      await Promise.resolve()
    }
    await run
    expect(peak).toBe(3)
  })

  it('records per-item failures without rejecting the whole run', async () => {
    const items = ['a', 'b', 'c']
    const results = await mapWithConcurrency(items, 5, async (s) => {
      if (s === 'b') throw new Error('boom')
    })
    expect(results.find(r => r.item === 'a')?.ok).toBe(true)
    expect(results.find(r => r.item === 'b')?.ok).toBe(false)
    expect(results.find(r => r.item === 'b')?.error?.message).toBe('boom')
    expect(results.find(r => r.item === 'c')?.ok).toBe(true)
  })

  it('wraps non-Error throws in an Error', async () => {
    const results = await mapWithConcurrency([1], 1, async () => {
      throw 'string failure'
    })
    expect(results[0].ok).toBe(false)
    expect(results[0].error).toBeInstanceOf(Error)
    expect(results[0].error?.message).toBe('string failure')
  })

  it('returns an empty array for no items', async () => {
    const results = await mapWithConcurrency([], 4, async () => {})
    expect(results).toEqual([])
  })
})
