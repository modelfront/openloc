import { useCallback, useEffect, useRef } from 'react'

// For pages that send the user to another tab (a create form, the CAT editor)
// and show data that tab can change. The app sets `refetchOnWindowFocus: false`
// globally (main.tsx), so such a page refreshes itself when the user comes
// back. One-shot rather than a blanket focus refetch: `arm()` when the tab
// opens, and `refresh` runs on the next window focus only.

export function useRefreshOnReturn(refresh: () => void): () => void {
  const refreshRef = useRef(refresh)
  useEffect(() => { refreshRef.current = refresh })
  // Tracked so an unmount mid-trip doesn't leak the listener.
  const cleanupRef = useRef<(() => void) | null>(null)

  useEffect(() => () => cleanupRef.current?.(), [])

  return useCallback(() => {
    cleanupRef.current?.()
    const onFocus = () => {
      refreshRef.current()
      cleanupRef.current?.()
    }
    window.addEventListener('focus', onFocus)
    cleanupRef.current = () => {
      window.removeEventListener('focus', onFocus)
      cleanupRef.current = null
    }
  }, [])
}
