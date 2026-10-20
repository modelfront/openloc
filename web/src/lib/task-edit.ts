// Client side of the task-edit guard (internal/taskedit). Every endpoint that
// writes translation text checks the job's current task first. It refuses
// with a stable `error.code`, or — when a manager edits a completed task —
// reopens the task and answers `task_reopened: true` beside the payload.
// Either way the task the editor shows has changed, so both paths reload it.

import type { QueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { ApiError } from '../api/request'
import { queryKeys } from './query-keys'
import { invalidateJobContext, invalidateProjectStatus } from './query-invalidation'

/** Refusal codes, one per locked task mode. Statuses: 409, or 403 for task_completed. */
export const TASK_LOCK_CODES = ['task_errored', 'ai_step_running', 'pretranslation_running', 'task_completed', 'task_not_started'] as const
export type TaskLockCode = (typeof TASK_LOCK_CODES)[number]

// Fixed toast ids: an autosave and the flush behind it can both hit the same
// refusal, and a run of edits must announce one reopen, not several.
const REOPENED_TOAST_ID = 'task-reopened'
const LOCKED_TOAST_ID = 'task-locked'

/** The refusal code of a task-lock error, or null for any other error. */
export function taskLockCode(err: unknown): TaskLockCode | null {
  if (!(err instanceof ApiError)) return null
  const code = (err.body as { error?: { code?: unknown } } | undefined)?.error?.code
  return TASK_LOCK_CODES.find(c => c === code) ?? null
}

export function isTaskLockedError(err: unknown): err is ApiError {
  return taskLockCode(err) !== null
}

// The job's caches (detail, steps, translations, the project's job list and
// task matrix under its prefix) plus the user's own task list.
function invalidateTask(queryClient: QueryClient, projectId: number, jobId: number) {
  invalidateJobContext(queryClient, projectId, jobId)
  queryClient.invalidateQueries({ queryKey: queryKeys.tasks.all })
}

/**
 * A write put the job's completed task back in progress. A reopened job also
 * reopens a completed project, and the reopen is a history event.
 */
export function handleTaskReopened(queryClient: QueryClient, projectId: number, jobId: number) {
  invalidateTask(queryClient, projectId, jobId)
  invalidateProjectStatus(queryClient, projectId)
  toast.success('Task reopened', { id: REOPENED_TOAST_ID })
}

/**
 * The server refused a write because of the task's state. Its message says
 * why; reloading the task lets the editor switch to the matching read-only
 * mode.
 */
export function handleTaskLocked(queryClient: QueryClient, projectId: number, jobId: number, err: ApiError) {
  invalidateTask(queryClient, projectId, jobId)
  toast.error(err.message, { id: LOCKED_TOAST_ID })
}

/**
 * Error options for a `useMutationWithToast` that writes to a task: a task-lock
 * refusal reloads the task and shows its own toast, and every other error gets
 * the hook's generic one.
 */
export function taskLockFailure(queryClient: QueryClient, projectId: number, jobId: number) {
  return {
    errorToast: (err: Error) => !isTaskLockedError(err),
    onError: (err: Error) => {
      if (isTaskLockedError(err)) handleTaskLocked(queryClient, projectId, jobId, err)
    },
  }
}
