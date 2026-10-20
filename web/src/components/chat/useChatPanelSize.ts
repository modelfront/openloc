// Size of the assistant panel: dragged from its top-left corner, clamped to
// the viewport, remembered in localStorage.
import { useCallback, useRef, useState } from 'react'
import { readStorage, writeStorage } from '@/lib/safe-storage'

// Resize bounds. Min keeps the header/input usable; max is clamped live
// against the viewport so a size persisted on a big monitor can't off-screen
// the panel on a laptop.
export const DEFAULT_SIZE = { w: 400, h: 560 }
const MIN_SIZE = { w: 320, h: 400 }
const SIZE_STORAGE_KEY = 'openloc-chat-panel-size'

function loadSize(): { w: number; h: number } {
  const raw = readStorage(SIZE_STORAGE_KEY)
  if (!raw) return DEFAULT_SIZE
  let parsed: { w?: unknown; h?: unknown } | null
  try {
    parsed = JSON.parse(raw)
  } catch {
    return DEFAULT_SIZE
  }
  if (typeof parsed?.w !== 'number' || typeof parsed?.h !== 'number') return DEFAULT_SIZE
  return { w: Math.max(MIN_SIZE.w, parsed.w), h: Math.max(MIN_SIZE.h, parsed.h) }
}

// Saved once, when a drag ends — not on every pointer move.
function saveSize(size: { w: number; h: number }) {
  writeStorage(SIZE_STORAGE_KEY, JSON.stringify(size))
}

function clampSize(w: number, h: number): { w: number; h: number } {
  // Viewport bounds mirror the max-w/max-h classes on the panel.
  const maxW = Math.max(MIN_SIZE.w, window.innerWidth - 40)
  const maxH = Math.max(MIN_SIZE.h, window.innerHeight - 96)
  return {
    w: Math.min(Math.max(MIN_SIZE.w, w), maxW),
    h: Math.min(Math.max(MIN_SIZE.h, h), maxH),
  }
}

export function useChatPanelSize() {
  const [size, setSize] = useState(loadSize)
  // Live during a drag; ref (not state) so pointermove doesn't re-render stale closures.
  const dragRef = useRef<{ startX: number; startY: number; startW: number; startH: number } | null>(null)

  // The panel is anchored bottom-right, so dragging the top-left handle LEFT
  // and UP grows it — deltas are inverted. Listeners live on window so the
  // drag survives the pointer leaving the 12px handle (it always does).
  const onResizeStart = useCallback((e: React.PointerEvent) => {
    e.preventDefault()
    dragRef.current = { startX: e.clientX, startY: e.clientY, startW: size.w, startH: size.h }
    const onMove = (ev: PointerEvent) => {
      const d = dragRef.current
      if (!d) return
      setSize(clampSize(d.startW + (d.startX - ev.clientX), d.startH + (d.startY - ev.clientY)))
    }
    const onUp = () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      if (dragRef.current) {
        // setSize is async; recompute the final value for persistence.
        setSize(current => {
          saveSize(current)
          return current
        })
      }
      dragRef.current = null
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }, [size.w, size.h])
  return { size, onResizeStart }
}
