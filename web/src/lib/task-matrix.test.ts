// The task matrix model: what each cell shows, what the filters keep, and what a click selects. The matrix component only
// renders this, so the behaviour is pinned here.

import { describe, it, expect } from 'vitest'
import {
  applySelection,
  buildTaskMatrix,
  languageLabelRows,
  languageRuns,
  cellHasNote,
  rowHasNotes,
  cellSelection,
  columnTaskIds,
  currentTaskMatrix,
  parseTaskMatrixFilters,
  rowTaskIds,
  selectedTasks,
  selectModeFor,
  stepSections,
  stepTones,
  taskState,
  type TaskMatrixFilters,
} from './task-matrix'
import { makeJob } from '@/test/fixtures/job'
import { makeProjectTask } from '@/test/fixtures/project-task'

const NOW = new Date('2026-09-24T12:00:00Z')
const TRANSLATE = { workflow_step_id: 100, step_name: 'translate', step_order: 0 }
const REVIEW = { workflow_step_id: 200, step_name: 'review', step_order: 1 }

// Two files × two languages, a two-step workflow:
//   guide/de  translate done (Ann)     review in progress (Bob, due in 2 days)
//   guide/fr  translate in progress (Ann, overdue)   review pending
//   api/de    translate FAILED (unassigned)          review pending
//   api/fr    translate done (Cat)     review done (Bob)
const jobs = [
  makeJob({ id: 1, file_id: 10, file_name: 'guide.docx', target_language: 'de', word_count: 100, workflow_step: step(12) }),
  makeJob({ id: 2, file_id: 10, file_name: 'guide.docx', target_language: 'fr', word_count: 100, workflow_step: step(21) }),
  makeJob({ id: 3, file_id: 20, file_name: 'api.md', segment_count: 4, target_language: 'de', word_count: 40, pipeline_status: 'failed', error_message: 'MT engine rejected the API key.', workflow_step: step(31) }),
  makeJob({ id: 4, file_id: 20, file_name: 'api.md', segment_count: 4, target_language: 'fr', word_count: 40, workflow_step: step(42) }),
]
const tasks = [
  makeProjectTask({ id: 11, job_id: 1, ...TRANSLATE, status: 'completed', assigned_to: 7, assignee_name: 'Ann', deadline: '2026-09-01T00:00:00Z', completed_at: '2026-09-02T00:00:00Z' }),
  makeProjectTask({ id: 12, job_id: 1, ...REVIEW, status: 'in_progress', assigned_to: 8, assignee_name: 'Bob', deadline: '2026-09-26T12:00:00Z' }),
  makeProjectTask({ id: 21, job_id: 2, ...TRANSLATE, status: 'in_progress', assigned_to: 7, assignee_name: 'Ann', deadline: '2026-09-20T00:00:00Z' }),
  makeProjectTask({ id: 22, job_id: 2, ...REVIEW, status: 'pending' }),
  makeProjectTask({ id: 31, job_id: 3, ...TRANSLATE, status: 'in_progress' }),
  makeProjectTask({ id: 32, job_id: 3, ...REVIEW, status: 'pending' }),
  makeProjectTask({ id: 41, job_id: 4, ...TRANSLATE, status: 'completed', assigned_to: 9, assignee_name: 'Cat', completed_at: '2026-09-03T00:00:00Z' }),
  makeProjectTask({ id: 42, job_id: 4, ...REVIEW, status: 'completed', assigned_to: 8, assignee_name: 'Bob', completed_at: '2026-09-05T00:00:00Z' }),
]

function step(id: number) {
  return { id, step_name: 'x', step_order: 0, status: 'in_progress', is_last_step: false, is_ai_step: false, deadline: null }
}

const build = (filters: TaskMatrixFilters = {}) =>
  buildTaskMatrix(jobs, tasks, filters, NOW)

describe('taskState', () => {
  it('turns only the failed job\'s current step red', () => {
    const failed = jobs[2]
    expect(taskState(tasks[4], failed)).toBe('error') // id 31 = its workflow_step
    expect(taskState(tasks[5], failed)).toBe('not_started') // the next step is untouched
  })

  it('reads a step sent back by a reviewer as not started', () => {
    expect(taskState(makeProjectTask({ status: 'rejected' }), makeJob())).toBe('not_started')
  })
})

describe('buildTaskMatrix', () => {
  it('puts steps across in workflow order and one row per job, by language then file', () => {
    const m = build()
    expect(m.columns.map(c => c.name)).toEqual(['translate', 'review'])
    expect(m.rows.map(r => `${r.targetLanguage}/${r.fileName}`)).toEqual([
      'de/api.md', 'de/guide.docx', 'fr/api.md', 'fr/guide.docx',
    ])
  })

  it('shows each task\'s own assignee, deadline, words and state', () => {
    const guideDe = build().rows.find(r => r.key === 'job:1')!
    const review = guideDe.cells[200]!
    expect(review).toMatchObject({ state: 'in_progress', assignees: [{ name: 'Bob', count: 1 }], deadline: '2026-09-26T12:00:00Z', words: 100, segments: 10 })
    const translate = guideDe.cells[100]!
    // A finished task shows when it finished, not a deadline that has passed.
    expect(translate).toMatchObject({ state: 'completed', deadline: null, completedAt: '2026-09-02T00:00:00Z' })
  })

  it('carries the job\'s error message on the failed cell only', () => {
    const apiDe = build().rows.find(r => r.key === 'job:3')!
    expect(apiDe.cells[100]).toMatchObject({ state: 'error', errors: ['MT engine rejected the API key.'] })
    expect(apiDe.cells[200]).toMatchObject({ state: 'not_started', errors: [] })
  })

  it('leaves a cell empty when the job has no task for that step', () => {
    const m = buildTaskMatrix(jobs, tasks.filter(t => t.id !== 22), {}, NOW)
    expect(m.rows.find(r => r.key === 'job:2')!.cells[200]).toBeUndefined()
  })
})

describe('buildTaskMatrix — filters', () => {
  const kept = (m: ReturnType<typeof build>) =>
    m.rows.flatMap(r => Object.values(r.cells).flatMap(c => c!.tasks.map(t => t.task.id))).sort()

  it('file keeps one file, even among files that share a name', () => {
    expect(build({ file: 20 }).rows.map(r => r.key)).toEqual(['job:3', 'job:4'])
    expect(parseTaskMatrixFilters(new URLSearchParams({ file: '20' }))).toEqual({ file: 20 })
    expect(parseTaskMatrixFilters(new URLSearchParams({ file: 'x' }))).toEqual({})
  })

  it('target keeps one language', () => {
    expect(build({ target: 'fr' }).rows.map(r => r.key)).toEqual(['job:4', 'job:2'])
  })

  it('status keeps matching tasks, dims the rest, hides rows with none', () => {
    const m = build({ state: 'error' })
    expect(m.rows.map(r => r.key)).toEqual(['job:3'])
    expect(m.rows[0].cells[100]!.dimmed).toBe(false)
    expect(m.rows[0].cells[200]!.dimmed).toBe(true)
    expect(kept(m)).toEqual([31])
  })

  it('assignee keeps one person\'s tasks, or the unassigned ones', () => {
    expect(kept(build({ assignee: 8 }))).toEqual([12, 42])
    expect(kept(build({ assignee: 'none' }))).toEqual([22, 31, 32])
  })

  it('overdue skips finished work even when its deadline has passed', () => {
    // 11 is done with a past deadline; 21 is open and past due.
    expect(kept(build({ due: 'overdue' }))).toEqual([21])
    expect(kept(build({ due: 'week' }))).toEqual([12])
  })

  it('search on a file name keeps the whole job; on a person, only their tasks', () => {
    expect(kept(build({ q: 'GUIDE' }))).toEqual([11, 12, 21, 22])
    expect(kept(build({ q: 'bob' }))).toEqual([12, 42])
  })

  it('search keeps its spaces: a trailing space is part of what is searched for', () => {
    const f = parseTaskMatrixFilters(new URLSearchParams({ q: 'guide ' }))
    expect(f.q).toBe('guide ')
    // "guide.docx" holds "guide" but not "guide ".
    expect(kept(build(f))).toEqual([])
  })
})

describe('languageRuns', () => {
  it('marks where each language starts and ends, with every task of its rows', () => {
    const m = build() // de: api.md (job 3), guide.docx (job 1); fr: api.md (job 4), guide.docx (job 2)
    expect(m.rows.map(r => r.key)).toEqual(['job:3', 'job:1', 'job:4', 'job:2'])
    const runs = languageRuns(m.rows)
    expect(runs.map(r => [r.start, r.end])).toEqual([[true, false], [false, true], [true, false], [false, true]])
    expect(runs[0].taskIds.sort()).toEqual(runs[1].taskIds.sort())
    expect(runs[0].taskIds.sort()).toEqual([11, 12, 31, 32])
  })
})

describe('languageLabelRows', () => {
  const run = (n: number) => Array.from({ length: n }, (_, k) => ({ start: k === 0, end: k === n - 1, taskIds: [] }))

  it('puts the code at the vertical middle of its run', () => {
    // Three rows of 52: the middle is 78 down, 26 into the second row.
    expect([...languageLabelRows(run(3), [52, 52, 52])]).toEqual([[1, 26]])
    // Two rows: the middle is the line between them, the top of the second.
    expect([...languageLabelRows(run(2), [52, 52])]).toEqual([[1, 0]])
    // One row: its own middle.
    expect([...languageLabelRows(run(1), [72])]).toEqual([[0, 36]])
  })

  it('places each run on its own, with rows of different heights', () => {
    // A run of 72 + 52 (middle 62, inside the first row), then a run of one.
    expect([...languageLabelRows([...run(2), ...run(1)], [72, 52, 52])]).toEqual([[0, 62], [2, 26]])
  })
})

describe('URL parsing', () => {
  it('drops values it does not know', () => {
    const p = new URLSearchParams({ task_status: 'bogus', assignee: 'x1', due: 'later', q: '' })
    expect(parseTaskMatrixFilters(p)).toEqual({})
  })

  it('reads every filter', () => {
    const p = new URLSearchParams({ task_status: 'error', assignee: '8', due: 'overdue', q: ' api ', target: 'de' })
    expect(parseTaskMatrixFilters(p)).toEqual({ state: 'error', assignee: 8, due: 'overdue', q: ' api ', target: 'de' })
    expect(parseTaskMatrixFilters(new URLSearchParams({ assignee: 'none' }))).toEqual({ assignee: 'none' })
  })
})

describe('the current-task view', () => {
  it('puts each job at its current step: in progress, else the furthest completed', () => {
    const steps = Object.fromEntries(build().rows.map(r => [r.key, r.currentStepId]))
    // guide/de review in progress; guide/fr and api/de translating; api/fr all done.
    expect(steps).toEqual({ 'job:1': 200, 'job:2': 100, 'job:3': 100, 'job:4': 200 })
  })

  it('puts a job with nothing started at its first step', () => {
    const m = buildTaskMatrix([jobs[0]], [
      makeProjectTask({ id: 1, job_id: 1, ...REVIEW, status: 'pending' }),
      makeProjectTask({ id: 2, job_id: 1, ...TRANSLATE, status: 'pending' }),
    ], {}, NOW)
    expect(m.rows[0].currentStepId).toBe(100)
  })

  it('keeps one cell per row, and drops a row whose current task the filters leave out', () => {
    const page = currentTaskMatrix(build({ assignee: 'none' }))
    // Unassigned: 22 (guide/fr review, but fr is translating), 31 and 32 (api/de, translating).
    expect(page.rows.map(r => r.key)).toEqual(['job:3'])
    expect(Object.keys(page.rows[0].cells)).toEqual(['100'])
  })

  it('keeps a job with no workflow tasks, in a stepless section after the steps', () => {
    const m = buildTaskMatrix([...jobs, makeJob({ id: 5, file_id: 50, file_name: 'loose.md', target_language: 'it' })], tasks, {}, NOW)
    const page = currentTaskMatrix(m)
    expect(page.rows.find(r => r.key === 'job:5')).toMatchObject({ currentStepId: null, cells: {} })
    const sections = stepSections(page)
    expect(sections.map(s => s.column?.stepId ?? null)).toEqual([100, 200, null])
    expect(sections[2].matrix).toMatchObject({ columns: [], rows: [expect.objectContaining({ key: 'job:5' })] })
    // Without such a job there is no stepless section.
    expect(stepSections(currentTaskMatrix(build())).map(s => s.column?.stepId ?? null)).toEqual([100, 200])
  })

  it('splits the rows into one single-column matrix per step', () => {
    const sections = stepSections(currentTaskMatrix(build()))
    expect(sections.map(s => [s.column?.name, s.matrix.columns.map(c => c.stepId), s.matrix.rows.map(r => r.key)])).toEqual([
      ['translate', [100], ['job:3', 'job:2']],
      ['review', [200], ['job:1', 'job:4']],
    ])
  })
})

describe('step tones — the colour of a step\'s header', () => {
  it('turns every step with a task in progress yellow', () => {
    // Translation has tasks in progress (and an error); so has Review.
    expect(Object.fromEntries(stepTones(build()))).toEqual({ 100: 'yellow', 200: 'yellow' })
  })

  it('turns a step green once every task at it is completed, though no job is there now', () => {
    // Every translation done, and guide/fr's review started.
    const translated = tasks.map(t =>
      t.workflow_step_id === TRANSLATE.workflow_step_id ? { ...t, status: 'completed' as const }
        : t.id === 22 ? { ...t, status: 'in_progress' as const } : t)
    const m = buildTaskMatrix(jobs.filter(j => j.pipeline_status !== 'failed'), translated, {}, NOW)
    expect(stepSections(currentTaskMatrix(m))[0].matrix.rows).toEqual([]) // every job is past Translation
    expect(Object.fromEntries(stepTones(m))).toEqual({ 100: 'green', 200: 'yellow' })
  })

  it('an error keeps a step from being done, and a step with nothing in progress stays plain', () => {
    const m = buildTaskMatrix(jobs, tasks.map(t => (t.status === 'in_progress' ? { ...t, status: 'pending' as const } : t)), {}, NOW)
    // Translation: done, done, the failed job's error, done. Review: nothing started but api/fr.
    expect(stepTones(m).size).toBe(0)
  })

  it('reads only the rows the filters keep', () => {
    // api/fr alone: both its steps are completed.
    expect(Object.fromEntries(stepTones(build({ file: 20, target: 'fr' })))).toEqual({ 100: 'green', 200: 'green' })
    expect(stepTones(build({ target: 'xx' })).size).toBe(0)
  })
})

describe('selection', () => {
  it('maps modifier keys to a mode; Cmd and Ctrl both toggle', () => {
    expect(selectModeFor({ metaKey: false, ctrlKey: false })).toBe('replace')
    expect(selectModeFor({ metaKey: true, ctrlKey: false })).toBe('toggle')
    expect(selectModeFor({ metaKey: false, ctrlKey: true })).toBe('toggle')
  })

  it('plain click replaces, and clicking the whole selection again clears it', () => {
    const one = applySelection(new Set([1, 2]), [3], 'replace')
    expect([...one]).toEqual([3])
    expect([...applySelection(one, [3], 'replace')]).toEqual([])
    // Part of a larger selection: replace, don't clear.
    expect([...applySelection(new Set([3, 4]), [3], 'replace')]).toEqual([3])
  })

  it('Ctrl/Cmd adds a cell, or removes it once fully selected', () => {
    const added = applySelection(new Set([1]), [2, 3], 'toggle')
    expect([...added].sort()).toEqual([1, 2, 3])
    expect([...applySelection(added, [2, 3], 'toggle')]).toEqual([1])
    // Partly selected group: Ctrl/Cmd completes it rather than removing.
    expect([...applySelection(new Set([2]), [2, 3], 'toggle')].sort()).toEqual([2, 3])
  })

  it('a cell with nothing in play changes nothing', () => {
    expect([...applySelection(new Set([5]), [], 'replace')]).toEqual([5])
  })

  it('row and column headers select their tasks', () => {
    const m = build()
    expect(rowTaskIds(m.rows.find(r => r.key === 'job:2')!).sort()).toEqual([21, 22])
    expect(columnTaskIds(m, 200).sort()).toEqual([12, 22, 32, 42])
  })

  it('a cell reads as selected when its task is', () => {
    const guideDe = build().rows.find(r => r.key === 'job:1')!
    expect(cellSelection(guideDe.cells[100]!, new Set([11, 31]))).toBe('all')
    expect(cellSelection(guideDe.cells[200]!, new Set([11, 31]))).toBe('none')
  })

  it('acts only on selected tasks still on screen', () => {
    const selected = new Set([11, 21, 999])
    expect(selectedTasks(build(), selected).map(t => t.task.id).sort()).toEqual([11, 21])
    // Filter fr away: task 21 is hidden, so it is no longer acted on.
    expect(selectedTasks(build({ target: 'de' }), selected).map(t => t.task.id)).toEqual([11])
  })
})

describe('notes — the third line a cell adds beyond its colour', () => {
  it('a cell has one only for an error or a send-back', () => {
    const m = build()
    const row = (k: string) => m.rows.find(r => r.key === k)!
    expect(cellHasNote(row('job:1').cells[200]!)).toBe(false) // plain in-progress review
    expect(cellHasNote(row('job:3').cells[100]!)).toBe(true) // the failed step
    const sentBack = buildTaskMatrix([jobs[0]], [makeProjectTask({ id: 1, job_id: 1, status: 'rejected', rejection_reason: 'Wrong terms.' })], {}, NOW)
    expect(cellHasNote(sentBack.rows[0].cells[100]!)).toBe(true)
  })

  it('only a row with a note gets the third line — one failed job does not stretch the rest', () => {
    const rows = build().rows
    expect(rowHasNotes(rows.find(r => r.key === 'job:3')!)).toBe(true) // failed
    expect(rowHasNotes(rows.find(r => r.key === 'job:1')!)).toBe(false)
  })
})
