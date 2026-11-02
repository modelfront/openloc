import { useCallback, useEffect, useRef } from 'react'
import { plainTextOf } from '../lib/source-caret'

/** The CSS highlight (`::highlight(...)` in index.css) that marks the
 *  remembered source selection. */
export const SOURCE_SELECTION_HIGHLIGHT = 'term-source-selection'

/** The browser's highlight registry, or null where the CSS Custom Highlight
 *  API is missing: there the selection is remembered but not marked. */
function highlightRegistry(): HighlightRegistry | null {
  return typeof CSS !== 'undefined' && CSS.highlights && typeof Highlight !== 'undefined' ? CSS.highlights : null
}

/**
 * Remembers the text the user last selected in the active row's source, as
 * plain source text (lib/source-caret.ts), for the term quick-add. The text
 * stays when the selection moves out of the source — into the translation, or
 * onto a button — and is dropped when the user clicks elsewhere in the source,
 * the row closes, or `clear` runs. While remembered, the range is painted with
 * a CSS highlight, which leaves the source's DOM untouched, so the user still
 * sees which words the quick-add will use. `fieldRef` goes on the element that
 * renders the source.
 */
export function useSourceSelection(enabled: boolean) {
  const fieldRef = useRef<HTMLElement | null>(null)
  const textRef = useRef('')
  const markRef = useRef<Highlight | null>(null)

  const clear = useCallback(() => {
    textRef.current = ''
    const registry = highlightRegistry()
    // Delete only our own mark: the next row may already have set its own.
    if (registry && markRef.current && registry.get(SOURCE_SELECTION_HIGHLIGHT) === markRef.current) {
      registry.delete(SOURCE_SELECTION_HIGHLIGHT)
    }
    markRef.current = null
  }, [])

  useEffect(() => {
    clear()
    if (!enabled) return
    const onSelectionChange = () => {
      const field = fieldRef.current
      const sel = document.getSelection()
      if (!field || !sel || sel.rangeCount === 0) return
      const range = sel.getRangeAt(0)
      if (!field.contains(range.startContainer) || !field.contains(range.endContainer)) return
      const text = sel.isCollapsed ? '' : plainTextOf(range.cloneContents()).trim()
      if (!text) { clear(); return }
      textRef.current = text
      const registry = highlightRegistry()
      if (!registry) return
      // The live range follows the selection; the mark keeps a copy.
      markRef.current = new Highlight(range.cloneRange())
      registry.set(SOURCE_SELECTION_HIGHLIGHT, markRef.current)
    }
    document.addEventListener('selectionchange', onSelectionChange)
    return () => {
      document.removeEventListener('selectionchange', onSelectionChange)
      clear()
    }
  }, [enabled, clear])

  const getText = useCallback(() => textRef.current, [])
  return { fieldRef, getText, clear }
}
