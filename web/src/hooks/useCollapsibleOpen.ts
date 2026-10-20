import { useState } from 'react'

/**
 * Open/closed state for `SlimCollapsibleSection`. Extracted so the behavior —
 * including the auto-open-once rule — is unit-testable on its own and stays put
 * if a second section variant ever needs it.
 *
 * State is NOT persisted: every mount (page load, refresh, navigating back)
 * resets to `defaultOpen`.
 *
 * `openWhen` opens the section once when it first turns true (e.g. an empty
 * list that loaded async). A manual toggle afterwards always wins — the
 * auto-open fires at most once per mount.
 */
export function useCollapsibleOpen(defaultOpen = false, openWhen?: boolean) {
  const [open, setOpen] = useState(defaultOpen || !!openWhen)
  const [autoOpened, setAutoOpened] = useState(!!openWhen)
  if (openWhen && !autoOpened) {
    setAutoOpened(true)
    setOpen(true)
  }
  return { open, toggle: () => setOpen(prev => !prev) }
}
