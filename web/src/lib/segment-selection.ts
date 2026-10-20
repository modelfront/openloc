// Multi-segment selection in the CAT editor: which translations are selected,
// and how a click changes that. Ids are translation ids, so a selection
// survives filtering and refetches. `ordered` is always the filtered list in
// display order — a range and "select all" follow what the user sees.

import { count } from './plural'

export type HeaderState = 'none' | 'some' | 'all'

/** Adds or removes `ids` as a group. */
export function setMany(prev: ReadonlySet<number>, ids: readonly number[], on: boolean): Set<number> {
  const next = new Set(prev)
  for (const id of ids) {
    if (on) next.add(id)
    else next.delete(id)
  }
  return next
}

/**
 * The selection after a click on `id`'s checkbox. A plain click toggles the
 * one row. Shift+click sets every row from `anchor` to `id` (both included,
 * in `ordered`) to the state the clicked row is switching to; with no anchor
 * on screen it is a plain click.
 */
export function clickSelect(
  prev: ReadonlySet<number>,
  ordered: readonly number[],
  id: number,
  anchor: number | null,
  shift: boolean,
): Set<number> {
  const on = !prev.has(id)
  const from = shift && anchor !== null ? ordered.indexOf(anchor) : -1
  const to = ordered.indexOf(id)
  if (from === -1 || to === -1) return setMany(prev, [id], on)
  const [lo, hi] = from < to ? [from, to] : [to, from]
  return setMany(prev, ordered.slice(lo, hi + 1), on)
}

/** The selected ids that are on screen, in display order. */
export function visibleSelection(selected: ReadonlySet<number>, ordered: readonly number[]): number[] {
  if (selected.size === 0) return []
  return ordered.filter(id => selected.has(id))
}

/** What the header checkbox shows for the rows on screen. */
export function headerState(selected: ReadonlySet<number>, ordered: readonly number[]): HeaderState {
  const n = visibleSelection(selected, ordered).length
  if (n === 0) return 'none'
  return n === ordered.length ? 'all' : 'some'
}

/** "12 segments selected", plus the selected rows the filter hides. */
export function selectionLabel(visible: number, total: number): string {
  const hidden = total - visible
  return hidden > 0
    ? `${count(visible, 'segment')} selected · ${hidden} hidden by filter`
    : `${count(visible, 'segment')} selected`
}
