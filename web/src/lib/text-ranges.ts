/**
 * Offset-range decorations over a piece of segment text.
 *
 * A decoration is a half-open `[start, end)` window plus the class that paints
 * it. Keeping decorations as ranges rather than nested spans is what lets them
 * compose with every rendering mode the editor has — inline-tag chips,
 * whitespace markers, plain text — since each renderer paints the ranges that
 * intersect its own window (see `renderRanges` in lib/segment-decorations.tsx).
 *
 * Producers: `termRanges` (lib/term-ranges.ts) and `tmDiffRanges`
 * (lib/tm-diff.ts).
 */

export interface DecoratedRange {
  start: number
  end: number
  /** Tailwind classes for the span. '' means "covered, but nothing to paint"
   *  — the renderer emits a plain leaf for it. */
  className: string
  /** Tooltip for the span, when the producer has one. */
  title?: string
  /** Text drawn at `start` that is not part of the text itself: a ghost range
   *  is zero-width (`start === end`) and carries words the text no longer
   *  has (lib/track-changes.ts). */
  ghost?: string
}

/**
 * Flatten several decoration lists into one sorted, non-overlapping list.
 *
 * Producers don't know about each other: a term underline and a TM diff
 * highlight can land on the same characters, and they paint different CSS
 * axes (text-decoration vs background) so both should survive. This sweeps
 * every boundary and emits one span per stretch of uniform coverage, joining
 * the classes that cover it. Adjacent spans that end up identical are
 * coalesced so the DOM doesn't fragment for nothing.
 *
 * Inputs need not be sorted or disjoint. A stretch covered by no input range
 * is omitted — the renderer fills those gaps with plain text.
 */
export function mergeRanges(...lists: DecoratedRange[][]): DecoratedRange[] {
  const all: DecoratedRange[] = []
  for (const list of lists) {
    for (const r of list) if (r.end > r.start) all.push(r)
  }
  if (all.length === 0) return []

  const boundaries = [...new Set(all.flatMap(r => [r.start, r.end]))].sort((a, b) => a - b)

  const out: DecoratedRange[] = []
  for (let i = 0; i < boundaries.length - 1; i++) {
    const start = boundaries[i]
    const end = boundaries[i + 1]
    const covering = all.filter(r => r.start <= start && r.end >= end)
    if (covering.length === 0) continue

    const className = covering.map(r => r.className).filter(Boolean).join(' ')
    const title = covering.find(r => r.title)?.title

    const prev = out[out.length - 1]
    if (prev && prev.end === start && prev.className === className && prev.title === title) {
      prev.end = end
      continue
    }
    out.push({ start, end, className, ...(title ? { title } : {}) })
  }
  return out
}

/**
 * Add ghost ranges to a sorted, non-overlapping list of painted ranges.
 *
 * `mergeRanges` drops zero-width ranges, so ghosts go in afterwards. A
 * painted range that strictly contains a ghost's offset is cut in two around
 * it, because the renderer draws a ghost only between ranges. At a shared
 * offset the ghost comes first, so a replacement reads old → new.
 */
export function withGhosts(painted: DecoratedRange[], ghosts: DecoratedRange[]): DecoratedRange[] {
  if (ghosts.length === 0) return painted
  const at = [...new Set(ghosts.map(g => g.start))]

  const cut: DecoratedRange[] = []
  for (const r of painted) {
    let start = r.start
    for (const p of at.filter(p => p > r.start && p < r.end).sort((a, b) => a - b)) {
      cut.push({ ...r, start, end: p })
      start = p
    }
    cut.push(start === r.start ? r : { ...r, start })
  }

  // Stable sort: ghosts sharing an offset keep their diff order.
  return [...ghosts, ...cut].sort((a, b) =>
    a.start - b.start || Number(b.ghost !== undefined) - Number(a.ghost !== undefined))
}
