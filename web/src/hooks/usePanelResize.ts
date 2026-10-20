import { useCallback, useEffect, useRef, useState } from 'react'
import { useLatestRef } from './useLatestRef'

/**
 * Drag-to-resize width for an element the user sizes on its own. By default
 * the grab handle sits on its left edge (a right-hand panel): dragging left
 * widens it. With `edge: 'right'` the handle is on its right edge (a table
 * column that may grow past the table): dragging right widens it. The width is
 * clamped to [min, max] and persisted in localStorage, so the arrangement
 * survives a reload.
 *
 * The pure helpers below are exported so the clamping and the storage
 * round-trip are testable without a DOM.
 */

/** Keyboard step, in px, for one arrow-key press on the handle. */
export const KEY_STEP = 16

export function clampWidth(width: number, min: number, max: number): number {
  if (!Number.isFinite(width)) return min
  return Math.min(max, Math.max(min, Math.round(width)))
}

export function loadWidth(key: string, fallback: number, min: number, max: number): number {
  try {
    const raw = localStorage.getItem(key)
    if (raw === null) return fallback
    const parsed = Number(raw)
    if (!Number.isFinite(parsed)) return fallback
    return clampWidth(parsed, min, max)
  } catch {
    return fallback
  }
}

function saveWidth(key: string, width: number): void {
  try { localStorage.setItem(key, String(width)) } catch { /* storage blocked */ }
}

export interface PanelResizeOptions {
  storageKey: string
  /** Narrowest width the panel can hold. Reaching it fires `onCollapse`. */
  min: number
  max: number
  /** Width used when nothing is stored yet, and the width a double-click restores. */
  fallback: number
  /** Accessible name of the handle, e.g. "Resize resources panel". */
  label: string
  /** Which edge the handle sits on. `left` (default): dragging left widens. */
  edge?: 'left' | 'right'
  /**
   * Called when a drag or a key press reaches `min`, so the owner can close the
   * panel instead of leaving a sliver nothing fits in. The width reverts to what
   * it was before, which is what reopening the panel then shows.
   */
  onCollapse?: () => void
  /**
   * Called when a drag on the closed panel's own affordance (`reopenDragProps`)
   * pulls it back past `min`. The panel opens mid-drag and follows the pointer
   * from there.
   */
  onExpand?: () => void
}

export interface PanelResize {
  width: number
  /** True while a drag is in flight — use it to suspend width transitions. */
  isResizing: boolean
  /** Spread onto the grab handle element. */
  separatorProps: {
    role: 'separator'
    'aria-orientation': 'vertical'
    'aria-label': string
    'aria-valuenow': number
    'aria-valuemin': number
    'aria-valuemax': number
    tabIndex: 0
    onPointerDown: (e: React.PointerEvent) => void
    onKeyDown: (e: React.KeyboardEvent) => void
    onDoubleClick: () => void
  }
  /**
   * Spread onto the affordance that shows while the panel is closed, so pulling
   * it open is a drag and not only a click. It does not preventDefault, so that
   * affordance's own click still works for a press with no movement.
   */
  reopenDragProps: {
    onPointerDown: (e: React.PointerEvent) => void
  }
  /** Sets and persists the width, clamped. For a caller-chosen width. */
  setWidth: (px: number) => void
  /**
   * Whether the last press on `reopenDragProps` actually moved. Lets that
   * affordance's `onClick` tell a click apart from the tail of a drag, which
   * has already chosen its own width.
   */
  wasDragged: () => boolean
}

export function usePanelResize({ storageKey, min, max, fallback, label, edge = 'left', onCollapse, onExpand }: PanelResizeOptions): PanelResize {
  // +1: moving the pointer left widens (a left-edge handle); -1: moving right does.
  const direction = edge === 'left' ? 1 : -1
  const [width, setWidth] = useState(() => loadWidth(storageKey, fallback, min, max))
  // `reopen` marks a drag that started on the closed panel's affordance: it
  // measures from zero and opens the panel on the way out.
  const [drag, setDrag] = useState<{ x: number; width: number; reopen?: boolean } | null>(null)
  const widthRef = useLatestRef(width)
  // Read through refs so a caller's inline callback doesn't resubscribe the
  // drag listeners on every render.
  const collapseRef = useLatestRef(onCollapse)
  const expandRef = useLatestRef(onExpand)
  const movedRef = useRef(false)

  const commit = useCallback((next: number) => {
    const clamped = clampWidth(next, min, max)
    widthRef.current = clamped
    setWidth(clamped)
    saveWidth(storageKey, clamped)
  }, [min, max, storageKey, widthRef])

  useEffect(() => {
    if (!drag) return
    // Which side of `min` the last move left the panel on, so opening and
    // closing fire once per crossing instead of once per pointer event.
    let open = !drag.reopen
    const onMove = (e: PointerEvent) => {
      movedRef.current = true
      const raw = drag.width + direction * (drag.x - e.clientX)
      const next = clampWidth(raw, min, max)
      if (raw > min) {
        if (!open) { open = true; expandRef.current?.() }
        setWidth(next)
        return
      }
      // No collapse behaviour asked for: the minimum is just a floor.
      if (!collapseRef.current) { setWidth(next); return }
      if (!open) return
      open = false
      collapseRef.current()
      // Pulled back in during a reopen drag: leave the width alone (it is still
      // the one the panel will show next time) and let the drag continue, so
      // pulling out again reopens.
      if (drag.reopen) return
      // Dragged shut from the open handle. Keep the width the panel had, so
      // reopening it is a reopen and not a 30px sliver to widen again, and end
      // the drag — the handle it started on is gone.
      commit(drag.width)
      setDrag(null)
    }
    const stop = () => setDrag(null)
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', stop)
    window.addEventListener('pointercancel', stop)
    // The pointer leaves the few-pixel-wide handle almost immediately, so the
    // cursor and the selection block live on <body>: the whole window behaves
    // like the handle until the button comes up.
    const { style } = document.body
    style.cursor = 'col-resize'
    style.userSelect = 'none'
    return () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', stop)
      window.removeEventListener('pointercancel', stop)
      style.cursor = ''
      style.userSelect = ''
      saveWidth(storageKey, widthRef.current)
    }
  }, [drag, min, max, storageKey, commit, direction, widthRef, collapseRef, expandRef])

  const widenKey = edge === 'left' ? 'ArrowLeft' : 'ArrowRight'
  const narrowKey = edge === 'left' ? 'ArrowRight' : 'ArrowLeft'
  const onKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === widenKey) { e.preventDefault(); commit(widthRef.current + KEY_STEP) }
    else if (e.key === narrowKey) {
      e.preventDefault()
      // Same rule as the drag: one step past the minimum closes the panel and
      // leaves the width it had.
      if (widthRef.current <= min && collapseRef.current) collapseRef.current()
      else commit(widthRef.current - KEY_STEP)
    }
  }, [commit, min, widenKey, narrowKey, widthRef, collapseRef])

  return {
    width,
    isResizing: drag !== null,
    separatorProps: {
      role: 'separator',
      'aria-orientation': 'vertical',
      'aria-label': label,
      'aria-valuenow': width,
      'aria-valuemin': min,
      'aria-valuemax': max,
      tabIndex: 0,
      onPointerDown: (e: React.PointerEvent) => {
        e.preventDefault()
        setDrag({ x: e.clientX, width: widthRef.current })
      },
      onKeyDown,
      onDoubleClick: () => commit(fallback),
    },
    reopenDragProps: {
      onPointerDown: (e: React.PointerEvent) => {
        movedRef.current = false
        setDrag({ x: e.clientX, width: 0, reopen: true })
      },
    },
    setWidth: commit,
    wasDragged: () => movedRef.current,
  }
}
