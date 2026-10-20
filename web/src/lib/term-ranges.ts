// term-ranges — offset-based term-underline computation for the CAT editor's
// source column.
//
// Emits plain {start, end, term, className} ranges over the segment's
// flattened text, not JSX, so every renderer (tag chips, whitespace markers)
// paints the same underlines — chips are node boundaries, so per-text-node
// painting is safe.
//
// MATCHING SEMANTICS — must mirror the server, not improvise:
// the server FINDS terms by tokenizing the source text (tokenizeSourceText in
// internal/terminology/handler.go): every punctuation/symbol rune except '-'
// and '_' becomes a space, any whitespace run splits words, and n-grams are
// joined with ONE space before the exact-equality DB lookup. So a multi-word
// term the sidebar lists can sit in the raw text with a newline, NBSP, double
// space, or comma between its words, which a literal-substring regex misses.
// normalizeTerm() + the separator run below are the client-side inverse of that
// tokenizer. Keep them in sync.
//
// Deliberately NOT loosened: countWordOccurrences (term-match.ts) stays a
// literal matcher because it is a documented mirror of Go's
// internal/qa/checker.go — loosening one side alone would fork QA verdicts.
// Consequence: a multi-word TARGET term split by a newline still counts as
// missing (red), which is consistent with what the QA panel reports.

import type { TermLookupResult } from '../api/terminology'
import { termMatchState, countWordOccurrences, isWholeWordMatch } from './term-match'
import { termStatusLabel } from './term-status'
import type { DecoratedRange } from './text-ranges'
import { escapeRegExp } from './regex'

const UNDERLINE_BASE = 'underline decoration-2 underline-offset-2 '
export const UNDERLINE_NEUTRAL = UNDERLINE_BASE + 'decoration-violet-400 dark:decoration-violet-500'
export const UNDERLINE_GREEN = UNDERLINE_BASE + 'decoration-green-500 dark:decoration-green-400'
export const UNDERLINE_RED = UNDERLINE_BASE + 'decoration-red-500 dark:decoration-red-400'

/** One decorated slice of the source text. `className === ''` means "matched,
 *  but nothing worth marking" (e.g. a forbidden term that isn't violated) —
 *  renderers must render such ranges as plain text. */
export interface TermRange extends DecoratedRange {
  term: TermLookupResult
}

// The characters the server tokenizer treats as word separators: any
// whitespace, punctuation, or symbol EXCEPT '-' and '_' (which it preserves
// inside words). A run of one or more of these may stand between the words of
// a multi-word term.
const SEPARATOR_RUN = '(?:(?![-_])[\\s\\p{P}\\p{S}])+'

/**
 * Normalize a string the way the server tokenizer normalizes source text:
 * lowercase, punctuation/symbols (except -_) to spaces, whitespace runs
 * collapsed to a single space. Two strings that normalize equal are "the same
 * term" as far as the server's lookup is concerned.
 */
export function normalizeTerm(s: string): string {
  return s
    .toLowerCase()
    .replace(/(?![-_])[\p{P}\p{S}]/gu, ' ')
    .split(/\s+/)
    .filter(Boolean)
    .join(' ')
}


/**
 * Compute the term-underline ranges for `text`.
 *
 * Coloring (per-OCCURRENCE): for an approved term the first
 * `covered` source occurrences — where covered = countWordOccurrences(target,
 * targetTerm) — are matched by a correct translation and render GREEN; any
 * occurrences beyond that are uncovered and render RED. So "password …
 * password" with a single correct "Passwort" in the target shows the first
 * occurrence green and only the extra one red, instead of painting the whole
 * term red. Sub-word hits (not on word boundaries) are dropped entirely — not
 * underlined, and they never consume a coverage slot. Without a target (read-only
 * panes) or for pending/forbidden terms, termUnderlineClass decides at the term level.
 *
 * `terms` is expected in server order (priority ASC) — when two term bases
 * define the same source term, the first entry wins.
 *
 * CONCEPT GROUPING: a concept's approved target synonyms are
 * ALTERNATIVES — a source occurrence is "covered" when the target contains
 * enough of ANY of them combined, mirroring the server's OR-within-concept QA
 * (internal/qa/checker.go). Coverage is therefore counted per CONCEPT, not per
 * (source,target) row: using one approved synonym must not paint the others red.
 */
export function termRanges(text: string, terms: TermLookupResult[], target?: string): TermRange[] {
  if (!text || !terms.length) return []

  // First entry per normalized source term wins (server sends priority order).
  // This representative row backs the range's tooltip and the regex alternation.
  const byNorm = new Map<string, TermLookupResult>()
  for (const t of terms) {
    const key = normalizeTerm(t.source_term)
    if (key && !byNorm.has(key)) byNorm.set(key, t)
  }
  if (!byNorm.size) return []

  // Group target synonyms by concept (deduped, split by status).
  type Concept = { approved: string[]; forbidden: string[] }
  const concepts = new Map<number, Concept>()
  for (const t of terms) {
    let c = concepts.get(t.concept_id)
    if (!c) { c = { approved: [], forbidden: [] }; concepts.set(t.concept_id, c) }
    if (t.status === 'approved') { if (!c.approved.includes(t.target_term)) c.approved.push(t.target_term) }
    else if (t.status === 'forbidden') { if (!c.forbidden.includes(t.target_term)) c.forbidden.push(t.target_term) }
  }
  // Map each normalized source term to its concept, preferring a concept that
  // has approved targets when the same source appears in more than one.
  const conceptBySource = new Map<string, Concept>()
  for (const t of terms) {
    const key = normalizeTerm(t.source_term)
    const c = concepts.get(t.concept_id)!
    const existing = conceptBySource.get(key)
    if (!existing || (existing.approved.length === 0 && c.approved.length > 0)) conceptBySource.set(key, c)
  }

  // Longest term first so the alternation prefers "email address" over "email"
  // at the same position.
  const pattern = [...byNorm.keys()]
    .sort((a, b) => b.length - a.length)
    .map(k => k.split(' ').map(escapeRegExp).join(SEPARATOR_RUN))
    .join('|')
  const regex = new RegExp(pattern, 'giu')

  const coveredByConcept = new Map<Concept, number>()
  const seenByConcept = new Map<Concept, number>()
  const ranges: TermRange[] = []

  for (const m of text.matchAll(regex)) {
    const part = m[0]
    const start = m.index
    const end = start + part.length
    const key = normalizeTerm(part)
    const match = byNorm.get(key)
    const concept = conceptBySource.get(key)
    if (!match || !concept) continue

    // Whole-word gate (shared with countWordOccurrences / the Go checker's
    // isWordBoundary): the DB pre-filter is a loose substring ILIKE, so the
    // server can return terms that only appear as SUB-words — e.g. single-letter
    // terms ("a" → Ampere, "s" → Sekunde) landing inside "Standard"/"system", a
    // plural swallowing its singular ("password" in "passwords"), or an
    // apostrophe fragment ("s" from the possessive "company's"). Anything that
    // isn't a standalone word is dropped — no underline, no coverage slot.
    if (!isWholeWordMatch(text, start, end)) continue

    let className: string
    if (target === undefined) {
      className = UNDERLINE_NEUTRAL // read-only pane
    } else if (match.source_status === 'forbidden') {
      // The span matched via a term that's forbidden IN THE SOURCE LANGUAGE — a
      // discouraged input phrasing. "Forbidden" constrains OUTPUT, so it's inert
      // on the source (which the translator can't change). Show it as an
      // informational match, never a red violation, and never credit/demand
      // target coverage for it.
      className = UNDERLINE_NEUTRAL
    } else if (concept.approved.length > 0) {
      // covered = Σ approved-synonym occurrences in the target; the first
      // `covered` whole-word source occurrences OF THE CONCEPT render GREEN.
      if (!coveredByConcept.has(concept)) {
        coveredByConcept.set(concept, concept.approved.reduce((s, at) => s + countWordOccurrences(target, at), 0))
      }
      const k = seenByConcept.get(concept) ?? 0
      seenByConcept.set(concept, k + 1)
      className = k < (coveredByConcept.get(concept) ?? 0) ? UNDERLINE_GREEN : UNDERLINE_RED
    } else if (concept.forbidden.length > 0) {
      // Forbidden-only concept: RED when a banned target is actually present (a
      // violation), otherwise nothing worth marking.
      className = concept.forbidden.some(ft => countWordOccurrences(target, ft) > 0) ? UNDERLINE_RED : ''
    } else {
      className = UNDERLINE_NEUTRAL // pending-only
    }

    // No tooltip on an unmarked range ('' className): it renders as plain text,
    // so there is nothing for the user to hover.
    ranges.push({
      start, end, term: match, className,
      ...(className ? { title: `${match.target_term} (${termStatusLabel(match.status)})` } : {}),
    })
  }
  return ranges
}

// termUnderlineClass picks the underline color for a matched source term at the
// TERM level (all occurrences share one color). When `target` is provided (the
// live editor path), approved terms turn GREEN once the target contains the
// expected translation the right number of times and RED while it's
// missing/undercounted. FORBIDDEN terms turn RED when the
// banned target term is actually present in the translation (a violation),
// neutral otherwise. Without a target (read-only panes) or for pending terms we
// keep the neutral violet.
//
// termRanges() above uses the per-OCCURRENCE variant for approved terms with a
// target (so 2× source with 1× correct target shows one green + one red rather
// than both red); this term-level helper covers every other case and is the
// unit under termUnderlineClass's tests.
export function termUnderlineClass(term: TermLookupResult, source: string, target: string | undefined): string {
  if (target === undefined) return UNDERLINE_NEUTRAL
  // Matched via a source-language-forbidden term (discouraged input phrasing):
  // inert on the unchangeable source → informational, never red (see termRanges).
  if (term.source_status === 'forbidden') return UNDERLINE_NEUTRAL
  if (term.status === 'forbidden') {
    // A forbidden term is only worth marking when its banned target term is
    // actually present (a violation → RED). When it's absent there's nothing
    // wrong and nothing to insert, so we DON'T underline at all ('').
    return countWordOccurrences(target, term.target_term) > 0 ? UNDERLINE_RED : ''
  }
  if (term.status !== 'approved') return UNDERLINE_NEUTRAL // pending
  const state = termMatchState(source, target, term.source_term, term.target_term)
  if (state === 'missing') return UNDERLINE_RED
  if (state === 'satisfied') return UNDERLINE_GREEN
  return UNDERLINE_NEUTRAL // 'absent'
}
