/**
 * Duration formatting for job processing times.
 *
 * Deliberately NOT the same function as `components/debug/format.ts`'s
 * `formatDuration`, which caps at seconds by contract (`61500 → '61.5s'`)
 * because it labels individual LLM request latencies where "1.0m" would read
 * as an outage. Job stages routinely run for minutes — an MT phase over a large
 * file is normal at 4m — so this one carries through to minutes and hours.
 * Two contracts, two names, rather than one function with a mode flag.
 */

const SECOND = 1000
const MINUTE = 60 * SECOND
const HOUR = 60 * MINUTE

/** `0ms` · `618ms` · `2.6s` · `4.2m` · `1.3h` */
export function formatDurationMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  if (ms < SECOND) return `${Math.round(ms)}ms`
  if (ms < MINUTE) return `${(ms / SECOND).toFixed(1)}s`
  if (ms < HOUR) return `${(ms / MINUTE).toFixed(1)}m`
  return `${(ms / HOUR).toFixed(1)}h`
}

/** `0.9091 → '91%'`. Rounds to whole percent; sub-1% shares floor to `<1%`
 *  rather than `0%` so a stage that ran at all never reads as free. */
export function formatShare(share: number): string {
  if (!Number.isFinite(share) || share <= 0) return '0%'
  const pct = share * 100
  if (pct < 1) return '<1%'
  return `${Math.round(pct)}%`
}

/** Compact count: `1234 → '1,234'`. */
export function formatCount(n: number): string {
  return n.toLocaleString()
}
