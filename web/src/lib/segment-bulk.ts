// Bulk actions on the CAT editor's segment selection: the server's per-row
// result and the toast that reports it. The server takes at most
// SEGMENT_BULK_MAX ids per request; larger selections go in chunks.

import { toast } from 'sonner'
import { count } from './plural'

export const SEGMENT_BULK_MAX = 500

export type SegmentBulkAction = 'confirm' | 'lock' | 'unlock' | 'clear'

export type BulkSkipReason =
  | 'not_found' | 'locked' | 'held' | 'empty' | 'unchanged' | 'conflict' | 'qa_error' | 'tag_not_in_source' | 'auto_approved'

export interface SegmentBulkResult {
  applied: number[]
  skipped: { id: number; reason: BulkSkipReason }[]
  workflow_reopened?: boolean
  task_reopened?: boolean
}

export function chunk<T>(items: readonly T[], size: number): T[][] {
  const out: T[][] = []
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size))
  return out
}

/**
 * Runs `fn` on each SEGMENT_BULK_MAX chunk of `ids`, one after another (never
 * in parallel: each request may write the same job), and returns the results
 * in order.
 */
export async function mapChunks<T, R>(ids: readonly T[], fn: (part: T[]) => Promise<R>): Promise<R[]> {
  const out: R[] = []
  for (const part of chunk(ids, SEGMENT_BULK_MAX)) out.push(await fn(part))
  return out
}

/** The note for a bulk write's `held` count (rows someone else has open), or null for none. */
export function heldSkipNote(held: number | undefined, noun = 'segment'): string | null {
  if (!held) return null
  const one = held === 1
  return `${count(held, noun)} ${one ? 'was' : 'were'} skipped because someone else is editing ${one ? 'it' : 'them'}.`
}

/** Warns with heldSkipNote when a bulk write left held rows alone. */
export function warnHeldSkipped(held: number | undefined, noun?: string) {
  const note = heldSkipNote(held, noun)
  if (note) toast.warning(note)
}

export function mergeResults(results: readonly SegmentBulkResult[]): SegmentBulkResult {
  return {
    applied: results.flatMap(r => r.applied),
    skipped: results.flatMap(r => r.skipped),
    workflow_reopened: results.some(r => r.workflow_reopened),
    task_reopened: results.some(r => r.task_reopened),
  }
}

const DONE: Record<SegmentBulkAction, string> = { confirm: 'Confirmed', lock: 'Locked', unlock: 'Unlocked', clear: 'Cleared' }

function skipLabel(action: SegmentBulkAction, reason: BulkSkipReason): string {
  switch (reason) {
    case 'not_found': return 'not in this task'
    case 'locked': return 'locked'
    case 'held': return 'being edited by someone else'
    case 'empty': return action === 'confirm' ? 'empty' : 'already empty'
    case 'conflict': return 'changed meanwhile'
    case 'qa_error': return 'with QA errors'
    case 'tag_not_in_source': return 'with a tag the source does not have'
    case 'auto_approved': return 'auto-approved (confirm one by one)'
    case 'unchanged':
      if (action === 'lock') return 'already locked'
      if (action === 'unlock') return 'already unlocked'
      if (action === 'confirm') return 'already confirmed'
      return 'unchanged'
  }
}

/** "Locked 7 segments · 3 already locked". */
export function bulkSummary(action: SegmentBulkAction, res: SegmentBulkResult): string {
  const counts = new Map<string, number>()
  for (const s of res.skipped) {
    const label = skipLabel(action, s.reason)
    counts.set(label, (counts.get(label) ?? 0) + 1)
  }
  const parts = [`${DONE[action]} ${count(res.applied.length, 'segment')}`]
  for (const [label, n] of counts) parts.push(`${n} ${label}`)
  return parts.join(' · ')
}
