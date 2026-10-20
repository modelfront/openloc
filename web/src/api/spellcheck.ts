import { request } from './request'
import { taskReopenedOf, type TaskReopenedFlag, type Translation } from './projects'

// --- Spell check (LanguageTool) ---

export interface SpellMatch {
  message: string
  /**
   * Offset into the segment's plain text, in UTF-16 code units. JavaScript
   * strings are UTF-16 too, so these index `String.prototype.slice` directly —
   * but they are NOT ProseMirror positions. Convert with `plainOffsetToPm`
   * (lib/pm-offset.ts) before touching the editor.
   */
  offset: number
  length: number
  replacements: { value: string }[]
  rule: {
    id: string
    description: string
    category: { id: string; name: string }
  }
}

export interface SpellcheckSegmentResult {
  translation_id: number
  matches: SpellMatch[]
}

export interface SpellcheckJobResult {
  results: SpellcheckSegmentResult[]
  /** Segments LanguageTool failed on (outage, rate limit, unsupported language). */
  failed: number
}

/**
 * Single-segment check — backs the live underlines while typing. With a job id
 * the server drops words in that job's project dictionary, so the underlines
 * agree with the review dialog.
 */
export const checkSpelling = (text: string, language: string, jobId?: number) =>
  request<{ matches: SpellMatch[] }>('/spellcheck', {
    method: 'POST',
    body: JSON.stringify({ text, language, ...(jobId !== undefined ? { job_id: jobId } : {}) }),
  }).then(r => r.matches ?? [])

/**
 * Check one chunk of a job's translations. The caller chunks so it can render
 * progress and support cancellation; the server caps the chunk size. The server
 * skips locked segments unless `includeLocked` is set.
 */
export const checkJobSpelling = (
  jobId: number,
  translationIds: number[],
  signal?: AbortSignal,
  opts: { includeLocked?: boolean } = {},
) =>
  request<{ data: SpellcheckJobResult }>(`/jobs/${jobId}/spellcheck`, {
    method: 'POST',
    body: JSON.stringify({
      translation_ids: translationIds,
      ...(opts.includeLocked ? { include_locked: true } : {}),
    }),
    signal,
  }).then(r => r.data)

// --- Per-project dictionary (what "Ignore all" persists to) ---

export interface SpellcheckDictionaryEntry {
  id: number
  tenant_id: number
  project_id: number
  language: string
  word: string
  created_by: number | null
  created_at: string
}

export const listSpellcheckDictionary = (projectId: number, language: string) =>
  request<{ data: SpellcheckDictionaryEntry[] }>(
    `/projects/${projectId}/spellcheck-dictionary?language=${encodeURIComponent(language)}`,
  ).then(r => r.data ?? [])

export const addSpellcheckDictionaryWord = (projectId: number, language: string, word: string) =>
  request<{ data: SpellcheckDictionaryEntry }>(`/projects/${projectId}/spellcheck-dictionary`, {
    method: 'POST',
    body: JSON.stringify({ language, word }),
  }).then(r => r.data)

export const deleteSpellcheckDictionaryWord = (projectId: number, wordId: number) =>
  request<void>(`/projects/${projectId}/spellcheck-dictionary/${wordId}`, { method: 'DELETE' })

// --- Bulk draft writes (how corrections are applied) ---

export interface BatchDraftFix {
  translation_id: number
  human_text: string
  human_rich_text?: string | null
  expected_updated_at?: string | null
}

export interface BatchDraftResult {
  /** Full rows, so the caller can refresh its cache and the fresh updated_at. */
  applied: Translation[]
  /** Someone else wrote first; the caller re-reads and retries. */
  conflicts: number[]
  locked: number[]
  /** Another user has the segment open in the editor; not written. */
  held: number[]
  rejected: number[]
  /** See `TaskReopenedFlag` in api/projects.ts. */
  taskReopened: boolean
}

/**
 * Apply many text corrections as drafts. Draft semantics on purpose — an edit
 * downgrades status to needs_review rather than confirming the segment. Locked
 * segments are not written unless `includeLocked` is set and the caller is an
 * owner or admin; they come back in `locked`.
 */
export const batchDraftTranslations = (
  fileId: number,
  fixes: BatchDraftFix[],
  opts: { includeLocked?: boolean } = {},
) =>
  request<{ data: Omit<BatchDraftResult, 'taskReopened'> } & TaskReopenedFlag>(`/files/${fileId}/translations/batch-draft`, {
    method: 'PUT',
    // include_locked is opt-in per request: a lock means "don't touch" and
    // unlock is owner/admin only, so the bypass stays visible at the call site
    // rather than being the endpoint's default.
    body: JSON.stringify({ fixes, ...(opts.includeLocked ? { include_locked: true } : {}) }),
  }).then((r): BatchDraftResult => ({ ...r.data, taskReopened: taskReopenedOf(r) }))
