import * as React from 'react'

/** Gap kept between a dragged popup and the viewport edge. */
const EDGE_MARGIN = 8

/** Elements inside the grab area that own the pointer — a drag never starts on one. */
const INTERACTIVE = 'button, a, input, select, textarea, [role="button"], [contenteditable="true"]'

type Offset = { x: number; y: number }

export type DialogDrag = {
  /** Ref callback for the popup element. */
  attachPopup: (node: HTMLDivElement | null) => void
  /** Inline `translate` for the popup. Tailwind's `-translate-*` utilities set the
   *  same property, so this replaces the centering offset instead of stacking on
   *  it — and leaves `transform` free for the open/close zoom animation. */
  style: React.CSSProperties
  onPointerDown: (e: React.PointerEvent) => void
}

/**
 * Lets a centered dialog be dragged by a grab area, within the viewport.
 *
 * Move and up listeners live on `window` — the pointer leaves the grab area on
 * any fast drag, and a capture-free drag also works in jsdom.
 */
export function useDialogDrag(enabled: boolean): DialogDrag | null {
  const popupRef = React.useRef<HTMLDivElement | null>(null)
  const [offset, setOffset] = React.useState<Offset>({ x: 0, y: 0 })

  // The popup unmounts on close while this hook's owner stays mounted, so the
  // reopened dialog would otherwise keep the old position.
  const attachPopup = React.useCallback((node: HTMLDivElement | null) => {
    popupRef.current = node
    if (!node) setOffset(o => (o.x === 0 && o.y === 0 ? o : { x: 0, y: 0 }))
  }, [])

  const clamp = React.useCallback(({ x, y }: Offset): Offset => {
    const el = popupRef.current
    if (!el) return { x, y }
    const { width, height } = el.getBoundingClientRect()
    // Offsets are relative to the centered position, so each axis has one symmetric limit.
    const limitX = Math.max(0, (window.innerWidth - width) / 2 - EDGE_MARGIN)
    const limitY = Math.max(0, (window.innerHeight - height) / 2 - EDGE_MARGIN)
    return {
      x: Math.min(limitX, Math.max(-limitX, x)),
      y: Math.min(limitY, Math.max(-limitY, y)),
    }
  }, [])

  const onPointerDown = React.useCallback((e: React.PointerEvent) => {
    if (e.button !== 0) return
    if ((e.target as HTMLElement).closest(INTERACTIVE)) return
    e.preventDefault() // no text selection under the drag
    const startX = e.clientX
    const startY = e.clientY
    const from = offset
    const onMove = (ev: PointerEvent) => {
      setOffset(clamp({ x: from.x + ev.clientX - startX, y: from.y + ev.clientY - startY }))
    }
    const onUp = () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }, [clamp, offset])

  // A shrinking window can strand a moved popup off-screen.
  React.useEffect(() => {
    if (!enabled) return
    const onResize = () => setOffset(o => (o.x === 0 && o.y === 0 ? o : clamp(o)))
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [enabled, clamp])

  if (!enabled) return null
  return {
    attachPopup,
    style: { translate: `calc(-50% + ${offset.x}px) calc(-50% + ${offset.y}px)` },
    onPointerDown,
  }
}
