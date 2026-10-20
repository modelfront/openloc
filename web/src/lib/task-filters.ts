// Shared parser for the URL-backed Tasks page filters, used by both TasksPage
// (the list query) and TaskFilterBar (the chip values), so the two can't read
// the URL differently. Keys are added in a fixed order (step → deadline → q) so
// JSON.stringify of the result is a stable query key.

import { TASK_DEADLINE_FILTERS, type TaskDeadlineFilter, type TaskListFilters } from '../api'

export const TASK_DEADLINE_LABELS: Record<TaskDeadlineFilter, string> = {
  overdue: 'Overdue',
  '24h': 'Due within 24h',
  week: 'Due within 7 days',
  none: 'No deadline',
}

export function parseTaskFilters(params: URLSearchParams): TaskListFilters {
  const f: TaskListFilters = {}
  const step = params.get('step')
  if (step) f.step = step
  const deadline = params.get('deadline')
  if (deadline && (TASK_DEADLINE_FILTERS as readonly string[]).includes(deadline)) {
    f.deadline = deadline as TaskDeadlineFilter
  }
  const q = params.get('q')
  if (q) f.q = q
  return f
}
