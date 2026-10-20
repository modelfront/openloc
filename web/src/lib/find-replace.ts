// The result line of a find-and-replace write, shared by Replace and Replace all.

import { count } from './plural'

export interface ReplaceCounts {
  replaced: number
  segments: number
  /** Matching segments left as they were, and why. */
  locked: number
  /** Another user has the segment open in the editor. */
  held: number
  skipped_tags: number
  conflicts: number
  rejected: number
  /** The write failed; the segment is unchanged and can be retried. */
  failed: number
}

/** "Replaced 4 occurrences in 3 segments · 1 locked · 1 crosses a tag". */
export function replaceSummary(r: ReplaceCounts): string {
  const parts = [`Replaced ${count(r.replaced, 'occurrence')} in ${count(r.segments, 'segment')}`]
  if (r.locked > 0) parts.push(`${r.locked} locked`)
  if (r.held > 0) parts.push(`${r.held} being edited by someone else`)
  if (r.skipped_tags > 0) parts.push(`${r.skipped_tags} ${r.skipped_tags === 1 ? 'crosses' : 'cross'} a tag`)
  if (r.conflicts > 0) parts.push(`${r.conflicts} changed meanwhile`)
  if (r.rejected > 0) parts.push(`${r.rejected} would be left empty`)
  if (r.failed > 0) parts.push(`${r.failed} could not be saved, try again`)
  return parts.join(' · ')
}

/** Sums the results of a replace sent in chunks. */
export function sumReplaceCounts<T extends ReplaceCounts>(results: readonly T[]): ReplaceCounts {
  const sum: ReplaceCounts = { replaced: 0, segments: 0, locked: 0, held: 0, skipped_tags: 0, conflicts: 0, rejected: 0, failed: 0 }
  for (const r of results) {
    for (const k of Object.keys(sum) as (keyof ReplaceCounts)[]) sum[k] += r[k]
  }
  return sum
}
