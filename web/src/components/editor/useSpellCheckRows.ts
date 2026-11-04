import { useMemo } from 'react'
import { translatedRows, type IndexRow } from '@/lib/editor-index'

/** What the spell check dialog reads from the editor's index: every segment
 *  in grid order, and each segment's translation. */
export function useSpellCheckRows(rows: readonly IndexRow[]) {
  const segmentIds = useMemo(() => rows.map(r => r.segmentId), [rows])
  const translationBySegment = useMemo(() => new Map(translatedRows(rows).map(r => [
    r.segmentId, { id: r.translationId!, status: r.status, locked: r.locked },
  ])), [rows])
  return { segmentIds, translationBySegment }
}
