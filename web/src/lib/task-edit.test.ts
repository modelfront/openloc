// Client side of the task-edit guard: recognising a refusal, and reloading the
// task after a refusal or a reopen. The editor's mode is computed from the job
// detail + job steps caches, and the project's task matrix from the jobs list,
// so those are the keys that must refetch — asserted against the real cache,
// not a spy, so a key that merely gets "touched" doesn't count.
//
// Anti-tautology RED proofs:
//   - drop the code check from taskLockCode (any ApiError) → the
//     "version conflict" and "unknown code" cases fail.
//   - drop invalidateProjectStatus from handleTaskReopened → the projects
//     assertion fails.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { QueryClient } from '@tanstack/react-query'

const toastSuccess = vi.fn()
const toastError = vi.fn()
vi.mock('sonner', () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
  },
}))

import { ApiError } from '../api/request'
import { queryKeys } from './query-keys'
import { handleTaskLocked, handleTaskReopened, isTaskLockedError, taskLockCode, taskLockFailure } from './task-edit'

const lockError = (status: number, code: string, message = 'This task has an error.') =>
  new ApiError(status, message, { error: { code, message } })

beforeEach(() => {
  toastSuccess.mockReset()
  toastError.mockReset()
})

describe('taskLockCode / isTaskLockedError', () => {
  it.each([
    ['task_errored', 409],
    ['ai_step_running', 409],
    ['task_not_started', 409],
    ['task_completed', 403],
  ])('recognises %s', (code, status) => {
    const err = lockError(status, code)
    expect(taskLockCode(err)).toBe(code)
    expect(isTaskLockedError(err)).toBe(true)
  })

  it.each<[string, unknown]>([
    ['a version conflict 409', new ApiError(409, 'updated by another user', { conflict: { id: 1 } })],
    ['a 409 with an unknown code', lockError(409, 'segment_locked')],
    ['a 403 without a code', new ApiError(403, 'forbidden', { error: { message: 'forbidden' } })],
    ['an ApiError with no body', new ApiError(409, 'conflict')],
    ['a plain Error', new Error('task_errored')],
    ['a non-error', { error: { code: 'task_errored' } }],
  ])('ignores %s', (_name, err) => {
    expect(taskLockCode(err)).toBeNull()
    expect(isTaskLockedError(err)).toBe(false)
  })
})

// Seed every cache the editor and the project page read, then report which
// ones the handler left stale (invalidated).
function seededClient() {
  const qc = new QueryClient()
  const keys = {
    jobDetail: queryKeys.job.detail(7),
    jobSteps: queryKeys.job.steps(7),
    projectTasks: queryKeys.jobs.tasks(42),
    projectJobs: queryKeys.jobs.byProject(42),
    myTasks: queryKeys.tasks.all,
    projects: queryKeys.projects.all,
    otherJobSteps: queryKeys.job.steps(8),
  }
  for (const key of Object.values(keys)) qc.setQueryData(key, { seeded: true })
  const stale = (key: readonly unknown[]) => qc.getQueryState(key)?.isInvalidated === true
  return { qc, keys, stale }
}

describe('handleTaskReopened', () => {
  it('reloads the task everywhere it shows, and the project status, then says so', () => {
    const { qc, keys, stale } = seededClient()
    handleTaskReopened(qc, 42, 7)

    expect(stale(keys.jobDetail)).toBe(true)
    expect(stale(keys.jobSteps)).toBe(true)
    expect(stale(keys.projectTasks)).toBe(true)
    expect(stale(keys.projectJobs)).toBe(true)
    expect(stale(keys.myTasks)).toBe(true)
    expect(stale(keys.projects)).toBe(true)
    // Scoped to this job.
    expect(stale(keys.otherJobSteps)).toBe(false)
    expect(toastSuccess).toHaveBeenCalledExactlyOnceWith('Task reopened')
  })
})

describe('handleTaskLocked', () => {
  it('toasts the server message and reloads the task so the editor locks', () => {
    const { qc, keys, stale } = seededClient()
    handleTaskLocked(qc, 42, 7, lockError(409, 'task_errored', 'This task has an error. Re-run its automation or complete the task before editing.'))

    expect(toastError).toHaveBeenCalledExactlyOnceWith(
      'This task has an error. Re-run its automation or complete the task before editing.',
    )
    expect(stale(keys.jobDetail)).toBe(true)
    expect(stale(keys.jobSteps)).toBe(true)
    expect(stale(keys.projectTasks)).toBe(true)
    expect(toastSuccess).not.toHaveBeenCalled()
  })
})

describe('taskLockFailure', () => {
  it('a task lock gets its own toast and reloads the task; the generic toast is off for it', () => {
    const { qc, keys, stale } = seededClient()
    const failure = taskLockFailure(qc, 42, 7)
    const err = lockError(409, 'task_errored')

    expect(failure.errorToast(err)).toBe(false)
    failure.onError(err)
    expect(toastError).toHaveBeenCalledExactlyOnceWith('This task has an error.')
    expect(stale(keys.jobDetail)).toBe(true)
  })

  it('any other error is left to the generic toast and reloads nothing', () => {
    const { qc, keys, stale } = seededClient()
    const failure = taskLockFailure(qc, 42, 7)
    const err = new ApiError(500, 'server boom')

    expect(failure.errorToast(err)).toBe(true)
    failure.onError(err)
    expect(toastError).not.toHaveBeenCalled()
    expect(stale(keys.jobDetail)).toBe(false)
  })
})
