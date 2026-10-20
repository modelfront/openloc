import { useRef, useCallback } from 'react'

/**
 * useInFlightGuard — serialise concurrent calls to an async function so the
 * second one waits for the first to finish before issuing.
 *
 * The classic "two saves race against optimistic-lock" pattern: caller A
 * starts an async write, before it returns caller B starts another with the
 * same `expected_updated_at` baseline → server rejects B with 409. The cure
 * is to serialise: B awaits A's completion, A's `onSuccess` advances the
 * baseline, B re-evaluates and either issues with the fresh baseline or
 * no-ops because nothing remains dirty.
 *
 * Guard semantics:
 *   1. `guard(fn)` checks whether a previous call is in flight. If so, it
 *      awaits the completion sentinel before running `fn`.
 *   2. The completion sentinel is `inner.then(() => undefined, () => undefined)`,
 *      which never rejects. Caller A's failure does not leak into B's flow —
 *      B's only signal is "A is done, my turn". B's own `fn` decides whether
 *      to proceed (e.g., re-checking `dirtyRef`).
 *   3. Cleanup uses an identity check (`if (ref.current === sentinel)`) so a
 *      third caller that started while we were running doesn't have its
 *      sentinel clobbered when ours unwinds.
 *
 * NOT a cancellation mechanism. Concurrent calls QUEUE; they don't cancel.
 * For cancel-on-supersede semantics use `AbortController` instead.
 *
 * USAGE WARNING — do post-completion side effects INSIDE the guarded fn,
 * not after `await guard(...)`. The sentinel resolves the moment the inner
 * Promise settles; any queued caller wakes up at that point. If your
 * post-completion logic advances state that the next caller depends on
 * (baselines, dirty flags, optimistic-lock cache patches), and that logic
 * lives AFTER the guard call instead of inside its fn, the next caller
 * will read stale state in the gap. Move post-completion sync
 * inside the guarded fn and you're safe.
 */
export function useInFlightGuard() {
  const ref = useRef<Promise<void> | null>(null)

  const guard = useCallback(async <T>(fn: () => Promise<T>): Promise<T> => {
    // Loop on ref.current — not a one-shot capture. Multiple concurrent
    // callers (3+) all observe the FIRST sentinel synchronously when they
    // start; without re-checking after each await, a third caller would
    // resume in the same microtask batch as the second and proceed BEFORE
    // the second's fn has had a chance to advance shared state. The loop
    // ensures each caller waits for ALL priors (the second's sentinel
    // appears on `ref` while we're still awaiting the first), not just
    // the one captured at start. Sentinels never reject (see below), so
    // these awaits never throw.
    while (ref.current) {
      await ref.current
    }

    // Run fn synchronously to claim the slot before any await yields. The
    // ref assignment must complete in the same tick as the dirty/precondition
    // check inside fn — otherwise a concurrent caller could observe ref=null
    // between the precondition check and the assignment.
    const inner = fn()
    // Map both fulfilment AND rejection to undefined: other callers awaiting
    // this sentinel must not see our error. The caller's own try/catch around
    // `await guard(...)` handles success-path errors normally.
    const sentinel = inner.then(() => undefined, () => undefined)
    ref.current = sentinel

    try {
      return await inner
    } finally {
      // Identity check: a later caller may have started + claimed the slot
      // while we were running. Don't clobber theirs.
      if (ref.current === sentinel) {
        ref.current = null
      }
    }
  }, [])

  return guard
}
