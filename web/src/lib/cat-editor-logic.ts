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
