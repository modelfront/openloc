import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { moveColumnBorder } from '../lib/segment-columns'

/**
 * Drag the borders between a grid header's columns. `widths` is null until the
 * first drag (the caller's defaults apply), then the px width of every column.
 * Kept in state only — a reload starts from the defaults again.
 *
 * `startDrag(i)` goes on the handle of the border between column i and i + 1.
 * The drag starts from the header cells' measured widths, so it continues from
 * whatever the defaults (or an earlier drag) rendered. `minWidths` holds one
 * entry per column: the narrowest a drag may make it.
 */
export function useColumnBorderDrag(headerRef: RefObject<HTMLElement | null>, minWidths: number[]) {
  const [widths, setWidths] = useState<number[] | null>(null)
  const [dragging, setDragging] = useState<number | null>(null)
  const stopRef = useRef<(() => void) | null>(null)

  useEffect(() => () => stopRef.current?.(), [])

  const startDrag = useCallback((i: number) => (e: React.PointerEvent) => {
    if (e.button !== 0 || !headerRef.current) return
    e.preventDefault() // no text selection while dragging
    e.stopPropagation()
    const cells = Array.from(headerRef.current.children).slice(0, minWidths.length)
    const start = cells.map(c => c.getBoundingClientRect().width)
    const startX = e.clientX
    setDragging(i)
    const onMove = (ev: PointerEvent) => setWidths(moveColumnBorder(start, i, ev.clientX - startX, minWidths))
    // pointercancel / blur: the browser can take the pointer away (a touch
    // pan, an OS gesture, switching windows) without ever sending pointerup.
    const stop = () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', stop)
      window.removeEventListener('pointercancel', stop)
      window.removeEventListener('blur', stop)
      stopRef.current = null
      setDragging(null)
    }
    stopRef.current = stop
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', stop)
    window.addEventListener('pointercancel', stop)
    window.addEventListener('blur', stop)
  }, [headerRef, minWidths])

  const reset = useCallback(() => setWidths(null), [])

  return { widths, dragging, startDrag, reset }
}
