import { useEffect } from 'react'
import type { RefObject } from 'react'
import type { TMMatch } from '@/api'
import { isMac } from '@/components/SegmentRow'
import type { PanelInsert } from '@/lib/panel-insert'
import { useLatestRef } from '@/hooks/useLatestRef'
import type { ActiveSegment } from './useActiveSegment'

interface Options {
  active: Pick<ActiveSegment, 'activeSegmentIdxRef' | 'activeInsertRef'>
  canEdit: boolean
  /** The open segment's usable TM matches, for Ctrl+1..5. */
  tmMatches: TMMatch[]
  segmentSearchRef: RefObject<HTMLInputElement | null>
  /** The segment selection's current state, read when Escape is pressed. */
  selectionRef: RefObject<{ selected: ReadonlySet<number>; clear: () => void }>
  openConcordance: () => void
  openFindReplace: (inSelection: boolean) => void
  openJump: () => void
  openQuickAddTerm: () => Promise<void>
  handleSidebarInsert: (insert: PanelInsert) => void
}

/**
 * Global keyboard shortcuts (editor level).
 *
 * Segment-level shortcuts (Mod+Enter confirm, Mod+Shift+S
 * copy-source) must work when focus is on a sidebar button, not only inside
 * ProseMirror. The active SegmentRow populates `activeInsertRef` with
 * `confirm`/`copySource` handlers and this window-level listener routes to
 * them regardless of DOM focus. Skipped inside form inputs (F&R search,
 * etc.) and when TipTap has already handled the event, to avoid
 * double-firing.
 */
export function useEditorShortcuts({
  active, canEdit, tmMatches, segmentSearchRef, selectionRef,
  openConcordance, openFindReplace, openJump, openQuickAddTerm, handleSidebarInsert,
}: Options) {
  const { activeSegmentIdxRef, activeInsertRef } = active
  const canEditRef = useLatestRef(canEdit)
  // Mod+1..5 reads the hits through a ref so the window keydown listener never
  // closes over a stale array.
  const tmMatchesRef = useLatestRef(tmMatches)

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const mod = isMac ? e.metaKey : e.ctrlKey
      if (mod && e.key === 'k') {
        e.preventDefault()
        openConcordance()
      }
      // The browser's find sees only the rows on screen; the editor's search
      // covers every segment of the file.
      if (mod && !e.shiftKey && !e.altKey && e.key === 'f') {
        e.preventDefault()
        segmentSearchRef.current?.focus()
        segmentSearchRef.current?.select()
      }
      if (mod && e.key === 'h') {
        e.preventDefault()
        openFindReplace(false)
      }
      if (mod && e.key === 'g') {
        e.preventDefault()
        openJump()
      }
      // Ctrl+1..5 — insert TM match from sidebar, the same insert as a click.
      if (mod && e.key >= '1' && e.key <= '5' && canEditRef.current) {
        const idx = parseInt(e.key) - 1
        const m = tmMatchesRef.current[idx]
        if (m) {
          e.preventDefault()
          handleSidebarInsert({ kind: 'tm', match: m, insertToken: m.insert_token })
        }
      }

      // ── Segment-level shortcuts (Mod+Enter confirm, Mod+Shift+S copy-source) ──
      // Only route to activeInsertRef if:
      //   1. An active segment exists
      //   2. Target is NOT inside ProseMirror (let TipTap's keymap handle that
      //      case via its own addKeyboardShortcuts — avoids double-fire)
      //   3. Target is NOT a form control (INPUT/TEXTAREA/SELECT) — those
      //      handle their own Enter key (e.g. F&R search field, jump-to
      //      segment input, any <select> dropdown)
      const target = e.target as HTMLElement | null
      const inProseMirror = !!target?.closest('.ProseMirror')
      const tag = target?.tagName
      const inFormInput = tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT'
      const canRouteToActive = activeSegmentIdxRef.current !== null && !inProseMirror && !inFormInput

      // Escape clears the segment selection before anything else closes —
      // unless it belongs to the open editor, a text field or a dialog. A
      // focused checkbox (the selection's own) does not count as a text field.
      const inTextField = inFormInput && !(target instanceof HTMLInputElement && target.type === 'checkbox')
      if (e.key === 'Escape' && !inProseMirror && !inTextField && !target?.closest('[role="dialog"],[role="alertdialog"]')
        && selectionRef.current.selected.size > 0) {
        e.preventDefault()
        selectionRef.current.clear()
        return
      }

      if (canRouteToActive && mod && e.key === 'Enter') {
        if (activeInsertRef.current.confirm) {
          e.preventDefault()
          activeInsertRef.current.confirm()
        }
      }
      if (canRouteToActive && mod && e.shiftKey && (e.key === 'S' || e.key === 's')) {
        if (activeInsertRef.current.copySource) {
          e.preventDefault()
          activeInsertRef.current.copySource()
        }
      }
      if (canRouteToActive && mod && e.shiftKey && (e.key === 'Backspace' || e.key === 'Delete')) {
        if (activeInsertRef.current.clear) {
          e.preventDefault()
          activeInsertRef.current.clear()
        }
      }
      // Tab/Shift+Tab and Down/Up from a read-only row: its editor takes no
      // focus or keys, so they land here on the page itself (a writable
      // editor handles them in its keymap). Focus on a control keeps them.
      const leave = activeInsertRef.current.leaveReadOnly
      if (leave && (!target || target === document.body) && !e.altKey && !mod) {
        if (e.key === 'Tab') {
          e.preventDefault()
          leave(e.shiftKey ? 'prev' : 'next', false)
        } else if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !e.shiftKey) {
          e.preventDefault()
          leave(e.key === 'ArrowDown' ? 'next' : 'prev', true)
        }
      }
      // Alt+Shift+T split, Alt+Shift+J join with next: TipTap has no binding
      // for them, so they also run from inside the editor. Matched on the
      // physical key: on a Mac, Alt+Shift turns e.key into another character.
      if (activeSegmentIdxRef.current !== null && !inFormInput && e.altKey && e.shiftKey && !mod
        && (e.code === 'KeyT' || e.code === 'KeyJ')) {
        const run = e.code === 'KeyT' ? activeInsertRef.current.split : activeInsertRef.current.joinNext
        if (run) {
          e.preventDefault()
          run()
        }
      }
      // Alt+Shift+G adds a term, from inside the editor too.
      if (activeSegmentIdxRef.current !== null && !inFormInput && e.altKey && e.shiftKey && !mod && e.code === 'KeyG') {
        e.preventDefault()
        void openQuickAddTerm()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [openConcordance, handleSidebarInsert, openQuickAddTerm, openFindReplace, openJump, activeSegmentIdxRef, activeInsertRef, segmentSearchRef, selectionRef, canEditRef, tmMatchesRef])
}
