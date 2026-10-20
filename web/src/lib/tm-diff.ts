/**
 * Difference highlighting between a fuzzy TM hit's source and the segment's
 * own source.
 *
 * Algorithm: character-level diff (lib/text-diff.ts), keeping only the INSERT
 * ops. It is one-sided by design: a call answers "what does `text` add that
 * `reference` doesn't have?". Both directions are on screen because both
 * strings are rendered, each diffed against the other, so a word only the
 * segment has shows up highlighted on the segment and a word only the TM has
 * shows up highlighted on the TM card.
 */

import type { DecoratedRange } from './text-ranges'
import { HIGHLIGHT_CLASS, HIGHLIGHT_TINT } from './text-highlight'
import { boundedCache, charDiff, DIFF_DELETE, DIFF_INSERT } from './text-diff'

/** The diff shares the app's one yellow "look here" run with search hits —
 *  same signal, so one constant owns the shade (lib/text-highlight.tsx). */
export const TM_DIFF_TINT = HIGHLIGHT_TINT
export const TM_DIFF_CLASS = HIGHLIGHT_CLASS

const memo = boundedCache<DecoratedRange[]>()

/**
 * Ranges over `text` covering what `text` adds relative to `reference`.
 * Offsets are UTF-16 code units, which is what `String.slice` and the regex
 * offsets in term-ranges.ts use — the two range sets are directly comparable.
 */
export function tmDiffRanges(text: string, reference: string): DecoratedRange[] {
  if (!text || !reference || text === reference) return []

  return memo(`${reference}\0${text}`, () => {
    const ranges: DecoratedRange[] = []
    let pos = 0 // cursor into `text`
    for (const [op, value] of charDiff(reference, text)) {
      // A deletion exists only in `reference`, so it occupies no offsets in
      // `text` and the cursor stays put.
      if (op === DIFF_DELETE) continue
      if (op === DIFF_INSERT) ranges.push({ start: pos, end: pos + value.length, className: TM_DIFF_CLASS })
      pos += value.length
    }
    return ranges
  })
}
