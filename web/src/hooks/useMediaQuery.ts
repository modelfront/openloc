import { useEffect, useState } from 'react'

/**
 * Subscribes to a CSS media query and returns whether it currently matches.
 * Re-renders when the match state flips (e.g. crossing a width breakpoint).
 *
 * SSR-/test-safe: falls back to `false` when `matchMedia` is unavailable, and
 * guards the initial read so it never throws in environments without a real
 * `window` (jsdom provides `matchMedia` only when polyfilled).
 *
 * Example:
 *   const isNarrow = useMediaQuery('(max-width: 1199px)')
 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => {
    try {
      return window.matchMedia?.(query).matches ?? false
    } catch {
      return false
    }
  })

  useEffect(() => {
    let mql: MediaQueryList
    try {
      mql = window.matchMedia(query)
    } catch {
      return
    }
    const onChange = () => setMatches(mql.matches)
    onChange() // sync in case the query changed between render and effect
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [query])

  return matches
}
