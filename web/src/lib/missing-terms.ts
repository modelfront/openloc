import type { TermLookupResult } from '../api/terminology'
import { isTermApplied } from './cat-editor-logic'
import { countWordOccurrences } from './term-match'
import { normalizeTerm, termRanges } from './term-ranges'

/** A term whose target the translation lacks, at a source term's offset. */
export interface MissingTerm {
  /** The row whose `target_term` F8 inserts. */
  term: TermLookupResult
  /** Offset of the source term in the flattened source. */
  at: number
}

/**
 * The term targets F8 inserts: one per source occurrence the target does not
 * cover, found where the source underlines find them (termRanges). The rows
 * for one source term are its choices: forbidden ones never go in, the first
 * approved one (server order) does, else the first pending one. None is
 * offered while the target has any of them by the Terminology panel's rule
 * (isTermApplied), or while its targets together cover every occurrence.
 * Uncovered occurrences are the later ones, as for placeables.
 */
export function getMissingTerms(terms: readonly TermLookupResult[], flatSource: string, target: string): MissingTerm[] {
  if (terms.length === 0) return []
  const offsets = new Map<string, number[]>()
  for (const r of termRanges(flatSource, [...terms])) {
    const key = normalizeTerm(r.term.source_term)
    offsets.set(key, [...(offsets.get(key) ?? []), r.start])
  }

  const out: MissingTerm[] = []
  for (const [key, at] of offsets) {
    const rows = terms.filter(t => t.target_term && (t.status === 'approved' || t.status === 'pending') && normalizeTerm(t.source_term) === key)
    if (rows.length === 0 || rows.some(t => isTermApplied(flatSource, target, t.source_term, t.target_term))) continue
    const pick = rows.find(t => t.status === 'approved') ?? rows[0]
    const covered = [...new Set(rows.map(t => t.target_term))].reduce((n, t) => n + countWordOccurrences(target, t), 0)
    // The underlines (termRanges) also find a multi-word term split by a no-break
    // space or a line break, which the word count does not.
    if (covered >= Math.max(at.length, countWordOccurrences(flatSource, pick.source_term))) continue
    // The cap keeps one place when the source counts more occurrences than
    // termRanges places.
    for (const start of at.slice(Math.min(covered, at.length - 1))) out.push({ term: pick, at: start })
  }
  return out.sort((a, b) => a.at - b.at)
}
