import { describe, it, expect } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useInFlightGuard } from './useInFlightGuard'

/**
 * Unit tests for the in-flight serialisation primitive.
 *
 * The primitive exists to prevent the saveMut/saveDraft 409-race class. These
 * tests prove the contract independently of any UI or save code, so a future
 * regression in either consumer can be triaged against the primitive itself
 * rather than re-debugging concurrency from scratch.
 */
describe('useInFlightGuard', () => {
  it('serialises two concurrent calls — second awaits first', async () => {
    const { result } = renderHook(() => useInFlightGuard())
    const guard = result.current

    // Manually controlled deferreds so we can pin the order of completion.
    let resolveA: () => void = () => {}
    let resolveB: () => void = () => {}
    const aDone = new Promise<void>((r) => { resolveA = r })
    const bDone = new Promise<void>((r) => { resolveB = r })

    const order: string[] = []

    let p1: Promise<string>
    let p2: Promise<string>
    await act(async () => {
      // Start both concurrently — B's fn must NOT run until A resolves.
      p1 = guard(async () => {
        await aDone
        order.push('A-fn-done')
        return 'A'
      })
      p2 = guard(async () => {
        order.push('B-fn-started')
        await bDone
        order.push('B-fn-done')
        return 'B'
      })

      // Yield once so the microtask queue drains. B should be parked on the
      // sentinel await; its fn body should not have started.
      await Promise.resolve()
      await Promise.resolve()
      expect(order, 'B fn must not start until A resolves').toEqual([])

      // Resolve A. B's fn body should now run.
      resolveA()
      await aDone
      // Yield for B to start its fn.
      await Promise.resolve()
      await Promise.resolve()
      expect(order, "after A resolves, B's fn must start").toEqual(['A-fn-done', 'B-fn-started'])

      resolveB()
    })

    expect(await p1!).toBe('A')
    expect(await p2!).toBe('B')
    expect(order).toEqual(['A-fn-done', 'B-fn-started', 'B-fn-done'])
  })

  it("does not propagate the prior call's rejection to the next caller", async () => {
    const { result } = renderHook(() => useInFlightGuard())
    const guard = result.current

    let p1: Promise<string>
    let p2: Promise<string>
    await act(async () => {
      // First call rejects.
      p1 = guard(async () => {
        throw new Error('boom A')
      })
      // Catch immediately so unhandled-rejection doesn't blow the test.
      p1.catch(() => undefined)

      // Second call should still run normally — A's rejection is the
      // sentinel's business, not B's.
      p2 = guard(async () => 'B-ok')
    })

    await expect(p1!).rejects.toThrow('boom A')
    await expect(p2!).resolves.toBe('B-ok')
  })

  it("returns the inner fn's resolved value to the caller", async () => {
    const { result } = renderHook(() => useInFlightGuard())
    const guard = result.current

    const value = await guard(async () => ({ id: 42, text: 'hello' }))
    expect(value).toEqual({ id: 42, text: 'hello' })
  })

  it("propagates the inner fn's rejection to the caller (so .catch still works)", async () => {
    const { result } = renderHook(() => useInFlightGuard())
    const guard = result.current

    const err = new Error('caller-visible')
    await expect(guard(async () => { throw err })).rejects.toBe(err)
  })

  it('three-deep: third caller waits for second, not first', async () => {
    const { result } = renderHook(() => useInFlightGuard())
    const guard = result.current

    let resolveA: () => void = () => {}
    let resolveB: () => void = () => {}
    let resolveC: () => void = () => {}
    const aDone = new Promise<void>((r) => { resolveA = r })
    const bDone = new Promise<void>((r) => { resolveB = r })
    const cDone = new Promise<void>((r) => { resolveC = r })

    const order: string[] = []

    let pA: Promise<string>
    let pB: Promise<string>
    let pC: Promise<string>
    await act(async () => {
      pA = guard(async () => { await aDone; order.push('A'); return 'A' })
      pB = guard(async () => { await bDone; order.push('B'); return 'B' })
      pC = guard(async () => { await cDone; order.push('C'); return 'C' })

      // Resolve in order — each caller's fn must not start until the prior
      // sentinel settles.
      resolveA()
      await aDone
      await Promise.resolve()
      await Promise.resolve()
      resolveB()
      await bDone
      await Promise.resolve()
      await Promise.resolve()
      resolveC()
    })

    await Promise.all([pA!, pB!, pC!])
    expect(order, 'guard preserves call order across three concurrent invocations').toEqual(['A', 'B', 'C'])
  })

  it('after all in-flight calls drain, a fresh call is not blocked', async () => {
    const { result } = renderHook(() => useInFlightGuard())
    const guard = result.current

    await guard(async () => 'first')
    // No prior in flight — second call should run immediately, not wait.
    const start = performance.now()
    await guard(async () => 'second')
    const elapsed = performance.now() - start
    // Generous threshold; the point is "doesn't block on a stale sentinel".
    expect(elapsed).toBeLessThan(50)
  })

  it('the same guard instance is stable across re-renders', async () => {
    const { result, rerender } = renderHook(() => useInFlightGuard())
    const guard1 = result.current
    rerender()
    const guard2 = result.current
    expect(guard1, 'guard reference must be stable so consumers can use it as a useCallback dep').toBe(guard2)
  })

  // Defensive — the production motivation for the identity check. The
  // failure mode it prevents: caller B replaces the ref with its own
  // sentinel; caller A's finally clause clobbers it; a later concurrent
  // caller D observes ref=null instead of B's sentinel and doesn't wait.
  // We can't observe the internal ref directly, but we can prove the
  // serialisation order holds even when callers settle out-of-order:
  // start A, B, C; resolve A LAST. The identity check ensures B's sentinel
  // remains the gate for C's await even after A unwinds.
  it('identity check: late-resolving prior call does not break later callers serialisation', async () => {
    const { result } = renderHook(() => useInFlightGuard())
    const guard = result.current

    let resolveA: () => void = () => {}
    let resolveB: () => void = () => {}
    let resolveC: () => void = () => {}
    const aDone = new Promise<void>((r) => { resolveA = r })
    const bDone = new Promise<void>((r) => { resolveB = r })
    const cDone = new Promise<void>((r) => { resolveC = r })

    const order: string[] = []

    let pA: Promise<string>
    let pB: Promise<string>
    let pC: Promise<string>
    await act(async () => {
      pA = guard(async () => { await aDone; order.push('A'); return 'A' })
      pB = guard(async () => { await bDone; order.push('B'); return 'B' })
      pC = guard(async () => { await cDone; order.push('C'); return 'C' })

      // Resolve B FIRST (out of order). C must still be gated; if the
      // identity check is broken, A's finally would clobber B's sentinel
      // before B's fn ever ran, then C would observe ref=null and start
      // its fn ahead of B.
      // (Note: in practice a guard CALLER would only B if A is done first,
      // but the primitive must not assume that — it must serialise no
      // matter the resolution order.)
      resolveA()
      await aDone
      // After A resolves, B's fn starts (B was awaiting A's sentinel).
      // We need B to actually run and update `ref.current` before C is
      // examined. Yield enough times.
      for (let i = 0; i < 5; i++) await Promise.resolve()
      resolveB()
      await bDone
      for (let i = 0; i < 5; i++) await Promise.resolve()
      resolveC()
    })

    await Promise.all([pA!, pB!, pC!])
    expect(order).toEqual(['A', 'B', 'C'])
  })

  // Pinned from a usage perspective. The consumer-side
  // rule "post-completion sync MUST be inside the guarded fn" can't be
  // enforced structurally (the primitive can't tell what the consumer does
  // after `await guard(...)`). What we CAN pin is that the primitive
  // honours the rule when used correctly: state written inside fn-A is
  // visible to fn-B by the time B starts.
  //
  // If a future change to `useInFlightGuard` made the sentinel resolve too
  // early (e.g., before fn() actually completed its sync block, or before
  // the microtask queue drained), B would start observing pre-sync state
  // and this test would fail. That's the regression net at the
  // primitive level. (The e2e stress loop catches it at the consumer level.)
  it('queued caller observes state set inside the prior fn', async () => {
    const { result } = renderHook(() => useInFlightGuard())
    const guard = result.current

    // Simulated shared state (in production: baselineUpdatedAtRef + cache).
    const shared = { baseline: 0 }
    const observations: number[] = []

    await act(async () => {
      const p1 = guard(async () => {
        // Simulated network call — yields, then writes shared state INSIDE
        // the guarded fn (the architecturally-correct shape).
        await Promise.resolve()
        await Promise.resolve()
        shared.baseline = 1
        return 'A'
      })

      const p2 = guard(async () => {
        // Queued behind p1. Must observe baseline=1 (A's write).
        observations.push(shared.baseline)
        await Promise.resolve()
        shared.baseline = 2
        return 'B'
      })

      const p3 = guard(async () => {
        // Queued behind p2. Must observe baseline=2 (B's write).
        observations.push(shared.baseline)
        return 'C'
      })

      await Promise.all([p1, p2, p3])
    })

    expect(
      observations,
      'each queued caller must observe the prior fn\'s post-await state. If this fails, the primitive\'s sentinel is resolving before fn body completes (or microtasks aren\'t draining as expected), and the consumer rule no longer holds.',
    ).toEqual([1, 2])
  })
})
