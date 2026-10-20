import { createContext, useCallback, useContext, useEffect, useMemo, useRef, type ReactNode, type RefObject } from 'react'

/**
 * Which layer a press outside belongs to.
 *
 * A popover opened inside a dialog portals its popup to the document body, so
 * a press on the dialog's backdrop lands outside BOTH of them. Each reads it
 * as its own outside-press and one click closes the picker the user was aiming
 * at *and* the form underneath it.
 *
 * The innermost open layer owns the press. Popups report themselves here while
 * open; a dialog asks before letting an outside press close it, and closes
 * normally when nothing is stacked on top.
 *
 * A ref rather than state: nothing renders differently, and opening a picker
 * must not re-render the form around it.
 */
const OpenPopupCount = createContext<{ change: (delta: number) => void } | null>(null)

/**
 * Marks a layer that nested popups belong to. Anything rendered inside reports
 * its open state to `countRef`.
 */
export function OpenPopupBoundary({ countRef, children }: { countRef: RefObject<number>; children: ReactNode }) {
  const layer = useMemo(() => ({ change: (delta: number) => { countRef.current += delta } }), [countRef])
  return <OpenPopupCount.Provider value={layer}>{children}</OpenPopupCount.Provider>
}

/**
 * Returns a reporter for a popup's open state. A no-op outside a boundary, so
 * every popover can call it unconditionally.
 *
 * Reports are idempotent and the popup is un-reported on unmount — a picker
 * torn down while open must not leave the count stuck above zero, which would
 * make its dialog undismissable.
 */
export function useReportPopupOpen(): (open: boolean) => void {
  const layer = useContext(OpenPopupCount)
  const reported = useRef(false)

  useEffect(() => () => {
    if (reported.current && layer) {
      layer.change(-1)
      reported.current = false
    }
  }, [layer])

  return useCallback((open: boolean) => {
    if (!layer || open === reported.current) {
      return
    }
    layer.change(open ? 1 : -1)
    reported.current = open
  }, [layer])
}
