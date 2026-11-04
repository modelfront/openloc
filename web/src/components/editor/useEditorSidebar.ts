import { useCallback, useEffect, useState } from 'react'
import { writeStorage } from '@/lib/safe-storage'
import { panelInsertApproves, panelInsertText, type PanelInsert } from '@/lib/panel-insert'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { usePanelResize } from '@/hooks/usePanelResize'
import { useLatestRef } from '@/hooks/useLatestRef'
import type { ActiveSegment } from './useActiveSegment'

/** Width the Resources panel opens at on a click, and its starting width. */
const SIDEBAR_CLICK_WIDTH = 320

interface Options {
  active: Pick<ActiveSegment, 'activeInsertRef'>
  /** The open segment's tagged source, for re-anchoring TM match tags on insert. */
  activeSourceRich: string
  canEdit: boolean
  plainTextFile: boolean
}

/**
 * The Resources panel: whether it is open, its width, and the inserts and
 * term clicks it sends into the open segment.
 */
export function useEditorSidebar({ active, activeSourceRich, canEdit, plainTextFile }: Options) {
  const { activeInsertRef } = active
  // Read by the inserts, which the keyboard path runs inside a keydown
  // handler: they must not close over state.
  const activeSourceRichRef = useLatestRef(activeSourceRich)
  const canEditRef = useLatestRef(canEdit)
  const plainTextFileRef = useLatestRef(plainTextFile)

  const [sidebarOpen, setSidebarOpen] = useState(() => { try { return localStorage.getItem('cat-sidebar-open') !== 'false' } catch { return true } })

  // A Resources-panel insert: the click on a TM match or MT suggestion, and
  // Ctrl+1..5. An exact TM match is approved too. It depends only on refs, so
  // it never changes.
  const handleSidebarInsert = useCallback((insert: PanelInsert) => {
    if (!canEditRef.current) return
    activeInsertRef.current.replaceAll?.(
      panelInsertText(insert, activeSourceRichRef.current, plainTextFileRef.current),
      insert.insertToken,
      panelInsertApproves(insert),
    )
  }, [activeInsertRef, activeSourceRichRef, canEditRef, plainTextFileRef])

  // Stable sidebar callbacks: they depend only on refs.
  const handleTermClick = useCallback((src: string, tgt: string, mt?: string) => activeInsertRef.current.selectAndReplace(src, tgt, mt), [activeInsertRef])
  const handleTermHover = useCallback((src: string, tgt: string, mt?: string) => activeInsertRef.current.selectTerm?.(src, tgt, mt), [activeInsertRef])
  const handleSidebarClose = useCallback(() => {
    setSidebarOpen(false)
    writeStorage('cat-sidebar-open', 'false')
  }, [])
  const handleSidebarOpen = useCallback(() => {
    setSidebarOpen(true)
    writeStorage('cat-sidebar-open', 'true')
  }, [])
  // Resources panel width. Dragging it all the way in closes the panel rather
  // than leaving a strip nothing fits in; past the maximum the segment grid is
  // the one that suffers.
  const sidebarResize = usePanelResize({
    storageKey: 'cat-sidebar-width',
    min: 30,
    max: 640,
    fallback: SIDEBAR_CLICK_WIDTH,
    label: 'Resize resources panel',
    onCollapse: handleSidebarClose,
    onExpand: handleSidebarOpen,
  })
  // Clicking the closed panel's tab opens it at SIDEBAR_CLICK_WIDTH, whatever
  // the last dragged width was. Dragging that tab instead chooses its own width.
  const openSidebarByClick = useCallback(() => {
    handleSidebarOpen()
    if (!sidebarResize.wasDragged()) sidebarResize.setWidth(SIDEBAR_CLICK_WIDTH)
  }, [handleSidebarOpen, sidebarResize])

  // Auto-collapse the Resources sidebar on narrow viewports (< 1200px) so the
  // segment grid keeps enough room. This is a display override, not a
  // preference change: we never write localStorage here, so widening the window
  // restores whatever the user last chose. The reopen handle still works as a
  // temporary manual override while narrow (it does persist, via handleSidebarOpen).
  const isNarrow = useMediaQuery('(max-width: 1199px)')
  useEffect(() => {
    if (isNarrow) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- follows the viewport width, an outside value
      setSidebarOpen(false)
    } else {
      try { setSidebarOpen(localStorage.getItem('cat-sidebar-open') !== 'false') } catch { setSidebarOpen(true) }
    }
  }, [isNarrow])

  return {
    sidebarOpen,
    sidebarResize,
    openSidebarByClick,
    handleSidebarClose,
    handleSidebarInsert,
    handleTermClick,
    handleTermHover,
  }
}

export type EditorSidebarState = ReturnType<typeof useEditorSidebar>
