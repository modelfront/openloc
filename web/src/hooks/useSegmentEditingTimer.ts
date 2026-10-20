import { useRef, useCallback, useEffect } from 'react'

// Accumulated editing ms per translation id, for visits that ended without a
// confirm. It lives outside the row because the CAT grid unmounts rows that
// scroll out of view, and a later visit must still report this time.
// `consume` removes the entry.
const accumulated = new Map<number, number>()

function addTime(translationId: number, ms: number) {
  if (ms > 0) accumulated.set(translationId, (accumulated.get(translationId) ?? 0) + ms)
}

/**
 * useSegmentEditingTimer — how long the user has spent on a segment while it
 * was active, added up across visits and reported once, by the confirm.
 *
 *   - `start()` opens a window. Calling it while a window is open keeps the
 *      time of that window.
 *   - `stop()` closes the open window and keeps its time for the next confirm.
 *      Unmounting the row does the same.
 *   - `consume()` returns everything kept for this translation plus the open
 *      window, clears it, and, if a window was open, opens the next one now.
 *      Only a confirm calls it; a draft save reports no time.
 *   - `restore(ms)` gives ms back: a confirm the server refused hands back what
 *      it consumed, so the next confirm reports it.
 *
 * Without a translation id every call is a no-op and `consume()` returns 0.
 */
export function useSegmentEditingTimer(translationId: number | undefined): {
  start: () => void
  stop: () => void
  consume: () => number
  restore: (ms: number) => void
} {
  const startRef = useRef<number>(0) // start of the open window; 0 = none

  const stop = useCallback(() => {
    if (translationId == null || startRef.current === 0) return
    addTime(translationId, Date.now() - startRef.current)
    startRef.current = 0
  }, [translationId])

  const start = useCallback(() => {
    if (translationId == null) return
    stop()
    startRef.current = Date.now()
  }, [translationId, stop])

  const consume = useCallback(() => {
    if (translationId == null) return 0
    const open = startRef.current > 0
    stop()
    const total = accumulated.get(translationId) ?? 0
    accumulated.delete(translationId)
    if (open) startRef.current = Date.now()
    return total
  }, [translationId, stop])

  const restore = useCallback((ms: number) => {
    if (translationId != null) addTime(translationId, ms)
  }, [translationId])

  // Unmount, or a different translation, closes the open window.
  useEffect(() => stop, [stop])

  return { start, stop, consume, restore }
}
