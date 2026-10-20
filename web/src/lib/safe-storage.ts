// localStorage access that never throws. Storage can be blocked (privacy
// extensions, some private modes) or full; a remembered preference is never
// worth an error, so reads fall back to null and writes are dropped.
// In a component, prefer `useStoredState`, which is built on these.

/** The stored string, or null when the key is missing or storage is blocked. */
export function readStorage(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

export function writeStorage(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    // storage blocked or full: the value just isn't remembered
  }
}

export function removeStorage(key: string): void {
  try {
    localStorage.removeItem(key)
  } catch {
    // storage blocked
  }
}
