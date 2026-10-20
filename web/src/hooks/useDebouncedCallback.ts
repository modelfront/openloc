import { useEffect, useMemo, useRef } from 'react'

/**
 * A debounced callback with imperative escape hatches.
 *
 * Call it like a normal function; use `.flush()` to fire the pending call NOW
 * (e.g., before unmount or navigation); use `.cancel()` to discard any pending
 * call entirely.
 */
export interface DebouncedCallback<Args extends unknown[]> {
  (...args: Args): void
  /** Fire the pending call immediately with the most recent arguments. No-op if nothing is pending. */
  flush: () => void
  /** Discard any pending call. No-op if nothing is pending. */
  cancel: () => void
}

/**
 * Returns a stable debounced version of `fn` that collapses rapid successive
 * calls into a single invocation with the latest arguments, firing `delay` ms
 * after the last call.
 *
 * Unlike `useDebounce(value, delay)` which debounces a *value*, this hook
 * debounces a *callback* — use it when the caller triggers the work
 * imperatively (e.g., on every keystroke, row add, or button click).
 *
 * Guarantees:
 * - The returned function has a stable reference across renders (safe for `useEffect` deps).
 * - Each call always dispatches to the LATEST `fn` passed in, never a stale closure.
 * - `.flush()` fires the pending call immediately — use this in an unmount cleanup
 *   to avoid losing the last save when the user navigates away mid-debounce.
 * - `.cancel()` discards the pending call without firing.
 *
 * Example:
 *   const debouncedSave = useDebouncedCallback(async (rows) => {
 *     await onSave(rows)
 *   }, 500)
 *
 *   const addRow = () => { ...; debouncedSave(newRows) }
 *
 *   useEffect(() => () => debouncedSave.flush(), [debouncedSave])
 */
export function useDebouncedCallback<Args extends unknown[]>(
  fn: (...args: Args) => void | Promise<void>,
  delay: number,
): DebouncedCallback<Args> {
  // Keep the latest fn in a ref so the debounced wrapper always calls the
  // freshest version — not a stale closure from a previous render.
  const fnRef = useRef(fn)
  useEffect(() => {
    fnRef.current = fn
  }, [fn])

  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const pendingArgsRef = useRef<Args | null>(null)

  // Build a stable debounced callable with .flush() and .cancel() attached.
  // useMemo gives us a stable reference across renders (the only dep that
  // should matter is `delay`, and it typically never changes).
  const debounced = useMemo<DebouncedCallback<Args>>(() => {
    const call = (...args: Args) => {
      pendingArgsRef.current = args
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
      }
      timerRef.current = setTimeout(() => {
        timerRef.current = null
        const pending = pendingArgsRef.current
        pendingArgsRef.current = null
        if (pending) {
          void fnRef.current(...pending)
        }
      }, delay)
    }
    call.flush = () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
      const pending = pendingArgsRef.current
      pendingArgsRef.current = null
      if (pending) {
        void fnRef.current(...pending)
      }
    }
    call.cancel = () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
      pendingArgsRef.current = null
    }
    return call
  }, [delay])

  // NOTE: we intentionally do NOT cancel pending calls on unmount here.
  // React unmounts effects in LIFO order, and if this hook cancelled before
  // the caller's cleanup had a chance to call .flush(), the pending write
  // would be silently dropped. Instead, the caller must explicitly choose:
  //
  //   useEffect(() => () => debouncedFn.flush(),  [debouncedFn])  // save on unmount
  //   useEffect(() => () => debouncedFn.cancel(), [debouncedFn])  // discard on unmount
  //
  // If the caller does nothing, the pending setTimeout still fires after
  // unmount, calling fnRef.current — which may write into unmounted state.
  // The caller should guard against that or flush explicitly.

  return debounced
}
