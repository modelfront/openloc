// Decoration layer for read-only segment text: `decorationRanges` picks what to
// paint, `renderRanges` paints it. See lib/text-ranges.ts for why decoration is
// range-based.

import React from 'react'
import type { TermLookupResult } from '../api'
import { termRanges } from './term-ranges'
import { mergeRanges, withGhosts, type DecoratedRange } from './text-ranges'
import { tmDiffRanges } from './tm-diff'
import { trackChangeRanges } from './track-changes'
import { searchRanges } from './text-search'

/**
 * Where a window of the text sits among the windows it is drawn in. Text
 * between tag chips is drawn node by node, and each ghost (a zero-width range
 * carrying removed words) must land in exactly one of them: the last window
 * that starts at or before the ghost. That window owns everything up to the
 * next one's start, and the first window also owns what comes before it.
 * A ghost on a line break therefore ends the line above, and one on a chip
 * boundary starts the text after the chip. One-window renderers keep the
 * default, which owns every ghost.
 */
export interface RangeWindow {
  /** Offset where the next window starts, or Infinity for the last one. */
  nextOffset: number
  first: boolean
}

const WHOLE_TEXT: RangeWindow = { nextOffset: Infinity, first: true }

/**
 * Slice `ranges` to the window `[offset, offset + slice.length)` and render the
 * decorated pieces. `leaf` renders an undecorated chunk — the plain renderer
 * passes `s => s`, the whitespace renderer passes its marker renderer — which
 * is what keeps one decoration usable from every rendering mode. Each leaf call
 * gets its own keyed <span> so leaves may use internal keys freely.
 *
 * Ranges must be sorted by start and non-overlapping, with ghosts placed
 * between painted ranges — `termRanges`, `mergeRanges` and `withGhosts`
 * guarantee that. A range with className '' is a match not worth marking
 * (unused forbidden term) and renders as a plain leaf.
 */
export function renderRanges(
  slice: string,
  ranges: DecoratedRange[],
  offset: number,
  keyPrefix: string,
  leaf: (s: string) => React.ReactNode,
  textWindow: RangeWindow = WHOLE_TEXT,
): React.ReactNode[] {
  const out: React.ReactNode[] = []
  const sliceEnd = offset + slice.length
  let pos = 0 // relative to slice
  for (let i = 0; i < ranges.length; i++) {
    const r = ranges[i]
    if (r.ghost !== undefined) {
      const owned = r.start < textWindow.nextOffset && (r.start >= offset || textWindow.first)
      if (!owned) continue
      const at = Math.min(Math.max(r.start - offset, pos), slice.length)
      if (at > pos) out.push(<span key={`${keyPrefix}p${i}`}>{leaf(slice.slice(pos, at))}</span>)
      out.push(
        r.className
          ? <span key={`${keyPrefix}g${i}`} className={r.className} title={r.title}>{leaf(r.ghost)}</span>
          : <span key={`${keyPrefix}g${i}`}>{leaf(r.ghost)}</span>,
      )
      pos = at
      continue
    }
    if (r.end <= offset || r.start >= sliceEnd) continue
    const s = Math.max(r.start - offset, 0)
    const e = Math.min(r.end - offset, slice.length)
    if (s > pos) out.push(<span key={`${keyPrefix}p${i}`}>{leaf(slice.slice(pos, s))}</span>)
    out.push(
      r.className
        ? <span key={`${keyPrefix}m${i}`} className={r.className} title={r.title}>{leaf(slice.slice(s, e))}</span>
        : <span key={`${keyPrefix}m${i}`}>{leaf(slice.slice(s, e))}</span>,
    )
    pos = e
  }
  if (pos < slice.length) out.push(<span key={`${keyPrefix}tail`}>{leaf(slice.slice(pos))}</span>)
  return out
}

/** What to decorate a piece of segment text with. Every field is optional. */
export interface Decorations {
  /** Approved-term underlines (lib/term-ranges.ts). */
  terms?: TermLookupResult[]
  /** Current target text — enables green/red term verdicts. */
  target?: string
  /** One-sided TM fuzzy diff: paint what the text adds over this string. */
  diffAgainst?: string | null
  /** Two-sided track changes: paint what was added since this earlier
   *  version, and draw what was removed (lib/track-changes.ts). */
  changesFrom?: string | null
  /** Search hits to tint (lib/text-search.ts `searchHighlightRegex`). */
  search?: RegExp | null
}

/** Whether `decorationRanges` has anything to compute for these options. */
export function hasDecorations({ terms, diffAgainst, changesFrom, search }: Decorations): boolean {
  return (terms != null && terms.length > 0) || !!diffAgainst || !!changesFrom || !!search
}

/**
 * The decorations to paint over `flat`, in one place so every renderer composes
 * them identically. Term underlines and the diffs paint different CSS axes and
 * can cover the same characters, so overlaps merge rather than one winning.
 */
export function decorationRanges(flat: string, { terms, target, diffAgainst, changesFrom, search }: Decorations): DecoratedRange[] {
  const term = terms && terms.length > 0 ? termRanges(flat, terms, target) : []
  const diff = diffAgainst ? tmDiffRanges(flat, diffAgainst) : []
  const changes = changesFrom ? trackChangeRanges(flat, changesFrom) : []
  const added = changes.filter(r => r.ghost === undefined)
  const hits = search ? searchRanges(flat, search) : []
  const painted = diff.length === 0 && added.length === 0 && hits.length === 0 ? term : mergeRanges(term, diff, added, hits)
  return withGhosts(painted, changes.filter(r => r.ghost !== undefined))
}
