import { useCallback, useEffect, useRef } from 'react'

/**
 * Longest selection we'll carry into a lookup. Concordance is a substring
 * search — a whole paragraph never matches anything, it just looks broken.
 */
export const MAX_SELECTION_LEN = 200

/** The slice of `Selection` this module needs. Keeps the reducer unit-testable. */
export interface SelectionLike {
  isCollapsed: boolean
  anchorNode: Node | null
  toString(): string
}

/**
 * Pure state transition for the remembered selection. Exported for tests — the
 * hook below is a thin `selectionchange` wrapper around this.
 *
 * The collapse case is why this exists at all: a `<button>`'s mousedown collapses
 * the document selection *before* its click handler runs, so a live
 * `window.getSelection()` read from a click is always empty. Hence the asymmetry
 * — keep the remembered value when the selection collapses OUTSIDE the container
 * (a toolbar click; the user hasn't changed their mind about what they selected),
 * drop it when it collapses INSIDE (they clicked into another segment, so the old
 * phrase is stale).
 */
export function nextSelectionValue(
  prev: string,
  sel: SelectionLike | null,
  container: Node | null,
): string {
  if (!sel) return prev
  if (sel.isCollapsed) return selectionInside(sel, container) ? '' : prev
  if (!selectionInside(sel, container)) return prev
  const text = sel.toString().trim()
  if (!text || text.length > MAX_SELECTION_LEN) return prev
  return text
}

/** True when the selection's anchor sits inside `container`. */
export function selectionInside(sel: SelectionLike | null, container: Node | null): boolean {
  return !!(sel && container && sel.anchorNode && container.contains(sel.anchorNode))
}

/**
 * Remembers the most recent non-empty text selection made inside `containerRef`,
 * so click-driven affordances (toolbar buttons, menu items) can act on what the
 * user highlighted instead of on an already-collapsed selection.
 *
 * Deliberately a ref, not state: nothing renders from it, and a selection-driven
 * re-render on every mouse drag over a virtualized segment list would be rough.
 */
export function useLastSelection(containerRef: React.RefObject<HTMLElement | null>) {
  const lastRef = useRef('')

  useEffect(() => {
    const onSelectionChange = () => {
      lastRef.current = nextSelectionValue(
        lastRef.current,
        document.getSelection(),
        containerRef.current,
      )
    }
    document.addEventListener('selectionchange', onSelectionChange)
    return () => document.removeEventListener('selectionchange', onSelectionChange)
  }, [containerRef])

  /**
   * Live selection if there is one, else the last one we saw.
   *
   * A live range inside the container wins even when it's rejected for being
   * over MAX_SELECTION_LEN — it returns '' rather than deferring to the
   * remembered phrase. Falling back there would search something the user
   * highlighted earlier and has visibly moved on from.
   */
  const getSelectionText = useCallback((): string => {
    const live = document.getSelection()
    if (live && !live.isCollapsed && selectionInside(live, containerRef.current)) {
      return nextSelectionValue('', live, containerRef.current)
    }
    return lastRef.current
  }, [containerRef])

  return { getSelectionText }
}
