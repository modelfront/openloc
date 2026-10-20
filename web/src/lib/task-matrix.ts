// The model behind the project task matrix (project page → Tasks tab).
//
// Workflow steps run across, jobs run down, and each cell is a TASK: one job's
// assignment for one step.
//
// Filters are URL-backed like the Files tab's filters
// (file-readiness.ts). `q` shares its key with that tab on purpose, so a file
// search typed there still applies here; the rest are keys of this view
// alone.
//
// Selection is a set of task ids, not cell keys: a cell reads as selected when
// every task it stands for is.

import type { Job, ProjectTask } from '../api'
import type { ChipColor } from '@/components/ui/chip'
import { currentStep, sentBack } from './current-task'
import { computeDeadlinePill } from './deadline'
import { isAutomatedStep } from './workflow-steps'

// --- Task state ------------------------------------------------------------

export const TASK_STATES = ['not_started', 'in_progress', 'completed', 'error'] as const
export type TaskState = (typeof TASK_STATES)[number]

export const TASK_STATE_DISPLAY = {
  not_started: { label: 'Not started', color: 'grey' },
  in_progress: { label: 'In progress', color: 'yellow' },
  completed: { label: 'Completed', color: 'green' },
  error: { label: 'Error', color: 'red' },
} as const satisfies Record<TaskState, { label: string; color: ChipColor }>

/**
 * A failed pipeline belongs to the job's current step, so only that task turns
 * red. A step sent back by a later reviewer (`rejected`) waits for the earlier
 * step again, which is "not started" from this step's point of view.
 */
export function taskState(task: ProjectTask, job: Job): TaskState {
  if (job.pipeline_status === 'failed' && job.workflow_step?.id === task.id) return 'error'
  if (task.status === 'completed') return 'completed'
  if (task.status === 'in_progress') return 'in_progress'
  return 'not_started'
}

// --- Filters (URL) ----------------------------------------------------------

export const DUE_FILTERS = ['overdue', 'week', 'none'] as const
export type DueFilter = (typeof DUE_FILTERS)[number]

export const DUE_FILTER_LABELS: Record<DueFilter, string> = {
  overdue: 'Overdue',
  week: 'Due in 7 days',
  none: 'No deadline',
}

/** `assignee`: a user id, or 'none' for unassigned tasks. */
export interface TaskMatrixFilters {
  q?: string
  /** One file, by id — where a click on the Files tab lands. */
  file?: number
  target?: string
  state?: TaskState
  assignee?: number | 'none'
  due?: DueFilter
}

/** The filter chips, in toolbar order, with the URL key each one sets. */
export const TASK_FILTERS = [
  { key: 'state', param: 'task_status', label: 'Status' },
  { key: 'file', param: 'file', label: 'File' },
  { key: 'target', param: 'target', label: 'Language' },
  { key: 'assignee', param: 'assignee', label: 'Assignee' },
  { key: 'due', param: 'due', label: 'Deadline' },
] as const satisfies readonly { key: keyof Omit<TaskMatrixFilters, 'q'>; param: string; label: string }[]
export type TaskFilterKey = (typeof TASK_FILTERS)[number]['key']

export const ALL_TASK_FILTER_KEYS: readonly TaskFilterKey[] = TASK_FILTERS.map(f => f.key)

export function parseTaskMatrixFilters(params: URLSearchParams): TaskMatrixFilters {
  const f: TaskMatrixFilters = {}
  // Not trimmed: a space is part of what is searched for ("v3 " ≠ "v3").
  const q = params.get('q')
  if (q) f.q = q
  const file = params.get('file')
  if (file && /^\d+$/.test(file)) f.file = Number(file)
  const target = params.get('target')
  if (target) f.target = target
  const state = params.get('task_status')
  if (state && (TASK_STATES as readonly string[]).includes(state)) f.state = state as TaskState
  const assignee = params.get('assignee')
  if (assignee === 'none') f.assignee = 'none'
  else if (assignee && /^\d+$/.test(assignee)) f.assignee = Number(assignee)
  const due = params.get('due')
  if (due && (DUE_FILTERS as readonly string[]).includes(due)) f.due = due as DueFilter
  return f
}

const includesCI = (haystack: string | null | undefined, needle: string) =>
  !!haystack && haystack.toLowerCase().includes(needle.toLowerCase())

function dueMatches(t: MatrixTask, due: DueFilter, now: Date): boolean {
  const band = computeDeadlinePill(t.task.deadline, now).band
  if (due === 'none') return band === 'none'
  // A finished task is never overdue or due soon.
  if (t.state === 'completed') return false
  if (due === 'overdue') return band === 'overdue'
  return band === 'approaching' || band === 'soon'
}

/**
 * Whether one task stays in play. `jobMatchesQuery` says the search already
 * matched the job (file name or language); otherwise the search looks at the
 * task's assignee, so searching a person lights up only their tasks.
 */
function taskMatches(t: MatrixTask, f: TaskMatrixFilters, jobMatchesQuery: boolean, now: Date): boolean {
  if (f.q && !jobMatchesQuery && !includesCI(t.task.assignee_name, f.q)) return false
  if (f.state && t.state !== f.state) return false
  if (f.assignee === 'none' && t.task.assigned_to != null) return false
  if (typeof f.assignee === 'number' && t.task.assigned_to !== f.assignee) return false
  if (f.due && !dueMatches(t, f.due, now)) return false
  return true
}

// --- The matrix -------------------------------------------------------------

export interface MatrixTask {
  task: ProjectTask
  job: Job
  state: TaskState
}

/** A step's name as a heading: workflow step names are stored lower-case. */
export const stepLabel = (name: string) => name.charAt(0).toUpperCase() + name.slice(1)

/** One workflow step = one column. */
export interface MatrixColumn {
  stepId: number
  name: string
  order: number
  type: ProjectTask['step_type']
}

export interface MatrixCell {
  key: string
  rowKey: string
  stepId: number
  /** The tasks the filters keep — what a click on the cell selects. */
  tasks: MatrixTask[]
  /** The cell has tasks, but the filters keep none of them. */
  dimmed: boolean
  state: TaskState
  /** Everyone assigned, with how many of the cell's tasks each holds; by name. */
  assignees: { name: string; count: number }[]
  unassigned: number
  /** Earliest deadline among unfinished tasks. */
  deadline: string | null
  /** Latest completion time, when every task is completed. */
  completedAt: string | null
  words: number
  segments: number
  done: number
  total: number
  errors: string[]
  sentBack: number
  rejectionReasons: string[]
  /** The comments the cell's tasks were handed by the step before them. */
  handoffNotes: string[]
}

/** One job: a file in one target language. */
export interface MatrixRow {
  key: string
  targetLanguage: string
  fileName: string
  job: Job
  cells: Record<number, MatrixCell | undefined>
  /**
   * The step of the job's current task (lib/current-task): in progress, else
   * the completed one furthest along. Before anything starts, the first step,
   * whose task is the one waiting. Null only for a job with no tasks.
   */
  currentStepId: number | null
  /** The step that sent the job back, whatever step the job is on now. */
  sentBack: { stepName: string; reason: string } | null
}

export interface TaskMatrix {
  columns: MatrixColumn[]
  rows: MatrixRow[]
}

function summarize(key: string, rowKey: string, stepId: number, all: MatrixTask[], kept: MatrixTask[]): MatrixCell {
  const shown = kept.length > 0 ? kept : all
  const states = shown.map(t => t.state)
  const state: TaskState =
    states.includes('error') ? 'error'
      : states.every(s => s === 'completed') ? 'completed'
        : states.every(s => s === 'not_started') ? 'not_started'
          : 'in_progress'

  const byPerson = new Map<number, { name: string; count: number }>()
  for (const t of shown) {
    if (t.task.assigned_to == null) continue
    const person = byPerson.get(t.task.assigned_to) ?? { name: t.task.assignee_name ?? 'Unknown', count: 0 }
    person.count++
    byPerson.set(t.task.assigned_to, person)
  }
  const assignees = [...byPerson.values()].sort((a, b) => a.name.localeCompare(b.name))
  const open = shown.filter(t => t.state !== 'completed')
  const openDeadlines = open.flatMap(t => (t.task.deadline ? [t.task.deadline] : [])).sort(byTime)
  const completedTimes = shown.flatMap(t => (t.task.completed_at ? [t.task.completed_at] : [])).sort(byTime)
  const jobs = new Map(shown.map(t => [t.job.id, t.job]))

  return {
    key,
    rowKey,
    stepId,
    tasks: kept,
    dimmed: kept.length === 0,
    state,
    assignees,
    unassigned: shown.filter(t => t.task.assigned_to == null && !isAutomatedStep(t.task.step_type)).length,
    deadline: openDeadlines[0] ?? null,
    completedAt: state === 'completed' ? (completedTimes[completedTimes.length - 1] ?? null) : null,
    words: [...jobs.values()].reduce((n, j) => n + j.word_count, 0),
    segments: [...jobs.values()].reduce((n, j) => n + j.segment_count, 0),
    done: states.filter(s => s === 'completed').length,
    total: shown.length,
    errors: [...new Set(shown.flatMap(t => (t.state === 'error' ? [t.job.error_message || 'Pipeline failed'] : [])))],
    sentBack: shown.filter(t => t.task.status === 'rejected').length,
    rejectionReasons: [...new Set(shown.flatMap(t => (t.task.status === 'rejected' && t.task.rejection_reason ? [t.task.rejection_reason] : [])))],
    handoffNotes: [...new Set(shown.flatMap(t => (t.task.handoff_note ? [t.task.handoff_note] : [])))],
  }
}

const byTime = (a: string, b: string) => new Date(a).getTime() - new Date(b).getTime()

function compareRows(a: MatrixRow, b: MatrixRow): number {
  return a.targetLanguage.localeCompare(b.targetLanguage)
    || a.fileName.localeCompare(b.fileName)
    || a.key.localeCompare(b.key, undefined, { numeric: true })
}

export function buildTaskMatrix(
  jobs: Job[],
  tasks: ProjectTask[],
  filters: TaskMatrixFilters,
  now: Date = new Date(),
): TaskMatrix {
  const jobById = new Map(jobs.map(j => [j.id, j]))

  const columnById = new Map<number, MatrixColumn>()
  const tasksByJob = new Map<number, MatrixTask[]>()
  for (const task of tasks) {
    const job = jobById.get(task.job_id)
    if (!job) continue // the jobs list is a poll behind — the job is new or just deleted
    if (!columnById.has(task.workflow_step_id)) {
      columnById.set(task.workflow_step_id, {
        stepId: task.workflow_step_id, name: task.step_name, order: task.step_order, type: task.step_type,
      })
    }
    const list = tasksByJob.get(job.id) ?? []
    list.push({ task, job, state: taskState(task, job) })
    tasksByJob.set(job.id, list)
  }
  const columns = [...columnById.values()].sort((a, b) => a.order - b.order || a.stepId - b.stepId)

  const taskFilter = !!(filters.state || filters.assignee != null || filters.due)
  const rows: MatrixRow[] = []
  for (const job of jobs) {
    if (filters.file != null && job.file_id !== filters.file) continue
    if (filters.target && job.target_language !== filters.target) continue
    const jobMatchesQuery = !filters.q || includesCI(job.file_name, filters.q) || includesCI(job.target_language, filters.q)
    const jobTasks = tasksByJob.get(job.id) ?? []
    const kept = jobTasks.filter(t => taskMatches(t, filters, jobMatchesQuery, now))
    // A job shows while any of its tasks stays in play. One with no tasks yet
    // has only its name to match on.
    const visible = jobTasks.length > 0 ? kept.length > 0 : jobMatchesQuery && !taskFilter
    if (!visible) continue
    const key = `job:${job.id}`
    const cells: MatrixRow['cells'] = {}
    for (const col of columns) {
      const all = jobTasks.filter(t => t.task.workflow_step_id === col.stepId)
      if (all.length === 0) continue
      cells[col.stepId] = summarize(`${key}|${col.stepId}`, key, col.stepId, all, kept.filter(t => all.includes(t)))
    }
    const jobSteps = jobTasks.map(t => t.task)
    const current = currentStep(jobSteps) ?? [...jobSteps].sort((a, b) => a.step_order - b.step_order)[0]
    rows.push({
      key, targetLanguage: job.target_language, fileName: job.file_name, job, cells,
      currentStepId: current?.workflow_step_id ?? null,
      sentBack: sentBack(jobSteps),
    })
  }
  rows.sort(compareRows)
  return { columns, rows }
}

// --- The current-task view ---------------------------------------------------

/**
 * Each row cut down to its current task, for the page's step view. A row whose
 * current task the filters leave out drops away: the view shows one task per
 * job, so a dimmed one would be a row with nothing in it. A job with no tasks
 * (no workflow) stays, with no cells — it still needs its Open.
 */
export function currentTaskMatrix(m: TaskMatrix): TaskMatrix {
  const rows = m.rows.flatMap(r => {
    if (r.currentStepId == null) return [{ ...r, cells: {} }]
    const cell = r.cells[r.currentStepId]
    return cell && !cell.dimmed ? [{ ...r, cells: { [cell.stepId]: cell } }] : []
  })
  return { columns: m.columns, rows }
}

export interface StepSection {
  /** Null for the jobs that have no workflow tasks. */
  column: MatrixColumn | null
  matrix: TaskMatrix
}

/**
 * One single-column matrix per workflow step, holding the rows whose current
 * task is there; then, when there are any, the jobs with no workflow tasks.
 */
export function stepSections(m: TaskMatrix): StepSection[] {
  const sections: StepSection[] = m.columns.map(column => ({
    column,
    matrix: { columns: [column], rows: m.rows.filter(r => r.currentStepId === column.stepId) },
  }))
  const stepless = m.rows.filter(r => r.currentStepId == null)
  if (stepless.length > 0) sections.push({ column: null, matrix: { columns: [], rows: stepless } })
  return sections
}

/**
 * A colour for each step's header: green when every task at the step is
 * completed, yellow when one is in progress. Other steps have none. Reads every task in the rows the filters
 * keep, not only the current ones: a step every job is past holds no current
 * task, and is the one that is done.
 */
export function stepTones(m: TaskMatrix): Map<number, ChipColor> {
  const tones = new Map<number, ChipColor>()
  for (const col of m.columns) {
    const states = m.rows.flatMap(r => r.cells[col.stepId]?.state ?? [])
    if (states.length > 0 && states.every(s => s === 'completed')) {
      tones.set(col.stepId, 'green')
    } else if (states.includes('in_progress')) {
      tones.set(col.stepId, 'yellow')
    }
  }
  return tones
}

/**
 * The background already says a cell's state, so a cell adds a third line
 * only for what the colour cannot: an error, a step sent back, or a comment
 * the task was handed.
 */
export const cellHasNote = (c: MatrixCell): boolean =>
  c.errors.length > 0 || c.sentBack > 0 || c.handoffNotes.length > 0

/** Whether a row needs the third line: some cell of it has a note. */
export const rowHasNotes = (r: MatrixRow): boolean =>
  Object.values(r.cells).some(c => c != null && cellHasNote(c))

// --- Selection ---------------------------------------------------------------

/** How a click changes the selection: plain, or with Ctrl/Cmd held. */
export type SelectMode = 'replace' | 'toggle'

export function selectModeFor(e: { metaKey: boolean; ctrlKey: boolean }): SelectMode {
  return e.metaKey || e.ctrlKey ? 'toggle' : 'replace'
}

/**
 * Plain click selects only these tasks — or clears the selection when they
 * already are the whole of it. Ctrl/Cmd adds them, or removes them when all
 * are already selected.
 */
export function applySelection(prev: ReadonlySet<number>, ids: number[], mode: SelectMode): Set<number> {
  if (ids.length === 0) return new Set(prev)
  const allIn = ids.every(id => prev.has(id))
  if (mode === 'replace') return allIn && prev.size === ids.length ? new Set() : new Set(ids)
  const next = new Set(prev)
  for (const id of ids) {
    if (allIn) next.delete(id)
    else next.add(id)
  }
  return next
}

export const cellTaskIds = (cell: MatrixCell | undefined): number[] =>
  cell ? cell.tasks.map(t => t.task.id) : []

export const rowTaskIds = (row: MatrixRow): number[] =>
  Object.values(row.cells).flatMap(cellTaskIds)

/** The matrix shows a language once over the run of its rows (rows are sorted
 *  by language). Per row: whether it starts or ends its run, and every
 *  task of the run — what a click on the language selects. */
export interface LanguageRun { start: boolean; end: boolean; taskIds: number[] }

export function languageRuns(rows: MatrixRow[]): LanguageRun[] {
  const out: LanguageRun[] = []
  let i = 0
  while (i < rows.length) {
    let j = i
    while (j + 1 < rows.length && rows[j + 1].targetLanguage === rows[i].targetLanguage) j++
    const taskIds = rows.slice(i, j + 1).flatMap(rowTaskIds)
    for (let k = i; k <= j; k++) out.push({ start: k === i, end: k === j, taskIds })
    i = j + 1
  }
  return out
}

/**
 * Where each run shows its language: the row holding the run's vertical
 * middle, and how far down that row the middle falls (px). Row heights are
 * known up front, so this is arithmetic, not layout.
 */
export function languageLabelRows(runs: LanguageRun[], heights: number[]): Map<number, number> {
  const out = new Map<number, number>()
  let i = 0
  while (i < runs.length) {
    let j = i
    while (!runs[j].end && j + 1 < runs.length) j++
    const mid = heights.slice(i, j + 1).reduce((n, h) => n + h, 0) / 2
    let top = 0
    let k = i
    while (k < j && top + heights[k] <= mid) top += heights[k++]
    out.set(k, mid - top)
    i = j + 1
  }
  return out
}

export const columnTaskIds = (m: TaskMatrix, stepId: number): number[] =>
  m.rows.flatMap(r => cellTaskIds(r.cells[stepId]))

export type CellSelection = 'all' | 'some' | 'none'

export function cellSelection(cell: MatrixCell, selected: ReadonlySet<number>): CellSelection {
  if (cell.tasks.length === 0) return 'none'
  const n = cell.tasks.filter(t => selected.has(t.task.id)).length
  return n === 0 ? 'none' : n === cell.tasks.length ? 'all' : 'some'
}

/**
 * The selected tasks that are still on screen. A task hidden by a filter, or
 * gone after a refetch, drops out — an action never reaches work the user
 * cannot see.
 */
export function selectedTasks(m: TaskMatrix, selected: ReadonlySet<number>): MatrixTask[] {
  return m.rows.flatMap(r => Object.values(r.cells).flatMap(c => (c ? c.tasks.filter(t => selected.has(t.task.id)) : [])))
}
