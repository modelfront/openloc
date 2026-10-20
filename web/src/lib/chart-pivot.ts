// Turning one dimension's VALUES into separate chart series.
//
// The metrics layer returns long rows — one per (x, dimension) pair:
//
//   week=2026-08-10  kind=human  words=29
//   week=2026-08-10  kind=mt     words=29
//
// recharts plots wide rows: one row per x, one key per series. Without a pivot
// the renderer is handed two rows for the same x and told to plot a single
// `words` series, so it draws one band and the second row is invisible.
//
// `series_key` names the dimension to split on; `series[0]` is the value column
// to read. The result is one row per x with a key per distinct dimension value.
//
// An average column (`spec.averages`) is not part of a whole, so its bands are
// drawn side by side: not stacked, not summed, a missing value left empty, and
// the smallest bands dropped instead of summed into "Other".
import type { ChartSpec } from '../api'

export interface PivotedChart {
  data: Record<string, unknown>[]
  /** Series keys to plot, in first-seen order. */
  series: string[]
  /** Parts of one whole stack; independent value columns don't. */
  stacked: boolean
}

/**
 * Most bands a split may draw. A dimension's cardinality is unbounded — a
 * tenant can have hundreds of projects — and past the palette size colours
 * repeat, which is the failure this whole feature exists to fix. Everything
 * below the top of the ranking collapses into one honest bucket instead.
 */
export const MAX_SERIES = 8
export const OTHER_SERIES = 'Other'

/**
 * Widens `spec.data` when `series_key` is set; otherwise passes the spec
 * through unchanged, so a chart plotting plain value columns is untouched.
 */
export function pivotChart(spec: ChartSpec): PivotedChart {
  const key = spec.series_key
  const valueCol = spec.series[0]
  if (!key || !valueCol) {
    return { data: spec.data, series: spec.series, stacked: false }
  }

  // Insertion-ordered so the x axis keeps the order the server sent (time
  // ascending, or value descending for categorical results).
  const byX = new Map<unknown, Record<string, unknown>>()
  const series: string[] = []
  const average = spec.averages?.includes(valueCol) ?? false

  for (const row of spec.data) {
    const x = row[spec.x_key]
    const name = categoryLabel(row[key])

    let out = byX.get(x)
    if (!out) {
      out = { [spec.x_key]: x }
      byX.set(x, out)
    }
    if (!series.includes(name)) series.push(name)
    // Repeated (x, name) pairs sum rather than overwrite: the same pair can
    // appear twice when a chart groups by a third dimension it doesn't plot.
    // Averages can't be summed, and a null average stays a gap.
    if (average) {
      if (out[name] === undefined && row[valueCol] != null) out[name] = toNumber(row[valueCol])
      continue
    }
    out[name] = (typeof out[name] === 'number' ? (out[name] as number) : 0) + toNumber(row[valueCol])
  }

  const data = [...byX.values()]
  const kept = capSeries(data, series, !average)
  if (average) return { data, series: kept, stacked: false }

  // Absent combinations become 0 rather than gaps — a stack with holes reads
  // as a drop in the total, which is the opposite of what happened.
  for (const row of data) {
    for (const name of kept) {
      if (row[name] === undefined) row[name] = 0
    }
  }
  return { data, series: kept, stacked: true }
}

/**
 * Keeps the biggest bands and, when `foldOther`, folds the rest into one
 * `Other`, mutating each row to match. Without it (averages, which can't be
 * summed) the rest are dropped. Ranking is by total across the whole window, so
 * a band that is large only in one bucket still survives if it matters overall.
 *
 * The kept bands stay in their original (server) order rather than being
 * re-sorted by size: a chart shouldn't reshuffle its colours when last week's
 * numbers shift. `Other` goes last, where a reader expects it.
 */
function capSeries(data: Record<string, unknown>[], series: string[], foldOther: boolean): string[] {
  if (series.length <= MAX_SERIES) return series

  const totals = new Map<string, number>()
  for (const name of series) {
    let sum = 0
    for (const row of data) sum += toNumber(row[name])
    totals.set(name, sum)
  }
  const keep = new Set(
    [...series]
      .sort((a, b) => (totals.get(b) ?? 0) - (totals.get(a) ?? 0))
      .slice(0, foldOther ? MAX_SERIES - 1 : MAX_SERIES),
  )

  for (const row of data) {
    let other = 0
    for (const name of series) {
      if (keep.has(name)) continue
      other += toNumber(row[name])
      delete row[name]
    }
    if (foldOther) row[OTHER_SERIES] = other
  }
  const kept = series.filter(n => keep.has(n))
  return foldOther ? [...kept, OTHER_SERIES] : kept
}

/**
 * A pie's slices, biggest first, capped like a split's bands: the top
 * `MAX_SERIES - 1` and one `Other` holding the rest (and any `Other` the server
 * already folded). Averages drop the rest instead, since they cannot be summed.
 */
export function capSlices(rows: Record<string, unknown>[], nameKey: string, valueKey: string, foldOther: boolean): Record<string, unknown>[] {
  const other = rows.filter(r => r[nameKey] === OTHER_SERIES)
  const rest = rows.filter(r => r[nameKey] !== OTHER_SERIES)
  if (rest.length + (other.length ? 1 : 0) <= MAX_SERIES) return rows
  const kept = rest.slice(0, MAX_SERIES - 1)
  if (!foldOther) return kept
  const folded = [...rest.slice(MAX_SERIES - 1), ...other]
  return [...kept, { [nameKey]: OTHER_SERIES, [valueKey]: folded.reduce((sum, r) => sum + toNumber(r[valueKey]), 0) }]
}

// A NULL dimension normalizes to "" upstream; label it rather than drawing a
// series, slice or tick with an empty name.
function categoryLabel(v: unknown): string {
  return String(v ?? '') || '(none)'
}

/** Rows with `key`'s empty values named "(none)" — for charts that plot a
 *  dimension's values directly (a pie's slices, a categorical axis). */
export function labelCategories(rows: Record<string, unknown>[], key: string): Record<string, unknown>[] {
  return rows.map(r => ({ ...r, [key]: categoryLabel(r[key]) }))
}

/** A chart value as a number; anything not numeric counts as 0. */
export function toNumber(v: unknown): number {
  const n = typeof v === 'number' ? v : Number(v)
  return Number.isFinite(n) ? n : 0
}
