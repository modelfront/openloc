import { useEffect } from 'react'

const APP_TITLE = 'OpenLoc'

function cleanParts(parts: (string | null | undefined)[]): string[] {
  return parts.map((p) => p?.trim() ?? '').filter(Boolean)
}

/** Builds the tab title: the non-empty parts, most specific first, then the app name. */
export function formatPageTitle(...parts: (string | null | undefined)[]): string {
  return [...cleanParts(parts), APP_TITLE].join(' · ')
}

/**
 * Sets the browser tab title to `<parts> · OpenLoc` and resets it to
 * `OpenLoc` on unmount. With no non-empty part (data still loading, or a
 * sub-section header) it leaves the title alone, so it never overrides the
 * page that owns the title.
 */
export function usePageTitle(...parts: (string | null | undefined)[]) {
  const hasParts = cleanParts(parts).length > 0
  const title = formatPageTitle(...parts)
  useEffect(() => {
    if (!hasParts) return
    document.title = title
    return () => {
      document.title = APP_TITLE
    }
  }, [title, hasParts])
}
