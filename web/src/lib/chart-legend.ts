// What a chart legend shows and does: a click picks the entries to show, and a
// pie's entries carry their share of what is still shown.
import { toNumber } from './chart-pivot'

/**
 * The hidden entries after a click on `name`, out of `all`. With everything
 * shown, the click keeps that entry alone; after that, each click adds an
 * entry back or takes one out. Taking out the last one shown, or adding the
 * last one hidden, shows everything again: nothing hidden means no pick.
 */
export function toggleSelected(hidden: ReadonlySet<string>, name: string, all: readonly string[]): Set<string> {
  if (hidden.size === 0) return new Set(all.filter(n => n !== name))
  const next = new Set(hidden)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  return all.every(n => next.has(n)) ? new Set() : next
}

/**
 * Each slice's share of the slices still shown, by name. A hidden slice, or
 * every slice when the shown ones add up to nothing, has none.
 */
export function sliceShares(
  rows: Record<string, unknown>[],
  nameKey: string,
  valueKey: string,
  hidden: ReadonlySet<string>,
): Map<string, number | null> {
  let total = 0
  for (const r of rows) {
    if (!hidden.has(String(r[nameKey]))) total += toNumber(r[valueKey])
  }
  const out = new Map<string, number | null>()
  for (const r of rows) {
    const name = String(r[nameKey])
    out.set(name, hidden.has(name) || total <= 0 ? null : toNumber(r[valueKey]) / total)
  }
  return out
}

/** `32%`; a slice too small to round to 1% reads `<1%`, never `0%`. */
export function formatShare(share: number): string {
  if (share > 0 && share < 0.005) return '<1%'
  return `${Math.round(share * 100)}%`
}

/** A series' total over every x. */
export function seriesSum(data: Record<string, unknown>[], name: string): number {
  let sum = 0
  for (const row of data) sum += toNumber(row[name])
  return sum
}

/** A series' mean over the x values it has, for ranking averages; 0 when it has none. */
export function seriesMean(data: Record<string, unknown>[], name: string): number {
  let sum = 0
  let n = 0
  for (const row of data) {
    if (row[name] == null) continue
    sum += toNumber(row[name])
    n++
  }
  return n ? sum / n : 0
}

/**
 * Biggest first, so the legend and the chart lead with what matters most.
 * Equal sizes keep their order; `last` stays at the end whatever its size
 * (the "Other" band).
 */
export function orderBySize<T>(items: T[], size: (item: T) => number, last?: (item: T) => boolean): T[] {
  return items
    .map((item, i) => ({ item, i, size: size(item), last: last?.(item) ?? false }))
    .sort((a, b) => Number(a.last) - Number(b.last) || b.size - a.size || a.i - b.i)
    .map(x => x.item)
}

// Units are plural nouns; one of a thing reads in the singular.
const SINGULAR: Record<string, string> = {
  words: 'word', segments: 'segment', characters: 'character', minutes: 'minute',
  tokens: 'token', issues: 'issue', calls: 'call', events: 'event',
}

/** `55 words`, `1 word`; the bare number when the unit is unknown. */
export function withUnit(formatted: string, value: number, unit?: string): string {
  if (!unit) return formatted
  return `${formatted} ${value === 1 ? SINGULAR[unit] ?? unit : unit}`
}

/**
 * The series under a value on the Y axis at one x: in a stack (bottom first,
 * in drawing order), the band whose range holds it; otherwise the series
 * closest to it. Null above the stack, below zero, or with nothing shown.
 */
export function seriesAt(row: Record<string, unknown>, series: string[], value: number, stacked: boolean): string | null {
  if (!Number.isFinite(value) || value < 0) return null
  if (stacked) {
    let top = 0
    for (const name of series) {
      top += toNumber(row[name])
      if (value <= top) return name
    }
    return null
  }
  let best: string | null = null
  let gap = Infinity
  for (const name of series) {
    if (row[name] == null) continue
    const d = Math.abs(toNumber(row[name]) - value)
    if (d < gap) {
      gap = d
      best = name
    }
  }
  return best
}
