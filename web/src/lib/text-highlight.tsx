import type { ReactNode } from 'react'
import { escapeRegExp } from './regex'

/** The tint on its own, so a selector can key on the colour without depending
 *  on the padding and radius around it. */
export const HIGHLIGHT_TINT = 'bg-yellow-300'

/**
 * The strong yellow "look here" run: a Find / Concordance search hit, and the
 * part of a fuzzy TM hit that differs from the segment (lib/tm-diff.ts). One
 * constant so the shade is decided once, in both light and dark mode. Lists
 * where many rows hit at once use the quiet SEARCH_HIT_CLASS below instead.
 *
 * The padding is cancelled by an equal negative margin: the tint reaches past
 * the glyphs it covers — a run one character wide is unreadable flush to its
 * edges — while the text around it stays where it was. Plain `px-*` pushes the
 * rest of the sentence along, and a run can start mid-word.
 */
export const HIGHLIGHT_CLASS = `${HIGHLIGHT_TINT} dark:bg-yellow-700 px-0.5 -mx-0.5 rounded`

/**
 * The quiet version for a list where many rows hit at once (TM browser
 * search): the same yellow at low strength, so the hits are easy to find
 * without the whole table shouting. No padding — runs sit inside table text.
 */
export const SEARCH_HIT_TINT = 'bg-yellow-200/60'
export const SEARCH_HIT_CLASS = `${SEARCH_HIT_TINT} dark:bg-yellow-400/25 rounded-sm`

/**
 * Wraps matching substrings in <mark> tags with yellow background.
 * Case-insensitive. Returns the original string if term is empty.
 *
 * Used by: ConcordanceModal (search result highlighting),
 *          SegmentRow (Find & Replace inline highlighting).
 *
 * SAFETY: this is a RENDER-TIME decoration only. The <mark> tags exist
 * in React's virtual DOM, never in stored data. When the term clears,
 * the next render produces clean text with zero leftover marks.
 */
export function highlightText(text: string, term: string): ReactNode {
  if (!term.trim()) return text
  const escaped = escapeRegExp(term)
  const regex = new RegExp(`(${escaped})`, 'gi')
  const parts = text.split(regex)
  return parts.map((part, i) =>
    regex.test(part)
      ? <mark key={i} className={HIGHLIGHT_CLASS}>{part}</mark>
      : part
  )
}
