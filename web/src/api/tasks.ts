import { request } from './request'

// A row on the Tasks page: a job assigned on the job's active workflow step.
// Shape matches internal/job/handler_tasks.go taskResponse.
export interface Task {
  job_id: number
  project_id: number
  project_name: string
  file_id: number
  file_name: string
  source_language: string
  target_language: string
  // Raw jobs.status; with current_step it detects a running QE+APE step
  // (the editor locks while it runs, see isAIStepRunning in lib/project-logic).
  status: string
  current_step: string
  step_name: string
  customer_name: string | null
  word_count: number
  translated_count: number | null
  total_count: number | null
  deadline: string | null
  // Owner/admin only (they see every assignee's tasks); absent for everyone else.
  assignee_name?: string
}

export const TASK_DEADLINE_FILTERS = ['overdue', '24h', 'week', 'none'] as const
export type TaskDeadlineFilter = (typeof TASK_DEADLINE_FILTERS)[number]

export interface TaskListFilters {
  q?: string
  step?: string
  deadline?: TaskDeadlineFilter
}

// One page of the Tasks list. `cursor` is opaque: pass it back for the next
// page; it is '' when has_more is false.
export interface TasksPage {
  data: Task[]
  cursor: string
  has_more: boolean
}

/**
 * One page of tasks. Owner/admin get every assignee's tasks, everyone else only
 * their own (scoped server-side, tenant-scoped too). `cursor === ''` is the
 * first page.
 */
export const listTasksPage = (filters: TaskListFilters, cursor = '', limit = 50) => {
  const qs = new URLSearchParams()
  if (cursor) qs.set('cursor', cursor)
  qs.set('limit', String(limit))
  if (filters.q) qs.set('q', filters.q)
  if (filters.step) qs.set('step', filters.step)
  if (filters.deadline) qs.set('deadline', filters.deadline)
  return request<TasksPage>(`/tasks?${qs.toString()}`)
}

/** Step names across the caller's whole task scope — the Step filter's options. */
export const listTaskSteps = () =>
  request<{ data: string[] }>('/tasks/steps').then(r => r.data)

