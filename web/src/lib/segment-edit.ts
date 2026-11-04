import type { QueryClient } from '@tanstack/react-query'
import type { EditorRow } from '../api'
import type { IndexRow } from './editor-index'
import type { EditorId } from './editor-scope'
import { queryKeys } from './query-keys'

/**
 * The version (updated_at) of one of the editor's loaded translations, read from
 * the editor's row cache when called: a draft save patches the cache before
 * the editor renders again, so this is the version a write must carry.
 * Undefined when no loaded page holds the translation.
 */
export function loadedVersion(queryClient: QueryClient, editorId: EditorId, translationId: number): string | undefined {
  for (const [, rows] of queryClient.getQueriesData<EditorRow[]>({ queryKey: queryKeys.job.editorRows(editorId) })) {
    const t = rows?.find(r => r.translation?.id === translationId)?.translation
    if (t) return t.updated_at
  }
  return undefined
}

/** True when a join with the row after `idx` may be offered: the next piece
 *  of a split segment, or the next sentence of the same paragraph. The server
 *  checks the join itself. */
export function joinsWithNext(rows: readonly Pick<IndexRow, 'joinsNext'>[], idx: number): boolean {
  return idx + 1 < rows.length && rows[idx].joinsNext
}

/**
 * Why the rows at these indices cannot be joined, or null when they may be:
 * at least two rows that follow each other in one paragraph of one file.
 */
export function joinRefusal(rows: readonly (Pick<IndexRow, 'joinsNext'> & { fileIdx?: number })[], indices: readonly number[]): string | null {
  if (indices.length < 2) return 'Select at least two rows to join.'
  const sorted = [...indices].sort((a, b) => a - b)
  if (new Set(sorted.map(i => rows[i].fileIdx)).size > 1) return 'Only rows of one file can be joined.'
  for (let i = 1; i < sorted.length; i++) {
    if (sorted[i] !== sorted[i - 1] + 1) return 'Only rows that follow each other can be joined.'
    if (!joinsWithNext(rows, sorted[i - 1])) return 'Only sentences of one paragraph can be joined.'
  }
  return null
}
