import type { SpellcheckJobResult, SpellMatch } from '../api/spellcheck'

/**
 * Orchestration for a document-wide spell check run.
 *
 * Scope resolution lives here rather than on the server: the CAT editor already
 * holds the translation list and the active filter, so the client resolves a
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

export interface RunSpellcheckOptions {
  jobId: number
  translationIds: number[]
  /** Plain text of a translation, for extracting flagged words. */
  textFor: (translationId: number) => string
  ignoredWords: Set<string>
  check: (jobId: number, ids: number[], signal?: AbortSignal) => Promise<SpellcheckJobResult>
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
    jobId, translationIds, textFor, ignoredWords, check,
    segmentIdFor, segmentIndexFor, signal, onProgress,
  } = opts
  const size = opts.chunkSize ?? SPELLCHECK_CHUNK_SIZE

  const errors: SpellError[] = []
  let failed = 0
  let done = 0

  for (const ids of chunk(translationIds, size)) {
    if (signal?.aborted) return { errors, failed, aborted: true }

    const result = await check(jobId, ids, signal)
    failed += result.failed ?? 0

    for (const segment of result.results ?? []) {
      const text = textFor(segment.translation_id)
      for (const match of segment.matches ?? []) {
        if (isIgnored(text, match, ignoredWords)) continue
        errors.push(toSpellError(segment.translation_id, text, match, segmentIdFor, segmentIndexFor))
      }
    }

    done += ids.length
    onProgress?.(done, translationIds.length)

    if (signal?.aborted) return { errors, failed, aborted: true }
  }

  return { errors, failed, aborted: false }
}

function toSpellError(
  translationId: number,
  text: string,
  match: SpellMatch,
  segmentIdFor?: (id: number) => number,
  segmentIndexFor?: (id: number) => number,
): SpellError {
  return {
    translationId,
    segmentId: segmentIdFor?.(translationId) ?? 0,
    segmentIndex: segmentIndexFor?.(translationId) ?? -1,
    offset: match.offset,
    length: match.length,
    word: text.slice(match.offset, match.offset + match.length),
    message: match.message,
    suggestions: (match.replacements ?? []).slice(0, 5).map(r => r.value),
    ruleCategory: match.rule?.category?.id ?? '',
  }
}
