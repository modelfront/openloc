import { request } from './request'
import type { Segment, Translation } from './projects'
import type { FuzzyMatch, Index, Ref, SearchResult, WordCandidate } from '../types/editor'
import type { JobResult } from '../types/scopecomplete'
import type { Placeable } from '../types/placeable'
import { editorApiPath, type EditorId } from '../lib/editor-scope'

export type { Index as EditorIndex, Ref as EditorRef, WordCandidate, FuzzyMatch, ScopeFile, EditBlock } from '../types/editor'
export type { JobResult as ScopeCompleteResult } from '../types/scopecomplete'

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

export const getEditorIndex = (id: EditorId) =>
  request<{ data: Index }>(editorApiPath(id, 'index')).then(r => r.data)

/** These segments with their translations, in scope order. */
export const getEditorRows = (id: EditorId, segmentIds: readonly number[]) =>
  request<{ data: EditorRow[] }>(editorApiPath(id, 'rows', `segment_ids=${segmentIds.join(',')}`)).then(r => r.data)

export interface EditorSearch {
  text: string
  source: boolean
  target: boolean
  caseSensitive: boolean
  regex: boolean
}

/** Segment ids whose text matches, in scope order. */
export const searchEditor = (id: EditorId, s: EditorSearch, signal?: AbortSignal) => {
  const params = new URLSearchParams({
    q: s.text,
    source: s.source ? '1' : '0',
    target: s.target ? '1' : '0',
    case: s.caseSensitive ? '1' : '0',
    regex: s.regex ? '1' : '0',
  })
  return request<{ data: SearchResult }>(editorApiPath(id, 'search', params), { signal })
    .then(r => r.data.segment_ids)
}

export const getEditorRefs = (id: EditorId) =>
  request<{ data: Ref[] }>(editorApiPath(id, 'refs')).then(r => r.data)

/** Translations whose target contains `word` anywhere; the caller narrows to whole words. */
export const getWordCandidates = (id: EditorId, word: string) =>
  request<{ data: WordCandidate[] }>(editorApiPath(id, 'word', `word=${encodeURIComponent(word)}`)).then(r => r.data)

/** Confirmed segments of the editor's files whose source is like this segment's, best first. */
export const getEditorFuzzy = (id: EditorId, segmentId: number) =>
  request<{ data: FuzzyMatch[] }>(editorApiPath(id, 'fuzzy', `segment_id=${segmentId}`)).then(r => r.data)

/** Open comments per translation of the editor's files; none: left out. */
export const getEditorCommentCounts = (id: EditorId) =>
  request<{ data: { translation_id: number; comment_count: number }[] }>(editorApiPath(id, 'comment-counts')).then(r => r.data)

/**
 * Complete the caller's step on each file of a multi-file editor (all of
 * them, or `job_ids`), one by one: a result per file, in editor order.
 * `steps` names the step in progress the caller saw per job (the index's
 * `step_id`); a job whose step moved on since is left alone, so sending the
 * request again never advances a job twice.
 */
export const completeEditorScope = (id: EditorId, body: {
  comment: string
  approve_all: boolean
  job_ids?: number[]
  steps?: Record<number, number>
}) =>
  request<{ data: JobResult[] }>(editorApiPath(id, 'complete'), { method: 'POST', body: JSON.stringify(body) }).then(r => r.data)
