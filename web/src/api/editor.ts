import { request } from './request'
import type { Segment, Translation } from './projects'
import type { Index, Ref, SearchResult, WordCandidate } from '../types/editor'
import type { Placeable } from '../types/placeable'

export type { Index as EditorIndex, Ref as EditorRef, WordCandidate } from '../types/editor'

/** One segment with its translation in the job's language, if any. */
export interface EditorRow {
  segment: Segment
  translation: Translation | null
  /** The text the translation held when its last finished workflow step
   *  round ended: the track-changes baseline. Null when there is none. */
  previous_step_text: string | null
  /** The source's placeables, in text order, for the editor to mark and
   *  offer for insertion. */
  placeables: Placeable[]
}

/** Rows one request may ask for. Mirrors editor.MaxRows. */
export const EDITOR_MAX_ROWS = 500

export const getEditorIndex = (jobId: number) =>
  request<{ data: Index }>(`/jobs/${jobId}/editor/index`).then(r => r.data)

/** These segments with their translations, in document order. */
export const getEditorRows = (jobId: number, segmentIds: readonly number[]) =>
  request<{ data: EditorRow[] }>(`/jobs/${jobId}/editor/rows?segment_ids=${segmentIds.join(',')}`).then(r => r.data)

export interface EditorSearch {
  text: string
  source: boolean
  target: boolean
  caseSensitive: boolean
  regex: boolean
}

/** Segment ids whose text matches, in document order. */
export const searchEditor = (jobId: number, s: EditorSearch, signal?: AbortSignal) => {
  const params = new URLSearchParams({
    q: s.text,
    source: s.source ? '1' : '0',
    target: s.target ? '1' : '0',
    case: s.caseSensitive ? '1' : '0',
    regex: s.regex ? '1' : '0',
  })
  return request<{ data: SearchResult }>(`/jobs/${jobId}/editor/search?${params}`, { signal })
    .then(r => r.data.segment_ids)
}

export const getEditorRefs = (jobId: number) =>
  request<{ data: Ref[] }>(`/jobs/${jobId}/editor/refs`).then(r => r.data)

/** Translations whose target contains `word` anywhere; the caller narrows to whole words. */
export const getWordCandidates = (jobId: number, word: string) =>
  request<{ data: WordCandidate[] }>(`/jobs/${jobId}/editor/word?word=${encodeURIComponent(word)}`).then(r => r.data)
