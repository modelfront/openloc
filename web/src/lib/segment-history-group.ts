import type { SegmentHistoryEvent } from '../api'
import { flattenRichText, hasXliffTags } from './xliff-tiptap'

/** One row in the History panel and the events it stands for. */
export interface HistoryRowModel {
  /** The event whose chip, source and text the row draws. */
  shown: SegmentHistoryEvent
  /** The event the row's time and approval come from: the newest event of a
   *  person's run, or the first appearance of a repeated machine result. */
  status: SegmentHistoryEvent
  /** Every event the row stands for, newest first. */
  events: SegmentHistoryEvent[]
  /** The text before this row, which the row's text is compared with. null
   *  when there is none — the first text, or the first after a clear — and
   *  then nothing on the row is marked. */
  previousText: string | null
}

/** A person's work in a step: their confirms, and confirms without changes. */
const PERSON_KINDS = new Set(['human', 'approved'])
const UNCONFIRMED = 'unconfirmed'
const CLEARED = 'cleared'

const isPerson = (e: SegmentHistoryEvent) => PERSON_KINDS.has(e.kind)

/** One person, one step assignment. The assignment id rather than the step
 *  name, so two adjacent steps that share a name stay apart. */
function sameRun(a: SegmentHistoryEvent, b: SegmentHistoryEvent): boolean {
  return a.job_step_assignment_id === b.job_step_assignment_id && a.created_by === b.created_by
}

/** Two machine events that produced the same thing — a pipeline re-run.
 *  Who triggered the run is not part of the result. */
function sameResult(a: SegmentHistoryEvent, b: SegmentHistoryEvent): boolean {
  return a.kind === b.kind
    && a.text === b.text
    && a.rich_text === b.rich_text
    && a.tm_name === b.tm_name
    && a.tm_match_percent === b.tm_match_percent
    && a.mt_engine_name === b.mt_engine_name
    && a.job_step_assignment_id === b.job_step_assignment_id
    && a.details?.predict_outcome === b.details?.predict_outcome
}

function joins(head: SegmentHistoryEvent, event: SegmentHistoryEvent): boolean {
  if (isPerson(head) || isPerson(event)) return isPerson(head) && isPerson(event) && sameRun(head, event)
  return sameResult(head, event)
}

/** The text a reader sees for an event: a tagged row renders through its
 *  chips, which show no tag content. */
function displayedText(e: SegmentHistoryEvent): string | null {
  if (e.rich_text && hasXliffTags(e.rich_text)) return flattenRichText(e.rich_text)
  return e.text
}

/**
 * Turn events into the rows the panel draws.
 *
 * A person's consecutive saves in one step are one row with their last text:
 * the steps in between are typing, not history. Every machine event — TM, MT,
 * file import, QE approval, APE, propagation, clear — keeps a row, so a hybrid
 * step reads TM → APE → the reviewer's edit. A machine result repeated by a
 * re-run is shown once, when it first appeared.
 *
 * `unconfirmed` is dropped unless it is the newest event. It only exists
 * because someone edited an approved segment, which the edit after it already
 * says. The newest one stays: the segment is sitting edited and unconfirmed
 * right now.
 *
 * Input must be newest-first, as the API returns it. This is display only:
 * every event stays in the database, and the reports count them one by one.
 */
export function groupHistoryRows(events: SegmentHistoryEvent[]): HistoryRowModel[] {
  const groups: SegmentHistoryEvent[][] = []
  for (let i = 0; i < events.length; i++) {
    const event = events[i]
    if (event.kind === UNCONFIRMED && i > 0) continue
    const last = groups[groups.length - 1]
    if (last && joins(last[0], event)) last.push(event)
    else groups.push([event])
  }

  const rows = groups.map((group): HistoryRowModel => {
    if (isPerson(group[0])) {
      return { shown: group.find(e => e.text != null) ?? group[0], status: group[0], events: group, previousText: null }
    }
    const first = group[group.length - 1]
    return { shown: first, status: first, events: group, previousText: null }
  })

  let running: string | null = null
  for (let i = rows.length - 1; i >= 0; i--) {
    const row = rows[i]
    row.previousText = running
    if (row.shown.kind === CLEARED) running = null
    else if (row.shown.text != null) running = displayedText(row.shown)
  }
  return rows
}
