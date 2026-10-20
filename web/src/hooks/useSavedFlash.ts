import { useCallback, useEffect, useRef, useState } from 'react'

/** How long "Saved ✓" stays after a save lands. */
export const SAVED_FLASH_MS = 5000

/**
 * The "Saved ✓" moment after a change that saves on its own. `flash()` shows
 * it for `ms`, restarting the wait on every call; the timer is cleared on
 * unmount.
 */
export function useSavedFlash(ms = SAVED_FLASH_MS) {
  const [shown, setShown] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const flash = useCallback(() => {
    clearTimeout(timer.current)
    setShown(true)
    timer.current = setTimeout(() => setShown(false), ms)
  }, [ms])
  useEffect(() => () => clearTimeout(timer.current), [])
  return [shown, flash] as const
}
