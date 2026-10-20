/**
 * Pure CAT editor logic: applied-term check and repetition detection.
 */

import { termMatchState } from './term-match'

/**
 * Check if a term is "already applied" — i.e. the target already carries the
 * approved translation at least as many times as the source uses the source
 * term (occurrence-aware, word-boundary count).
 *
 * This is the SAME predicate as the green source-term underline
 * (`termMatchState === 'satisfied'`), so the sidebar's ✓ / disabled state stays
 * in lockstep with the underline: a term with 2 source occurrences but only 1
 * translated is NOT "applied" — it keeps its ✗ (no check), stays clickable so
 * the linguist can insert the missing occurrence, and its second source
 * occurrence shows red.
 */
export function isTermApplied(
  sourceText: string,
  translationText: string,
  sourceTerm: string,
  targetTerm: string
): boolean {
  if (!targetTerm || !translationText) return false
  return termMatchState(sourceText, translationText, sourceTerm, targetTerm) === 'satisfied'
}

/**
 * Compute the set of segment IDs that are *repetitions* — segments whose
 * `source_text` appears earlier in the same file. The FIRST occurrence is
 * treated as "new" (no repetition badge); every SUBSEQUENT occurrence is tagged.
 *
 * Inputs are walked in array order, which is the segments' on-disk order in
 * the file (segments come back from the API sorted by position). Comparison
 * is on raw `source_text` — leading/trailing whitespace and case differences
 * count as distinct sources, mirroring how the auto-propagation SQL
 * (`PropagateTranslation`) keys its match.
 *
 * Pure function, no side effects — drives the per-row "R" badge in the
 * editor's Match column.
 */
export function computeRepetitionSegmentIds(
  segments: { id: number; source_text: string }[] | null | undefined,
): Set<number> {
  const repeats = new Set<number>()
  if (!segments) return repeats
  const seen = new Set<string>()
  for (const s of segments) {
    if (seen.has(s.source_text)) {
      repeats.add(s.id)
    } else {
      seen.add(s.source_text)
    }
  }
  return repeats
}

/**
 * Compute the set of segment IDs that are the FIRST occurrence of a
 * source_text *and* have at least one subsequent duplicate later in the
 * file. This separates the "source of repetitions" from the "repetition"
 * itself so the linguist sees which segment will propagate when they confirm it.
 *
 * Single pass: walk segments, remember the first occurrence's id per
 * source_text, and mark it the moment a duplicate is seen.
 *
 * Pure mirror of `computeRepetitionSegmentIds` — together they partition
 * the repeated-source segments into "source" and "propagated". A segment
 * with a unique source_text is in NEITHER set.
 */
export function computeRepetitionSourceIds(
  segments: { id: number; source_text: string }[] | null | undefined,
): Set<number> {
  const sources = new Set<number>()
  if (!segments) return sources
  const firstSeenId = new Map<string, number>()
  for (const s of segments) {
    const prior = firstSeenId.get(s.source_text)
    if (prior === undefined) {
      firstSeenId.set(s.source_text, s.id)
    } else {
      sources.add(prior)
    }
  }
  return sources
}

export interface RepetitionStats {
  segments: number
  words: number
  characters: number
}

/**
 * Aggregate stats for the "Repetitions" band shown in the CAT editor's
 * leverage status bar. Counts only the SUBSEQUENT occurrences (the same
 * predicate as `computeRepetitionSegmentIds`) — the first occurrence is the
 * canonical row and is not "leverage" yet.
 *
 * A project with 3 sources that each appear 8 times reports "21 repetition
 * segments" (the 21 subsequent occurrences), not "24 in a repetition group".
 *
 * Word count uses a plain whitespace-split (matches `term-insert.ts`).
 * Empty / whitespace-only source counts as 0 words.
 */
export function computeRepetitionStats(
  segments: { id: number; source_text: string }[] | null | undefined,
): RepetitionStats {
  const out: RepetitionStats = { segments: 0, words: 0, characters: 0 }
  if (!segments) return out
  const seen = new Set<string>()
  for (const s of segments) {
    if (seen.has(s.source_text)) {
      out.segments++
      const trimmed = s.source_text.trim()
      out.words += trimmed === '' ? 0 : trimmed.split(/\s+/).length
      out.characters += s.source_text.length
    } else {
      seen.add(s.source_text)
    }
  }
  return out
}
