import { useCallback, useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'
import type { TermLookupResult } from '@/api'
import type { SegmentRowHandle } from '@/components/SegmentRow'
import type { EntryCaret } from '@/lib/entry-caret'
import { useLatestRef } from '@/hooks/useLatestRef'

/**
 * The open segment of the editor grid: its index, the refs the open
 * SegmentRow fills, and `switchActiveSegment`, the one way to change it.
 * A mousedown outside the grid, the sidebar and every dialog closes it.
 */
export function useActiveSegment(segmentScrollRef: RefObject<HTMLDivElement | null>) {
  const [activeSegmentIdx, setActiveSegmentIdx] = useState<number | null>(null)
  // Where the open row's caret starts when an arrow key entered it from the
  // row above or below, or a click on its translation opened it; every other
  // switch clears it.
  const [entryCaret, setEntryCaret] = useState<{ idx: number; caret: EntryCaret } | null>(null)
  // For the window handlers and switchActiveSegment, which must not close
  // over state.
  const activeSegmentIdxRef = useLatestRef(activeSegmentIdx)
  const activeRowDirtyRef = useRef(false) // tracks if active segment has unsaved edits

  const [activeTerms, setActiveTerms] = useState<TermLookupResult[]>([])
  const [activeEditText, setActiveEditText] = useState('')
  // The deactivating row can't clear these itself — it only
  // receives `onTermsLoaded`/`onEditTextChange` WHILE it's the active row (see
  // the row props in SegmentGrid), so its deactivate cleanup sees them
  // `undefined` and its `?.([])` is a no-op. Clear at the owning layer when
  // nothing is active.
  useEffect(() => {
    if (activeSegmentIdx === null) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- the closing row reports into this state and cannot clear it itself
      setActiveTerms([])
      setActiveEditText('')
    }
  }, [activeSegmentIdx])

  // Ref populated by the active SegmentRow with its imperative handlers.
  // The editor's window-level keyboard shortcuts (Ctrl+1..5, Mod+Enter,
  // Mod+Shift+S) route into these handlers so shortcuts work regardless of
  // whether focus is currently inside the ProseMirror editor or on a
  // sidebar/panel button. The editor's own TipTap keymap still
  // handles these when focus IS inside the editor — the window handler
  // skips via a `.ProseMirror` closest() check to avoid double-firing.
  const activeInsertRef = useRef<SegmentRowHandle>({ selectAndReplace: () => {}, selectTerm: () => {} })
  const flushDraftRef = useRef<() => Promise<void>>(async () => {})

  // Single chokepoint for changing the active segment. Every
  // transition (row click, Tab, Cmd+G, F&R navigate, click-outside) flushes
  // the previously-active row's draft FIRST, then sets the new index. The
  // discard option is reserved for Escape — the only "abandon edit" gesture.
  // Never call `setActiveSegmentIdx` directly: a bypass skips the flush and
  // loses the edit. The lint rule in web/eslint.config.js pins direct setter
  // calls to this helper.
  const switchActiveSegment = useCallback(async (
    newIdx: number | null,
    opts: { discard?: boolean; caret?: EntryCaret } = {},
  ): Promise<void> => {
    if (
      !opts.discard &&
      activeSegmentIdxRef.current !== null &&
      activeRowDirtyRef.current
    ) {
      try {
        await flushDraftRef.current()
      } catch (err) {
        // saveDraft already toasts on conflict (409) and other errors; we just
        // don't want a flush failure to block the transition.
        console.warn('switchActiveSegment: flushDraft failed:', err)
      }
    }
    activeRowDirtyRef.current = false
    setEntryCaret(opts.caret && newIdx !== null ? { idx: newIdx, caret: opts.caret } : null)
    // The ONLY legitimate setActiveSegmentIdx call site, gated by the flush invariant above.
    // eslint-disable-next-line no-restricted-syntax
    setActiveSegmentIdx(newIdx)
  }, [activeSegmentIdxRef])

  // Click outside active segment → deactivate.
  // Route through `switchActiveSegment(null)` so the flush-on-transition
  // behavior matches every other exit path (row click, Tab, F&R navigate, etc.).
  // The dirty check + flush + setter live in the chokepoint helper.
  useEffect(() => {
    const handleMouseDown = (e: MouseEvent) => {
      const idx = activeSegmentIdxRef.current
      if (idx === null) return
      const target = e.target as HTMLElement
      // Don't deactivate if clicking inside the editor, the sidebar, or a button
      if (target.closest('[data-testid="segment-editor"]') || target.closest('[data-term-panel]') || target.closest('[data-testid="editor-sidebar"]') || target.closest('button')) return
      // Don't deactivate if clicking inside any portaled dialog (Jump, Step Completion,
      // Step Rejection, Keyboard shortcuts, Analysis, conflict — anything Radix portals
      // to <body>). Without this, mousedown bubbles past the portal subtree to document
      // and trips the deactivation path.
      if (target.closest('[data-slot$="-portal"]')) return
      // Popovers portal to <body> without a "-portal" slot; their popup carries
      // data-slot="popover-content". The QA finding popover lives inside a
      // segment row, and a mousedown on its text would otherwise deactivate the
      // row here while the React click, bubbling through the portal to the row,
      // re-activates it — a needless editor unmount/remount on every click.
      if (target.closest('[data-slot="popover-content"]')) return
      // Same for the inline spell fix panel — portaled to <body> too, and its
      // non-<button> parts (source text, the "Change to" input) must not
      // deactivate the segment the panel is fixing.
      if (target.closest('[data-spellcheck-menu]')) return
      // Don't deactivate if clicking inside the segment list (gaps between rows, row borders, etc.)
      // Row clicks will fire onActivate to switch segments; only deactivate for clicks fully outside.
      if (segmentScrollRef.current?.contains(target)) return
      void switchActiveSegment(null)
    }
    document.addEventListener('mousedown', handleMouseDown)
    return () => document.removeEventListener('mousedown', handleMouseDown)
  }, [switchActiveSegment, segmentScrollRef, activeSegmentIdxRef])

  return {
    activeSegmentIdx,
    activeSegmentIdxRef,
    entryCaret,
    activeRowDirtyRef,
    activeTerms,
    setActiveTerms,
    activeEditText,
    setActiveEditText,
    activeInsertRef,
    flushDraftRef,
    switchActiveSegment,
  }
}

export type ActiveSegment = ReturnType<typeof useActiveSegment>
