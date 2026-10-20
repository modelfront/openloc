// The Complete step dialog: what is left unapproved, how to say it, and which
// segments stopped "Approve all and complete".

import type { Translation } from '../api'
import type { SegmentBulkResult } from './segment-bulk'
import { count } from './plural'

export interface UnapprovedCounts {
  /** Unapproved segments with text — Draft and Pretranslated alike. */
  drafts: number
  /** Segments with no text; "Approve all" cannot approve them. */
  untranslated: number
}

/** Counts the segments that are neither Approved nor Auto-approved. */
export function unapprovedCounts(translations: readonly Translation[]): UnapprovedCounts {
  let drafts = 0
  let untranslated = 0
  for (const t of translations) {
    if (t.status === 'approved' || t.status === 'auto_approved') continue
    if (t.active_text) drafts++
    else untranslated++
  }
  return { drafts, untranslated }
}

/** "There are 3 drafts and 2 untranslated segments." A part that is zero is
 *  left out. Called only when at least one count is above zero. */
export function unapprovedSentence({ drafts, untranslated }: UnapprovedCounts): string {
  const parts: string[] = []
  if (drafts > 0) parts.push(count(drafts, 'draft'))
  if (untranslated > 0) parts.push(`${untranslated} untranslated ${untranslated === 1 ? 'segment' : 'segments'}`)
  const verb = (drafts > 0 ? drafts : untranslated) === 1 ? 'is' : 'are'
  return `There ${verb} ${parts.join(' and ')}.`
}

/** The segments "Approve all" left unapproved, which stop the step from completing. */
export interface ApproveBlocked {
  /** A QA error, a tag the source lacks or a change made meanwhile: the user fixes these. */
  toFix: number[]
  /** Open in someone else's editor. */
  held: number[]
}

/**
 * The 1-based segment numbers "Approve all" could not approve, in file order,
 * or null when none stops the step from completing. Rows it never approves
 * (empty, already approved, auto-approved) do not count. `positionOf` maps a
 * translation id to its 0-based segment position.
 */
export function blockedSegmentNumbers(
  skipped: SegmentBulkResult['skipped'],
  positionOf: (translationId: number) => number | undefined,
): ApproveBlocked | null {
  const out: ApproveBlocked = { toFix: [], held: [] }
  for (const s of skipped) {
    const list = s.reason === 'held' ? out.held
      : s.reason === 'qa_error' || s.reason === 'tag_not_in_source' || s.reason === 'conflict' ? out.toFix
        : null
    const pos = positionOf(s.id)
    if (list && pos !== undefined) list.push(pos + 1)
  }
  if (out.toFix.length + out.held.length === 0) return null
  out.toFix.sort((a, b) => a - b)
  out.held.sort((a, b) => a - b)
  return out
}
