/**
 * Track changes between two versions of a segment's text, for the History
 * panel: what a step or a machine pass changed in the text before it.
 *
 * Two-sided, unlike the TM diff: added words are painted over `text`, and
 * removed words are ghost ranges — zero-width, carrying the removed text,
 * which the renderer draws inline. The diff is word-level (lib/text-diff.ts),
 * so a changed word is shown whole.
 */

import type { DecoratedRange } from './text-ranges'
import { HIGHLIGHT_CLASS } from './text-highlight'
import { boundedCache, DIFF_DELETE, DIFF_INSERT, wordDiff, type DiffOp } from './text-diff'

/** Added text uses the app's one yellow "look here" run. */
export const TRACK_INSERT_CLASS = HIGHLIGHT_CLASS
/** Removed text is struck through on a red tint — the panel's colour for
 *  text that is thrown away, as on the Cleared chip. */
export const TRACK_DELETE_CLASS = 'line-through bg-red-100 text-red-800 dark:bg-red-900/50 dark:text-red-300 px-0.5 rounded'
/** On a removed run directly followed by an added one. The added run's
 *  highlight reaches 2px past its glyphs (`-mx-0.5` in HIGHLIGHT_CLASS), so a
 *  4px margin leaves a 2px gap between the two tints. */
export const TRACK_GAP_CLASS = 'mr-1'

const memo = boundedCache<DecoratedRange[]>()

/** Split a run into its leading space, its words and its trailing space. A
 *  run that is only space keeps it all as words: the space is the change. */
function edges(value: string): [lead: string, core: string, trail: string] {
  const m = /^(\s*)([\s\S]*?)(\s*)$/.exec(value)!
  return m[2] ? [m[1], m[2], m[3]] : ['', value, '']
}

/**
 * Ranges over `text` marking what changed since `previous`. A removal sits at
 * the offset where the removed words were and comes before the words that
 * replaced them, so a replacement reads old → new. Spaces at the edges of a
 * change are drawn but not marked, so the marks hug the words.
 *
 * An empty `previous` gives no ranges: the first text a segment gets is not a
 * change to anything, and painting all of it would say nothing.
 */
export function trackChangeRanges(text: string, previous: string | null | undefined): DecoratedRange[] {
  if (!text || !previous || text === previous) return []

  return memo(`${previous}\0${text}`, () => {
    const diffs: DiffOp[] = wordDiff(previous, text)
    const ranges: DecoratedRange[] = []
    let pos = 0 // cursor into `text`
    diffs.forEach(([op, value], i) => {
      const [lead, core, trail] = edges(value)
      if (op === DIFF_DELETE) {
        const next = diffs[i + 1]
        const touchesAdded = !trail && next?.[0] === DIFF_INSERT && !edges(next[1])[0]
        const className = touchesAdded ? `${TRACK_DELETE_CLASS} ${TRACK_GAP_CLASS}` : TRACK_DELETE_CLASS
        if (lead) ranges.push({ start: pos, end: pos, ghost: lead, className: '' })
        ranges.push({ start: pos, end: pos, ghost: core, className, title: 'Removed' })
        if (trail) ranges.push({ start: pos, end: pos, ghost: trail, className: '' })
        return
      }
      if (op === DIFF_INSERT) {
        const start = pos + lead.length
        ranges.push({ start, end: start + core.length, className: TRACK_INSERT_CLASS, title: 'Added' })
      }
      pos += value.length
    })
    return ranges
  })
}
