import { describe, it, expect } from 'vitest'
import { groupHistoryRows, type HistoryRowModel } from './segment-history-group'
import type { SegmentHistoryEvent } from '../api'

let nextId = 0

// Newest-first is the order the API returns and the grouper assumes, so the
// tests build their lists that way too.
function ev(over: Partial<SegmentHistoryEvent> = {}): SegmentHistoryEvent {
  return {
    id: ++nextId, kind: 'human', text: 'Hola', rich_text: null,
    created_at: '2026-09-15T10:00:00Z',
    tm_match_percent: null, edit_distance: 3, details: {},
    created_by: 7, created_by_name: 'Maria', created_by_email: 'maria@example.com',
    mt_engine_name: null, tm_name: null, step_name: 'Translation',
    job_step_assignment_id: 1,
    ...over,
  }
}

/** A pipeline event: nobody's edit, and no workflow step behind it. */
const machine = (over: Partial<SegmentHistoryEvent>) =>
  ev({ created_by: null, created_by_name: null, created_by_email: null,
       step_name: null, job_step_assignment_id: null, edit_distance: null, ...over })

const tm = (over: Partial<SegmentHistoryEvent> = {}) =>
  machine({ kind: 'tm', text: 'Denuncia enviada', tm_name: 'Acme TM', tm_match_percent: 98, ...over })

const shape = (rows: HistoryRowModel[]) => rows.map(r => `${r.shown.kind}×${r.events.length}`)

describe('groupHistoryRows', () => {
  // Re-running pre-translation fills the same segment from the same TM entry
  // again. Each run writes a row, and none of them changed anything.
  it('shows identical pre-translation re-runs once, at the time the result first appeared', () => {
    const rows = groupHistoryRows([
      tm({ created_at: '2026-09-15T12:00:00Z' }),
      tm({ created_at: '2026-09-15T10:00:00Z' }),
      tm({ created_at: '2026-09-15T08:00:00Z' }),
      tm({ created_at: '2026-09-15T06:00:00Z' }),
    ])
    expect(shape(rows)).toEqual(['tm×4'])
    expect(rows[0].status.created_at).toBe('2026-09-15T06:00:00Z')
  })

  it('keeps a re-run that produced a different result', () => {
    const rows = groupHistoryRows([tm({ tm_match_percent: 100 }), tm({ tm_match_percent: 98 })])
    expect(shape(rows)).toEqual(['tm×1', 'tm×1'])
  })

  // A hybrid step runs QE/APE when it starts, and then a person works in the
  // same step. The APE rewrite is what the person started from, so it keeps a
  // row of its own, and each row is compared with the one before it.
  it('keeps every machine event of a hybrid step and collapses the reviewer’s edits', () => {
    const rows = groupHistoryRows([
      ev({ text: 'Denuncia recibida por el equipo', job_step_assignment_id: 5, step_name: 'AI review' }),
      ev({ text: 'Denuncia recibida', job_step_assignment_id: 5, step_name: 'AI review' }),
      machine({ kind: 'ape', text: 'Denuncia enviada.', job_step_assignment_id: 5, step_name: 'AI review',
                details: { predict_outcome: 'ape_needs_review' } }),
      tm(),
    ])
    expect(shape(rows)).toEqual(['human×2', 'ape×1', 'tm×1'])
    expect(rows[0].shown.text).toBe('Denuncia recibida por el equipo')
    expect(rows.map(r => r.previousText)).toEqual(['Denuncia enviada.', 'Denuncia enviada', null])
  })

  it('keeps MT and the QE approval of it as two rows', () => {
    const rows = groupHistoryRows([
      machine({ kind: 'auto_approved', text: null, job_step_assignment_id: 5, details: { predict_outcome: 'approved' } }),
      machine({ kind: 'mt', text: 'Informe enviado', mt_engine_name: 'DeepL' }),
    ])
    expect(shape(rows)).toEqual(['auto_approved×1', 'mt×1'])
  })

  it('does not merge machine events that ended differently', () => {
    const ape = { kind: 'ape', text: 'Hola.', job_step_assignment_id: 5 }
    const rows = groupHistoryRows([
      machine({ ...ape, details: { predict_outcome: 'ape_approved' } }),
      machine({ ...ape, details: { predict_outcome: 'ape_needs_review' } }),
    ])
    expect(shape(rows)).toEqual(['ape×1', 'ape×1'])
  })

  it('collapses one person’s saves in one step into the last one', () => {
    const rows = groupHistoryRows([ev({ text: 'C' }), ev({ text: 'B' }), ev({ text: 'A' })])
    expect(shape(rows)).toEqual(['human×3'])
    expect(rows[0].shown.text).toBe('C')
  })

  it('folds an approval at the end of a run into it, and still shows the run’s text', () => {
    const rows = groupHistoryRows([
      ev({ kind: 'approved', text: null, edit_distance: null }),
      ev({ text: 'B' }),
      ev({ text: 'A' }),
    ])
    expect(shape(rows)).toEqual(['human×3'])
    expect(rows[0].shown.text).toBe('B')
    expect(rows[0].status.kind).toBe('approved')
  })

  it('keeps two people in one step apart', () => {
    const rows = groupHistoryRows([ev({ created_by: 8 }), ev({ created_by: 7 })])
    expect(shape(rows)).toEqual(['human×1', 'human×1'])
  })

  // A rejected step reopens the earlier assignment under the same id. The
  // reviewer's event between the two passes keeps them apart.
  it('keeps a reject bounce as separate rows', () => {
    const rows = groupHistoryRows([
      ev({ job_step_assignment_id: 1 }), ev({ job_step_assignment_id: 2, created_by: 8 }), ev({ job_step_assignment_id: 1 }),
    ])
    expect(shape(rows)).toEqual(['human×1', 'human×1', 'human×1'])
  })

  // A reject writes no history row, so when the reviewer never touched the
  // segment the rework sits next to the first pass, under the same reopened
  // assignment, and joins it.
  it('merges rework into the first pass when the reviewer left no event', () => {
    const rows = groupHistoryRows([
      ev({ text: 'rework', job_step_assignment_id: 1 }),
      ev({ text: 'first pass', job_step_assignment_id: 1 }),
    ])
    expect(shape(rows)).toEqual(['human×2'])
    expect(rows[0].shown.text).toBe('rework')
  })

  // RunAIStep stamps its rows with the person who started the run and the step
  // it ran in: the same person and assignment as the edits that follow.
  it('keeps AI-step rows apart from the same person’s edits in that step', () => {
    const run = { created_by: 7, job_step_assignment_id: 5, edit_distance: null }
    const ape = groupHistoryRows([
      ev({ ...run, text: 'Denuncia recibida' }),
      ev({ ...run, kind: 'ape', text: 'Denuncia enviada.', details: { predict_outcome: 'ape_needs_review' } }),
      tm(),
    ])
    expect(shape(ape)).toEqual(['human×1', 'ape×1', 'tm×1'])
    const qe = groupHistoryRows([
      ev({ ...run, text: 'Informe enviado.' }),
      ev({ ...run, kind: 'auto_approved', text: null, details: { predict_outcome: 'approved' } }),
      machine({ kind: 'mt', text: 'Informe enviado' }),
    ])
    expect(shape(qe)).toEqual(['human×1', 'auto_approved×1', 'mt×1'])
  })

  it('keeps two steps with the same name apart', () => {
    const rows = groupHistoryRows([
      ev({ job_step_assignment_id: 2, step_name: 'Review' }),
      ev({ job_step_assignment_id: 1, step_name: 'Review' }),
    ])
    expect(shape(rows)).toEqual(['human×1', 'human×1'])
  })

  // A reviewer who confirms without changing anything leaves a status-only
  // row. The next edit is still compared with the last text anyone wrote.
  it('shows a confirm without changes as its own row, and compares past it', () => {
    const rows = groupHistoryRows([
      ev({ text: 'Y', created_by: 9, job_step_assignment_id: 3 }),
      ev({ kind: 'approved', text: null, created_by: 8, job_step_assignment_id: 2 }),
      ev({ text: 'X', job_step_assignment_id: 1 }),
      tm({ text: 'T' }),
    ])
    expect(shape(rows)).toEqual(['human×1', 'approved×1', 'human×1', 'tm×1'])
    expect(rows[0].previousText).toBe('X')
  })

  it('compares nothing with a translation that was cleared', () => {
    const rows = groupHistoryRows([ev({ text: 'Y' }), machine({ kind: 'cleared', text: null }), tm({ text: 'X' })])
    expect(shape(rows)).toEqual(['human×1', 'cleared×1', 'tm×1'])
    expect(rows[0].previousText).toBeNull()
  })

  it('compares nothing with the oldest row', () => {
    const rows = groupHistoryRows([ev({ text: 'Y' }), tm({ text: 'X' })])
    expect(rows[1].previousText).toBeNull()
  })

  // The panel draws a tagged row through its chips, so the comparison is with
  // what the reader sees, not with the stored plain text.
  it('compares with the text a tagged row shows', () => {
    const rows = groupHistoryRows([
      ev({ text: 'Click Save now' }),
      tm({ text: 'Click {1}Save', rich_text: 'Click <ph id="1">{1}</ph>Save' }),
    ])
    expect(rows[0].previousText).toBe('Click Save')
  })

  // Editing an approved segment writes `unconfirmed`. Between edits it says
  // nothing the next edit doesn't; as the newest event it says the segment is
  // sitting edited and unconfirmed right now.
  it('drops an unconfirmed event unless it is the newest', () => {
    expect(shape(groupHistoryRows([
      ev({ text: 'B' }), ev({ kind: 'unconfirmed', text: null }), ev({ text: 'A' }),
    ]))).toEqual(['human×2'])
    expect(shape(groupHistoryRows([
      ev({ kind: 'unconfirmed', text: null }), ev({ text: 'A' }),
    ]))).toEqual(['unconfirmed×1', 'human×1'])
  })

  it('returns nothing for an empty list', () => {
    expect(groupHistoryRows([])).toEqual([])
  })
})
