import { useEffect, useRef } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { IndexLookup } from '@/lib/editor-index'

interface Options {
  activeSegmentId: number | undefined
  /** True once the grid is on screen and the index is loaded. */
  ready: boolean
  indexLookup: IndexLookup
  scrollToRow: (segIdx: number) => Promise<boolean>
}

/**
 * The last opened segment stays in the URL (?seg=<segment id>), so a reload,
 * Back or a shared link returns to it. Replaced, not pushed: moving between
 * segments adds no history entries.
 */
export function useActiveSegmentUrl({ activeSegmentId, ready, indexLookup, scrollToRow }: Options) {
  const [searchParams, setSearchParams] = useSearchParams()
  useEffect(() => {
    if (activeSegmentId === undefined || searchParams.get('seg') === String(activeSegmentId)) return
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      next.set('seg', String(activeSegmentId))
      return next
    }, { replace: true })
  }, [activeSegmentId, searchParams, setSearchParams])

  // Once the grid is up, open the segment the URL names.
  const restoredRef = useRef(false)
  useEffect(() => {
    if (restoredRef.current || !ready) return
    restoredRef.current = true
    const idx = indexLookup.bySegment.get(Number(searchParams.get('seg')))
    if (idx !== undefined) void scrollToRow(idx)
  }, [ready, indexLookup, searchParams, scrollToRow])
}
