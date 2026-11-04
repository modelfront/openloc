import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { RefObject } from 'react'
import { defaultRangeExtractor, useVirtualizer, type Range } from '@tanstack/react-virtual'
import type { EditorRow } from '@/api'
import { useColumnBorderDrag } from '@/hooks/useColumnBorderDrag'
import { EDITOR_PAGE_SIZE, useEditorPages } from '@/hooks/useEditorRows'
import { segmentColumnMins } from '@/lib/segment-columns'
import type { IndexLookup, IndexRow } from '@/lib/editor-index'
import type { LineCaret } from '@/lib/line-caret'
import type { ActiveSegment } from './useActiveSegment'
import type { EditorId } from '@/lib/editor-scope'

/** Rows within which a programmatic scroll is smooth; further ones jump. */
const SMOOTH_SCROLL_ROWS = 50

interface Options {
  jid: EditorId
  segmentScrollRef: RefObject<HTMLDivElement | null>
  /** The index rows; `rows` is the same list, empty while it loads. */
  indexRows: IndexRow[] | undefined
  rows: IndexRow[]
  indexLookup: IndexLookup
  filteredIndices: number[]
  extrasWidth: number
  gridShown: boolean
  active: Pick<ActiveSegment, 'activeSegmentIdx' | 'switchActiveSegment'>
}

/**
 * The segment grid's virtual list: the sticky header's measurements and
 * column drag, the row virtualizer, scrolling to a row, and the text of the
 * rows it shows.
 */
export function useSegmentGrid({
  jid, segmentScrollRef, indexRows, rows, indexLookup, filteredIndices, extrasWidth, gridShown, active,
}: Options) {
  const { activeSegmentIdx, switchActiveSegment } = active
  const filteredCount = filteredIndices.length

  // Navigate to next/previous within filtered results
  const goToSegment = useCallback((idx: number, caret?: LineCaret) => {
    if (idx >= 0 && idx < filteredCount) {
      void switchActiveSegment(filteredIndices[idx], { caret })
    }
  }, [filteredCount, filteredIndices, switchActiveSegment])

  // The column header lives INSIDE the scroll container as a sticky row so
  // it shares the exact same (scrollbar-reduced) content width as the body
  // rows — otherwise the Source/Translation columns drift out of alignment when
  // the vertical scrollbar appears. Because the header occupies flow space at
  // the top of the scroll content, the virtualizer needs its height as
  // `scrollMargin` (content-before-the-list), and each row's transform
  // subtracts it. We measure the header rather than hardcoding so font/zoom
  // changes stay correct.
  const headerRef = useRef<HTMLDivElement>(null)
  const columnMins = useMemo(() => segmentColumnMins(extrasWidth), [extrasWidth])
  const columnDrag = useColumnBorderDrag(headerRef, columnMins)
  const [headerHeight, setHeaderHeight] = useState(0)
  useLayoutEffect(() => {
    const el = headerRef.current
    if (!el) return
    const measure = () => setHeaderHeight(el.getBoundingClientRect().height)
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [gridShown])
  // Position of the active segment in the filtered list. The
  // virtualizer's `rangeExtractor` (below) appends this index to the visible
  // range so the active SegmentRow stays mounted regardless of scroll position.
  // Unmounting it would destroy the in-memory edit not yet auto-saved by the
  // 3s draft-save debounce, and on scroll-back the editor would re-initialize
  // from the stale React Query cache. Keeping the row mounted preserves the
  // ProseMirror doc and the React state holding `editText` / `originalText` /
  // baselineRef.
  const activeRowKeeperRef = useRef<number>(-1)
  activeRowKeeperRef.current = activeSegmentIdx == null ? -1 : filteredIndices.indexOf(activeSegmentIdx)
  const rowVirtualizer = useVirtualizer({
    count: filteredCount,
    getScrollElement: () => segmentScrollRef.current,
    // A one-line row (index.css --seg-row-min-h), the same for every row.
    // TanStack caches a measured size only when it differs from the estimate,
    // so an estimate that changed with the active row would replace the real
    // height of every row that measured exactly 33px.
    estimateSize: () => 33,
    // Measured heights are cached per segment, not per list position: a filter
    // or search moves segments to new positions, and a row that stays mounted
    // through that is never measured again.
    getItemKey: useCallback(
      (idx: number) => rows[filteredIndices[idx]]?.segmentId ?? idx,
      [rows, filteredIndices],
    ),
    overscan: 10,
    // Sticky header occupies this many px at the top of the scroll content.
    scrollMargin: headerHeight,
    rangeExtractor: useCallback((range: Range) => {
      const base = defaultRangeExtractor(range)
      const keeper = activeRowKeeperRef.current
      if (keeper < 0 || base.includes(keeper)) return base
      // Insert the active index in sorted order so virtual items stay ordered.
      const merged = [...base, keeper].sort((a, b) => a - b)
      return merged
    }, []),
  })

  // Stabilise the clicked row's visual position across activation.
  // Anchor via the row element's `getBoundingClientRect()`, not TanStack's
  // `item.start`: the previously-active row's height collapse only resolves
  // after its `measureElement` ref fires in a later frame, so `start` values
  // are stale in the first rAF. Capture the clicked row's screen-relative top
  // before changing activeSegmentIdx, then restore it over a settle loop
  // (multiple rAFs) by adjusting scrollTop.
  const prevActiveRef = useRef<number | null>(null)
  const programmaticScrollRef = useRef(false)
  const pendingAnchorRef = useRef<{ idx: number; clientTop: number } | null>(null)

  // Wrap onActivate from each row so we can capture the visual top of the
  // row that's about to become active, BEFORE React renders the new state.
  const captureAnchorBeforeActivate = useCallback((idx: number) => {
    const row = document.querySelector<HTMLElement>(`[data-segment-idx="${idx}"]`)
    if (row) {
      pendingAnchorRef.current = { idx, clientTop: row.getBoundingClientRect().top }
    }
  }, [])

  useEffect(() => {
    if (activeSegmentIdx === prevActiveRef.current) return
    const scrollEl = segmentScrollRef.current

    // Programmatic scroll path (F&R) handles its own positioning — skip.
    if (programmaticScrollRef.current) {
      programmaticScrollRef.current = false
      prevActiveRef.current = activeSegmentIdx
      return
    }

    prevActiveRef.current = activeSegmentIdx
    if (activeSegmentIdx == null || !scrollEl) return

    const anchor = pendingAnchorRef.current
    pendingAnchorRef.current = null

    // Settle loop: adjust on each rAF for a bounded number of frames so
    // late measureElement flushes (previously-active row collapsing back
    // to compact, newly-active editor rendering) can be absorbed. Stop
    // EARLY once the row's position is stable across two consecutive
    // checks — prevents the loop from chasing oscillating measurements
    // when the layout never quite settles.
    let frames = 0
    const MAX_FRAMES = 20 // ~330ms at 60fps, covers measurement + 150ms transition
    let lastTop: number | null = null
    let lastHeight = -1
    let stableFrames = 0
    const settle = () => {
      const row = document.querySelector<HTMLElement>(`[data-segment-idx="${activeSegmentIdx}"]`)
      if (!row) {
        if (frames++ < MAX_FRAMES) requestAnimationFrame(settle)
        return
      }
      const rect = row.getBoundingClientRect()
      // The virtualizer's total size, which drops when the row that just
      // deactivated collapses back to compact.
      const contentHeight = scrollEl.scrollHeight

      // Stability check: the row hasn't moved AND the list has stopped
      // resizing — stop adjusting before more late measurements arrive and we
      // start oscillating. Both halves are needed: on a slow frame the row can
      // hold still for two frames while the deactivated row's collapse is
      // still pending, and exiting there leaves the anchor to be undone by a
      // shift of the collapsed row's full height.
      if (lastTop !== null && Math.abs(rect.top - lastTop) < 1 && contentHeight === lastHeight) {
        stableFrames++
        if (stableFrames >= 2) return // settled for 2 consecutive frames
      } else {
        stableFrames = 0
      }
      lastTop = rect.top
      lastHeight = contentHeight

      if (anchor && anchor.idx === activeSegmentIdx) {
        const delta = rect.top - anchor.clientTop
        if (Math.abs(delta) > 1) {
          scrollEl.scrollTop += delta
          lastTop = anchor.clientTop // expected post-adjustment position
        }
      } else {
        const containerRect = scrollEl.getBoundingClientRect()
        const rowTopInContainer = rect.top - containerRect.top
        const rowBottomInContainer = rect.bottom - containerRect.top
        if (rowTopInContainer < 0 || rowBottomInContainer > scrollEl.clientHeight) {
          row.scrollIntoView({ block: 'center', behavior: 'auto' })
        }
      }

      if (frames++ < MAX_FRAMES) requestAnimationFrame(settle)
    }
    requestAnimationFrame(settle)
  }, [activeSegmentIdx, segmentScrollRef])

  // Scroll to a row by its index position (Find & Replace, spell check,
  // cross-references, Jump to segment, a reload). Explicitly scrolls the
  // virtualizer — does NOT rely on the activeSegmentIdx effect, which has
  // "restore position" logic that fights programmatic navigation. Only scrolls
  // when the row is off-screen or clipped; smooth only over a short distance,
  // since a smooth scroll across thousands of rows of estimated height misses.
  // Returns false when the filter hides the row.
  const scrollToRow = useCallback(async (
    segIdx: number,
    opts: { activate?: boolean } = {},
  ): Promise<boolean> => {
    const activate = opts.activate ?? true
    const filteredPos = filteredIndices.indexOf(segIdx)
    if (filteredPos === -1) return false
    programmaticScrollRef.current = true // tell the activeSegmentIdx effect to skip its scroll
    // Route through the chokepoint so the previously-active row's draft
    // is flushed before we navigate to the F&R match.
    //
    // `activate: false` scrolls without opening an editor. The spell check
    // dialog needs that: it writes corrections through the bulk draft endpoint,
    // and an open editor on a row being written to would race its own autosave
    // against those writes.
    if (activate) await switchActiveSegment(segIdx)
    requestAnimationFrame(() => {
      const scrollEl = segmentScrollRef.current
      const items = rowVirtualizer.getVirtualItems()
      const behavior = Math.abs(filteredPos - (items[0]?.index ?? 0)) < SMOOTH_SCROLL_ROWS ? 'smooth' : 'auto'
      if (!scrollEl) {
        rowVirtualizer.scrollToIndex(filteredPos, { align: 'center', behavior })
        return
      }
      // Check if the target row is already fully visible
      const item = items.find(v => v.index === filteredPos)
      if (item) {
        const rowTop = item.start - scrollEl.scrollTop
        const rowBottom = rowTop + item.size
        const viewportHeight = scrollEl.clientHeight
        // Fully visible with some padding (active row expands, so add margin)
        const pad = 40
        if (rowTop >= pad && rowBottom <= viewportHeight - pad) {
          return // already in view — no scroll needed
        }
      }
      // 'auto' = minimal scroll to bring item into view (TanStack's equivalent of 'nearest')
      rowVirtualizer.scrollToIndex(filteredPos, { align: 'auto', behavior })
    })
    return true
  }, [filteredIndices, rowVirtualizer, switchActiveSegment, segmentScrollRef])

  const scrollToTranslation = useCallback(async (translationId: number, opts: { activate?: boolean } = {}) => {
    const segIdx = indexLookup.byTranslation.get(translationId)
    if (segIdx !== undefined) await scrollToRow(segIdx, opts)
  }, [indexLookup, scrollToRow])

  // Text of the rows on screen, plus the open row wherever it is, by segment
  // id. A row whose page has not arrived yet renders as a placeholder.
  const virtualItems = rowVirtualizer.getVirtualItems()
  const shownIndices = useMemo(() => {
    const out = virtualItems.map(v => filteredIndices[v.index])
    // Unfiltered, the page past each edge of the view loads too, so a short
    // scroll finds its rows ready.
    if (out.length > 0 && filteredIndices.length === rows.length) {
      out.push(Math.max(0, out[0] - EDITOR_PAGE_SIZE / 2), Math.min(rows.length - 1, out[out.length - 1] + EDITOR_PAGE_SIZE / 2))
    }
    if (activeSegmentIdx != null) out.push(activeSegmentIdx)
    return out
  }, [virtualItems, filteredIndices, activeSegmentIdx, rows.length])
  const loadedRows = useEditorPages(jid, indexRows, shownIndices)
  const rowAt = (idx: number): EditorRow | undefined => {
    const row = rows[idx]
    return row ? loadedRows.get(row.segmentId) : undefined
  }

  return {
    segmentScrollRef,
    headerRef,
    headerHeight,
    columnDrag,
    rowVirtualizer,
    captureAnchorBeforeActivate,
    goToSegment,
    scrollToRow,
    scrollToTranslation,
    rowAt,
  }
}

export type SegmentGridState = ReturnType<typeof useSegmentGrid>
