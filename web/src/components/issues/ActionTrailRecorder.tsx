import { useEffect, useRef } from 'react'
import { useLocation } from 'react-router-dom'
import { useSonner } from 'sonner'
import { installActionTrail, recordTrail, trailPagePath } from '@/lib/action-trail'

/**
 * Keeps the action trail (lib/action-trail): clicks, failed API calls and
 * console errors through installActionTrail, page changes and notices here.
 * Renders nothing; mounted once, inside the router.
 */
export function ActionTrailRecorder() {
  useEffect(() => installActionTrail(), [])

  const { pathname } = useLocation()
  useEffect(() => {
    recordTrail({ kind: 'page', path: trailPagePath(pathname) })
  }, [pathname])

  // A notice is recorded when it appears; one shown again under the same id
  // after it closed counts again.
  const { toasts } = useSonner()
  const shown = useRef(new Set<string | number>())
  useEffect(() => {
    for (const t of toasts) {
      if (!shown.current.has(t.id) && typeof t.title === 'string') {
        recordTrail({ kind: 'toast', level: t.type ?? 'default', message: t.title })
      }
    }
    shown.current = new Set(toasts.map(t => t.id))
  }, [toasts])

  return null
}
