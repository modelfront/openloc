import type { RefObject } from 'react'
import { observeElementOffset, useVirtualizer } from '@tanstack/react-virtual'

/** The nearest ancestor that scrolls vertically: what moves the table. */
function scrollParentOf(el: HTMLElement | null): HTMLElement | null {
  for (let p = el?.parentElement; p; p = p.parentElement) {
    const { overflowY } = getComputedStyle(p)
    if (overflowY === 'auto' || overflowY === 'scroll') return p
  }
  return null
}

/**
 * Row windowing for a table that grows the page instead of scrolling itself:
 * the scroll element is the nearest scrolling ancestor, and the offset is the
 * distance scrolled past the table's own top (negative while the table starts
 * below the scroller's top), read on every scroll. Rows are `rowHeight` px
 * each, not measured: a measured row changes the table's height under a
 * scroll in progress. `rowHeight` undefined turns it off and returns null.
 */
export function usePageRowVirtualizer(boxRef: RefObject<HTMLElement | null>, count: number, rowHeight: number | undefined) {
  const enabled = rowHeight !== undefined
  const virtualizer = useVirtualizer({
    count: enabled ? count : 0,
    enabled,
    getScrollElement: () => scrollParentOf(boxRef.current),
    estimateSize: () => rowHeight ?? 0,
    overscan: 10,
    observeElementOffset: (instance, cb) => observeElementOffset(instance, (_offset, isScrolling) => {
      const box = boxRef.current
      const scroller = instance.scrollElement
      cb(box && scroller ? scroller.getBoundingClientRect().top - box.getBoundingClientRect().top : 0, isScrolling)
    }),
  })
  return enabled ? virtualizer : null
}
