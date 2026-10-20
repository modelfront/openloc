// Axis and tooltip labels for time-grained charts.
//
// The metrics layer aliases a grain column to the grain's own name, so a chart
// whose `x_key` is `day` / `week` / `month` is exactly the chart whose x values
// are dates — `YYYY-MM-DD`, from `date_trunc(...)::date`. Anything else (a
// customer, a language) passes through untouched.
//
// Axis ticks are short because they compete for width; the tooltip is where the
// year goes, since that is the label you get when you actually want to know.

export type TimeGrain = 'day' | 'week' | 'month'

const GRAINS: Record<string, TimeGrain> = { day: 'day', week: 'week', month: 'month' }

/** The grain this x axis plots, or null when the axis isn't a time axis. */
export function grainOf(xKey: string | undefined): TimeGrain | null {
  return (xKey && GRAINS[xKey]) || null
}

/**
 * Parses `YYYY-MM-DD` into a LOCAL date.
 *
 * `new Date('2026-09-14')` parses as UTC midnight, which prints as the 13th
 * anywhere west of Greenwich — an off-by-one-day on every label for half the
 * world. Building from the parts keeps the date the server meant.
 */
export function parseLocalDate(value: unknown): Date | null {
  if (typeof value !== 'string') return null
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!m) return null
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])]
  const date = new Date(y, mo - 1, d)
  // Rejects 2026-02-30 and friends: the Date constructor rolls them over
  // silently, and a label for a day that doesn't exist is worse than the raw
  // string.
  if (date.getFullYear() !== y || date.getMonth() !== mo - 1 || date.getDate() !== d) return null
  return date
}

/** Month name only — localized, so "Sep" follows the reader's language. */
const monthName = (d: Date) => d.toLocaleDateString(undefined, { month: 'short' })

/**
 * Day and month, in that order.
 *
 * Deliberately NOT `toLocaleDateString({day, month})`, which yields "Sep 18" in
 * en-US: the axis format is fixed so the same chart reads the same for everyone
 * sharing a link. Only the month NAME is localized.
 */
const dayAndMonth = (d: Date) => `${d.getDate()} ${monthName(d)}`

/** A week runs from its `date_trunc` start through the sixth day after it. */
function weekEnd(start: Date): Date {
  return new Date(start.getFullYear(), start.getMonth(), start.getDate() + 6)
}

/** Short label for an axis tick — no year, because width is scarce. */
export function formatTick(grain: TimeGrain, value: unknown): string {
  const d = parseLocalDate(value)
  if (!d) return String(value ?? '')
  switch (grain) {
    case 'day':
      return dayAndMonth(d)
    case 'week':
      return `${dayAndMonth(d)} - ${dayAndMonth(weekEnd(d))}`
    case 'month':
      return monthName(d)
  }
}

/** Full label for a tooltip — always carries the year. */
export function formatTooltipLabel(grain: TimeGrain, value: unknown): string {
  const d = parseLocalDate(value)
  if (!d) return String(value ?? '')
  switch (grain) {
    case 'day':
      return `${dayAndMonth(d)} ${d.getFullYear()}`
    case 'week': {
      const end = weekEnd(d)
      // A week spanning New Year needs both years to be unambiguous; within one
      // year the leading year would just be noise.
      return d.getFullYear() === end.getFullYear()
        ? `${dayAndMonth(d)} - ${dayAndMonth(end)} ${end.getFullYear()}`
        : `${dayAndMonth(d)} ${d.getFullYear()} - ${dayAndMonth(end)} ${end.getFullYear()}`
    }
    case 'month':
      return `${monthName(d)} ${d.getFullYear()}`
  }
}

/**
 * Minimum pixels recharts must leave between two x-axis labels, by grain.
 *
 * Recharts drops ticks that would land closer than this, so the axis thins
 * itself to fit the width it actually has — more labels on a full-page chart,
 * fewer in a two-column card, without either being told about the other.
 *
 * The numbers are label WIDTHS at 11px, plus breathing room. A weekly label is
 * a span ("31 Aug - 6 Sep", ~75px) and needs far more room than a month name,
 * which is why one gap for all three reads as cramped on weeks or sparse on
 * months.
 */
const TICK_GAP: Record<TimeGrain, number> = { day: 28, week: 72, month: 16 }

export function tickGapFor(grain: TimeGrain): number {
  return TICK_GAP[grain]
}

// Below this a tick is short enough to print in full.
const COMPACT_FROM = 10_000

/**
 * Chart number: `55K`, `110K`, `1.3M` from 10,000 up, the full number with
 * separators below it. Used for Y axis ticks, which it keeps a few characters
 * wide, and for tooltip values, so both read the same way.
 */
export function formatAxisNumber(value: unknown, locale?: string): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return String(value)
  if (Math.abs(value) < COMPACT_FROM) return value.toLocaleString(locale)
  return new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}
