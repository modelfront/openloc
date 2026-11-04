import { useMemo } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import { useSegmentSelection } from '@/hooks/useSegmentSelection'
import { useLatestRef } from '@/hooks/useLatestRef'
import { isMac } from '@/components/SegmentRow'
import { translatedRows, type IndexLookup, type IndexRow } from '@/lib/editor-index'

/**
 * Multi-segment selection, by translation id, over the rows the filter
 * shows: the selection itself, a ref for the window key handler, and the
 * picked rows in index order.
 */
export function useEditorSelection(rows: IndexRow[], filteredIndices: number[], indexLookup: IndexLookup) {
  const orderedTranslationIds = useMemo(
    () => filteredIndices.flatMap(i => rows[i].translationId ?? []),
    [filteredIndices, rows],
  )
  const selection = useSegmentSelection(orderedTranslationIds)
  const selectionRef = useLatestRef(selection)
  const indexRowByTranslation = useMemo(
    () => new Map(translatedRows(rows).map(r => [r.translationId!, r])),
    [rows],
  )
  const pickedSet = useMemo(() => new Set(selection.picked), [selection.picked])
  const pickedRows = useMemo(
    () => selection.picked.flatMap(id => indexLookup.byTranslation.get(id) ?? []).sort((a, b) => a - b),
    [selection.picked, indexLookup],
  )

  return { selection, selectionRef, orderedTranslationIds, indexRowByTranslation, pickedSet, pickedRows }
}

export type SegmentSelection = ReturnType<typeof useSegmentSelection>

/** Cmd+click (Mac) / Ctrl+click elsewhere, outside the open editor's text. */
export function isSelectToggleClick(e: ReactMouseEvent): boolean {
  const mod = isMac ? e.metaKey : e.ctrlKey
  return mod && !(e.target as HTMLElement | null)?.closest('.ProseMirror')
}
