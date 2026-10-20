// term-match — client-side terminology matching for the CAT editor's live
// source-term underlines.
//
// This is a deliberate, documented port of the Go matcher in
// internal/qa/checker.go (countWordOccurrences + CheckTerminology). The editor
// needs an INSTANT green/red signal as the user types, before the debounced
// server-side QA round-trips. The shared unit-test cases (term-match.test.ts
// mirrors checker_test.go) guard the two implementations against drift.
//
// Semantics, identical to the Go side:
//   - case-insensitive, word-boundary-anchored, non-overlapping occurrence count
//   - a boundary is the start/end of the string OR an adjacent character that is
//     neither a letter nor a digit
//   - a term is "satisfied" when the target contains at least as many
//     occurrences of the target term as the source has of the source term
//
// Limitation (same as Go): scripts without word separators (e.g. CJK Han) treat
// adjacent ideographs as letters, so a term flanked by such characters won't
// match. The reported bugs are Latin-script.

// \p{L} = any Unicode letter, \p{N} = any Unicode number. A "word character" is
// either — matching Go's unicode.IsLetter || unicode.IsDigit.
const WORD_CHAR = /[\p{L}\p{N}]/u

// Apostrophes that GLUE a word together: straight ('), right single quote (’),
// modifier letter apostrophe (ʼ). An apostrophe sitting between two word chars
// is part of the word (possessive "company's", contraction "don't"), so it is
// NOT a boundary — otherwise the tokenizer's bare "s"/"t" fragments would match
// single-letter or partial terms. Keep in sync with Go isWordBoundary.
const APOSTROPHE = /['’ʼ]/

export function isWordChar(ch: string | undefined): boolean {
  return ch !== undefined && WORD_CHAR.test(ch)
}

/**
 * isWholeWordMatch reports whether the [start, end) slice of `s` is a whole-word
 * occurrence: each adjacent character is the string edge, a true separator, or a
 * quote-style apostrophe — but NOT a word char and NOT a word-gluing apostrophe
 * (one flanked by a word char on its far side, i.e. a possessive/contraction
 * fragment). Mirrors Go isWordBoundary.
 */
export function isWholeWordMatch(s: string, start: number, end: number): boolean {
  const before = start > 0 ? s[start - 1] : undefined
  if (isWordChar(before)) return false
  if (before !== undefined && APOSTROPHE.test(before) && isWordChar(start > 1 ? s[start - 2] : undefined)) return false
  const after = end < s.length ? s[end] : undefined
  if (isWordChar(after)) return false
  if (after !== undefined && APOSTROPHE.test(after) && isWordChar(end + 1 < s.length ? s[end + 1] : undefined)) return false
  return true
}

/**
 * countWordOccurrences counts case-insensitive, word-boundary-anchored,
 * non-overlapping occurrences of `needle` within `haystack`.
 */
export function countWordOccurrences(haystack: string, needle: string): number {
  if (!needle) return 0
  const h = haystack.toLowerCase()
  const n = needle.toLowerCase()
  let count = 0
  let from = 0
  for (;;) {
    const idx = h.indexOf(n, from)
    if (idx < 0) break
    const end = idx + n.length
    if (isWholeWordMatch(h, idx, end)) {
      count++
      from = end // non-overlapping
    } else {
      from = idx + 1 // resync past this (sub-word) hit
    }
  }
  return count
}

/**
 * Correctness of an approved term in a given source/target pair, mirroring the
 * Go CheckTerminology rule:
 *   - "absent": the source term does not occur as a whole word in the source —
 *     nothing to enforce (the DB pre-filter uses a loose substring ILIKE).
 *   - "satisfied": target occurrences >= source occurrences.
 *   - "missing": target has fewer occurrences than the source.
 */
export type TermMatchState = 'satisfied' | 'missing' | 'absent'

export function termMatchState(source: string, target: string, sourceTerm: string, targetTerm: string): TermMatchState {
  const srcN = countWordOccurrences(source, sourceTerm)
  if (srcN === 0) return 'absent'
  const tgtN = countWordOccurrences(target, targetTerm)
  return tgtN >= srcN ? 'satisfied' : 'missing'
}

/**
 * conceptMatchState is the concept-level correctness signal, mirroring the Go
 * CheckTerminology OR-within-concept rule (internal/qa/checker.go): a concept's
 * approved target synonyms are ALTERNATIVES, so it's satisfied when the target
 * contains enough of ANY of them combined. This is the client twin of the
 * server QA fix — one approved synonym used must NOT paint the others red.
 *   - srcN = MAX occurrences over the source synonyms; 'absent' when the source
 *     doesn't occur (nothing to enforce).
 *   - tgtN = SUM of occurrences over the approved target synonyms.
 *   - 'satisfied' when tgtN >= srcN, else 'missing'.
 */
export function conceptMatchState(source: string, target: string, sourceTerms: string[], approvedTargets: string[]): TermMatchState {
  let srcN = 0
  for (const st of sourceTerms) srcN = Math.max(srcN, countWordOccurrences(source, st))
  if (srcN === 0) return 'absent'
  let tgtN = 0
  for (const at of approvedTargets) tgtN += countWordOccurrences(target, at)
  return tgtN >= srcN ? 'satisfied' : 'missing'
}
