import type { EditorIndex, Translation } from '../api'
import type { RepetitionStats, ScopeFile } from '../types/editor'
import { deriveStatus, type DisplayStatus, type StatusFacts } from './segment-status'

/** Repetition role of a segment: its source text occurs once, it is the first
 *  of several, or it repeats an earlier one. Mirrors editor.Repetition*. */
export type RepetitionRole = 0 | 1 | 2
export const REPETITION_SOURCE = 1
export const REPETITION_REPEAT = 2

/**
 * One segment as the CAT editor knows it before its text is loaded: enough to
 * count, filter, select and navigate every segment of a large file.
 */
export interface IndexRow extends StatusFacts {
  segmentId: number
  /** Null when the segment has no translation in the job's language. */
  translationId: number | null
  /** The import position; the pieces of a split share it. */
  position: number
  /** Orders the pieces of a split at one position, 0 for the first. */
  subPosition: number
  /** A join with the next row may be offered (editor.Index.JoinsNext). */
  joinsNext: boolean
  wordCount: number
  origin: string
  tmMatch: number | null
  locked: boolean
  inFlight: string
  confirmedVia: string
  /** The QE note's first six characters: enough for qeChipLabel. */
  qeNoteHead: string
  repetition: RepetitionRole
  /** Which of the editor's files the row is in: an index into `files`. */
  fileIdx: number
}

/** The cached index: its rows in document order, the repetition totals, and
 *  where each row sits. A save replaces a row but never moves one, so the
 *  lookup keeps its identity until the index is fetched again. */
export interface EditorIndexData {
  rows: IndexRow[]
  repetitionStats: RepetitionStats
  lookup: IndexLookup
  /** The editor's files in order, each with its job and its rows' range: one
   *  for a job, several for a multi-file editor. */
  files: ScopeFile[]
}

export function toIndexData(idx: EditorIndex): EditorIndexData {
  const rows: IndexRow[] = new Array(idx.segment_ids.length)
  const files = idx.files ?? []
  let fileIdx = 0
  for (let i = 0; i < rows.length; i++) {
    while (fileIdx + 1 < files.length && i >= files[fileIdx + 1].first_row) fileIdx++
    rows[i] = {
      segmentId: idx.segment_ids[i],
      translationId: idx.translation_ids[i] || null,
      position: idx.positions[i],
      subPosition: idx.sub_positions[i],
      joinsNext: idx.joins_next[i],
      wordCount: idx.word_counts[i],
      status: idx.statuses[i],
      hasText: idx.has_target[i],
      stepEdited: idx.step_edited[i],
      predicted: idx.predicted[i],
      origin: idx.origins[i],
      tmMatch: idx.tm_match[i] ?? null,
      locked: idx.locked[i],
      inFlight: idx.in_flight[i],
      confirmedVia: idx.confirmed_via[i],
      qeNoteHead: idx.qe_note_heads[i],
      repetition: idx.repetition[i] as RepetitionRole,
      fileIdx,
    }
  }
  return { rows, repetitionStats: idx.repetition_stats, lookup: indexLookup(rows), files }
}

/** The number the editor shows for a row: the import number, and the piece of
 *  a split after a dot ("3", "3.1"). Mirrors segmentedit.Number. */
export function segmentNumber(row: Pick<IndexRow, 'position' | 'subPosition'>): string {
  const n = row.position + 1
  return row.subPosition === 0 ? String(n) : `${n}.${row.subPosition}`
}

/** The index of the row a typed segment number names: the row showing that
 *  number, else for a whole number the row holding it now (a join keeps the
 *  first number of the rows it joins). Undefined when the number is not in
 *  the file. Rows are in document order. */
export function rowForNumber(rows: Pick<IndexRow, 'position' | 'subPosition'>[], input: string): number | undefined {
  const typed = input.trim()
  const exact = rows.findIndex(r => segmentNumber(r) === typed)
  if (exact >= 0) return exact
  if (!/^\d+$/.test(typed)) return undefined
  const position = Number(typed) - 1
  let at: number | undefined
  for (let i = 0; i < rows.length && rows[i].position <= position; i++) {
    if (rows[i].subPosition === 0) at = i
  }
  return at !== undefined && rows.length > 0 && position <= rows[rows.length - 1].position ? at : undefined
}

/** The index fields a saved translation row determines. */
export function indexFieldsOf(t: Translation & { in_flight_phase?: { translation_phase: string; valid: boolean } }): Partial<IndexRow> {
  return {
    translationId: t.id,
    status: t.status,
    hasText: !!t.active_text,
    stepEdited: !!t.step_edited,
    predicted: !!t.predict_outcome?.valid,
    origin: t.origin?.valid ? t.origin.translation_origin : '',
    tmMatch: t.tm_match_percent ?? null,
    locked: t.locked === true,
    ...(t.in_flight_phase ? { inFlight: t.in_flight_phase.valid ? t.in_flight_phase.translation_phase : '' } : {}),
    confirmedVia: t.confirmed_via ?? '',
    qeNoteHead: (t.qe_note ?? '').slice(0, 6),
  }
}

/** The row's display status; a segment without a translation is untranslated. */
export function displayStatusOfRow(row: IndexRow, predictedBeforeStep?: ReadonlySet<number>): DisplayStatus {
  if (row.translationId == null) return 'untranslated'
  return deriveStatus(row, { predictedBeforeStep: !!predictedBeforeStep?.has(row.translationId) })
}

/** Where each segment sits in the index, by segment id and by translation id. */
export interface IndexLookup {
  bySegment: Map<number, number>
  byTranslation: Map<number, number>
}

export function indexLookup(rows: readonly IndexRow[]): IndexLookup {
  const bySegment = new Map<number, number>()
  const byTranslation = new Map<number, number>()
  rows.forEach((r, i) => {
    bySegment.set(r.segmentId, i)
    if (r.translationId != null) byTranslation.set(r.translationId, i)
  })
  return { bySegment, byTranslation }
}

/** The rows that have a translation: what the progress bar and Complete step count. */
export function translatedRows(rows: readonly IndexRow[]): IndexRow[] {
  return rows.filter(r => r.translationId != null)
}
