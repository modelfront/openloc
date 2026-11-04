import type { SpellcheckJobResult, SpellcheckSegmentResult, SpellMatch } from '../api/spellcheck'

/**
 * Orchestration for a document-wide spell check run.
 *
 * Scope resolution lives here rather than on the server: the CAT editor already
 * holds the index of every segment and the active filter, so the client resolves a
 * scope to concrete translation ids and the endpoint stays a dumb "check these"
 * call. That also keeps the filter predicates in one place
 * (`lib/segment-filters.ts`) instead of duplicated in Go.
 */

// Scope vocabulary and resolution live in lib/spellcheck-scope.ts — one module
// for both surfaces. Re-exported here so existing callers keep working.
export { resolveRunScope, type SpellRunScope } from './spellcheck-scope'

/**
 * Ids per request. The server rejects more than 200 in one call; staying well
 * under that keeps each chunk's round-trip short enough that progress moves
 * visibly and cancellation feels immediate. 20 ids ≈ two full joined
 * LanguageTool requests server-side (minGroupSegments in
 * internal/spellcheck/batch.go), so a chunk costs a few seconds even on a
 * CPU-bound LanguageTool pod.
 */
export const SPELLCHECK_CHUNK_SIZE = 20

/** A single reviewable finding, flattened across segments. */
export interface SpellError {
  translationId: number
  segmentId: number
  /** Index of the segment in the unfiltered list — used to navigate the grid. */
  segmentIndex: number
  offset: number
  length: number
  /** The flagged text itself. Groups "Change all" / "Ignore all" decisions. */
  word: string
  message: string
  suggestions: string[]
  ruleCategory: string
  /** The target text that was checked; offset indexes into it. */
  targetText: string
  sourceText: string
}

export interface SpellcheckRunResult {
  errors: SpellError[]
  failed: number
  aborted: boolean
}

export function chunk<T>(items: T[], size: number): T[][] {
  const out: T[][] = []
  for (let i = 0; i < items.length; i += size) {
    out.push(items.slice(i, i + size))
  }
  return out
}

/**
 * Whether a match's flagged word is in the ignore set.
 *
 * Comparison is case-sensitive to match the server and the dictionary table:
 * in German `adresse` vs `Adresse` is the error itself, so folding case would
 * hide real findings.
 */
export function isIgnored(
  text: string,
  match: { offset: number; length: number },
  ignoredWords: Set<string>,
): boolean {
  if (ignoredWords.size === 0) return false
  return ignoredWords.has(text.slice(match.offset, match.offset + match.length))
}

/** Checks one chunk of translation ids. */
export type SpellcheckChunk = (ids: number[], signal?: AbortSignal) => Promise<SpellcheckJobResult>

/**
 * A check that sends each translation to its own job's endpoint, job by job,
 * and puts the results together: the multi-file editor's check. Ids without a
 * job are left out.
 */
export function checkByJob(
  jobOf: (translationId: number) => number | undefined,
  checkJob: (jobId: number, ids: number[], signal?: AbortSignal) => Promise<SpellcheckJobResult>,
): SpellcheckChunk {
  return async (ids, signal) => {
    const byJob = new Map<number, number[]>()
    for (const id of ids) {
      const jobId = jobOf(id)
      if (jobId === undefined) continue
      const list = byJob.get(jobId)
      if (list) list.push(id)
      else byJob.set(jobId, [id])
    }
    const out: SpellcheckJobResult = { results: [], failed: 0 }
    for (const [jobId, part] of byJob) {
      const r = await checkJob(jobId, part, signal)
      out.results.push(...(r.results ?? []))
      out.failed += r.failed ?? 0
    }
    return out
  }
}

export interface RunSpellcheckOptions {
  translationIds: number[]
  ignoredWords: Set<string>
  check: SpellcheckChunk
  segmentIdFor?: (translationId: number) => number
  segmentIndexFor?: (translationId: number) => number
  chunkSize?: number
  signal?: AbortSignal
  onProgress?: (done: number, total: number) => void
}

/**
 * Run the check chunk by chunk, aggregating findings.
 *
 * Chunks run sequentially rather than in parallel: the point of chunking is a
 * progress bar and a responsive Cancel, and firing them all at once would give
 * neither while hammering LanguageTool with our own concurrency on top of the
 * server's internal fan-out.
 */
export async function runSpellcheck(opts: RunSpellcheckOptions): Promise<SpellcheckRunResult> {
  const {
    translationIds, ignoredWords, check,
    segmentIdFor, segmentIndexFor, signal, onProgress,
  } = opts
  const size = opts.chunkSize ?? SPELLCHECK_CHUNK_SIZE

  const errors: SpellError[] = []
  let failed = 0
  let done = 0

  for (const ids of chunk(translationIds, size)) {
    if (signal?.aborted) return { errors, failed, aborted: true }

    const result = await check(ids, signal)
    failed += result.failed ?? 0

    for (const segment of result.results ?? []) {
      for (const match of segment.matches ?? []) {
        if (isIgnored(segment.text, match, ignoredWords)) continue
        errors.push(toSpellError(segment, match, segmentIdFor, segmentIndexFor))
      }
    }

    done += ids.length
    onProgress?.(done, translationIds.length)

    if (signal?.aborted) return { errors, failed, aborted: true }
  }

  return { errors, failed, aborted: false }
}

function toSpellError(
  segment: SpellcheckSegmentResult,
  match: SpellMatch,
  segmentIdFor?: (id: number) => number,
  segmentIndexFor?: (id: number) => number,
): SpellError {
  const translationId = segment.translation_id
  return {
    translationId,
    segmentId: segmentIdFor?.(translationId) ?? 0,
    segmentIndex: segmentIndexFor?.(translationId) ?? -1,
    offset: match.offset,
    length: match.length,
    word: segment.text.slice(match.offset, match.offset + match.length),
    message: match.message,
    suggestions: (match.replacements ?? []).slice(0, 5).map(r => r.value),
    ruleCategory: match.rule?.category?.id ?? '',
    targetText: segment.text,
    sourceText: segment.source_text,
  }
}
