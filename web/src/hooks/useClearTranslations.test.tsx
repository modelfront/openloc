// Anti-tautology: drop cleared===0 branch in clearedToast → "no
// translations" test fails. Remove predicate invalidation → project test
// fails.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  useClearSegment,
  useClearJobTranslations,
  useClearProjectTranslations,
} from './useClearTranslations'
import { queryKeys } from '../lib/query-keys'
import { ApiError } from '../api/request'

const mockClearTranslation = vi.fn()
const mockClearJob = vi.fn()
const mockClearProject = vi.fn()

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    clearTranslation: (...args: unknown[]) => mockClearTranslation(...args),
    clearJobTranslations: (...args: unknown[]) => mockClearJob(...args),
    clearProjectTranslations: (...args: unknown[]) => mockClearProject(...args),
  }
})

const toastSuccess = vi.fn()
const toastError = vi.fn()
const toastWarning = vi.fn()
vi.mock('sonner', () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
    warning: (m: string) => toastWarning(m),
  },
}))

beforeEach(() => {
  mockClearTranslation.mockReset().mockResolvedValue({ id: 1 })
  mockClearJob.mockReset().mockResolvedValue({ cleared: 3 })
  mockClearProject.mockReset().mockResolvedValue({ cleared: 7 })
  toastSuccess.mockReset()
  toastError.mockReset()
  toastWarning.mockReset()
})

function wrap() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
  return { qc, invalidateSpy, wrapper }
}

describe('useClearSegment', () => {
  it('calls clearTranslation(fileId, translationId) and invalidates job + project caches', async () => {
    const { invalidateSpy, wrapper } = wrap()
    const { result } = renderHook(
      () => useClearSegment({ fileId: 11, jobId: 7, projectId: 42 }),
      { wrapper },
    )
    result.current.mutate(99)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(mockClearTranslation).toHaveBeenCalledExactlyOnceWith(11, 99)
    expect(toastSuccess).toHaveBeenCalledWith('Translation cleared')

    const keys = invalidateSpy.mock.calls.map(([opts]) => (opts as { queryKey?: unknown[] }).queryKey)
    expect(keys).toContainEqual(queryKeys.job.translations(7))
    expect(keys).toContainEqual(['jobs', 42])
  })
})

describe('useClearSegment — task edit guard', () => {
  it('a clear that reopened the task says so and refreshes the task list', async () => {
    mockClearTranslation.mockResolvedValueOnce({ translation: { id: 99 }, taskReopened: true })
    const { invalidateSpy, wrapper } = wrap()
    const { result } = renderHook(() => useClearSegment({ fileId: 11, jobId: 7, projectId: 42 }), { wrapper })
    result.current.mutate(99)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(toastSuccess).toHaveBeenCalledWith('Task reopened')
    const keys = invalidateSpy.mock.calls.map(([opts]) => (opts as { queryKey?: unknown[] }).queryKey)
    expect(keys).toContainEqual(queryKeys.tasks.all)
  })

  it('a task-lock refusal toasts the server message and reloads the job steps', async () => {
    mockClearTranslation.mockRejectedValueOnce(
      new ApiError(403, 'This task is completed.', { error: { code: 'task_completed', message: 'This task is completed.' } }),
    )
    const { invalidateSpy, wrapper } = wrap()
    const { result } = renderHook(() => useClearSegment({ fileId: 11, jobId: 7, projectId: 42 }), { wrapper })
    result.current.mutate(99)
    await waitFor(() => expect(result.current.isError).toBe(true))

    expect(toastError).toHaveBeenCalledExactlyOnceWith('This task is completed.')
    const keys = invalidateSpy.mock.calls.map(([opts]) => (opts as { queryKey?: unknown[] }).queryKey)
    expect(keys).toContainEqual(queryKeys.job.steps(7))
  })
})

describe('useClearJobTranslations', () => {
  it('calls clearJobTranslations with scope and surfaces cleared count', async () => {
    const { wrapper } = wrap()
    const { result } = renderHook(
      () => useClearJobTranslations({ jobId: 7, projectId: 42 }),
      { wrapper },
    )
    result.current.mutate('non_confirmed')
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(mockClearJob).toHaveBeenCalledExactlyOnceWith(7, 'non_confirmed')
    expect(toastSuccess).toHaveBeenCalledWith('Cleared 3 translations')
  })

  it('shows "No translations to clear" when cleared=0 (anti-lying toast)', async () => {
    mockClearJob.mockResolvedValueOnce({ cleared: 0 })
    const { wrapper } = wrap()
    const { result } = renderHook(
      () => useClearJobTranslations({ jobId: 7, projectId: 42 }),
      { wrapper },
    )
    result.current.mutate('all')
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(toastSuccess).toHaveBeenCalledWith('No translations to clear')
  })

  it('singularises when cleared=1', async () => {
    mockClearJob.mockResolvedValueOnce({ cleared: 1 })
    const { wrapper } = wrap()
    const { result } = renderHook(
      () => useClearJobTranslations({ jobId: 7, projectId: 42 }),
      { wrapper },
    )
    result.current.mutate('non_confirmed')
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(toastSuccess).toHaveBeenCalledWith('Cleared 1 translation')
  })

  it('surfaces backend error verbatim', async () => {
    mockClearJob.mockRejectedValueOnce(new Error('pipeline is running — wait for it to finish before clearing'))
    const { wrapper } = wrap()
    const { result } = renderHook(
      () => useClearJobTranslations({ jobId: 7, projectId: 42 }),
      { wrapper },
    )
    result.current.mutate('all')
    await waitFor(() => expect(result.current.isError).toBe(true))

    expect(toastError).toHaveBeenCalledWith('pipeline is running — wait for it to finish before clearing')
    expect(toastSuccess).not.toHaveBeenCalled()
  })
})

describe('useClearProjectTranslations', () => {
  it('calls clearProjectTranslations and uses predicate to invalidate every job cache', async () => {
    const { qc, invalidateSpy, wrapper } = wrap()
    // Prime the cache with two job entries so the predicate has something
    // to match against.
    qc.setQueryData(['job', 'detail', 1], { id: 1 })
    qc.setQueryData(['job', 'translations', 2], [])

    const { result } = renderHook(() => useClearProjectTranslations(42), { wrapper })
    result.current.mutate('non_confirmed')
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(mockClearProject).toHaveBeenCalledExactlyOnceWith(42, 'non_confirmed')
    expect(toastSuccess).toHaveBeenCalledWith('Cleared 7 translations')

    const predicateCalls = invalidateSpy.mock.calls.filter(([opts]) => 'predicate' in (opts as object))
    expect(predicateCalls.length).toBeGreaterThanOrEqual(1)
  })

  // Built from queryKeys, not hand-written arrays: the job caches are keyed
  // 'job-translations' / 'job-qa-report' / …, so a predicate testing
  // `queryKey[0] === 'job'` reaches only the detail cache and leaves the
  // cleared project's segments and QA dots on screen.
  it('predicate reaches every job-scoped cache, not just job detail', async () => {
    const { invalidateSpy, wrapper } = wrap()
    const { result } = renderHook(() => useClearProjectTranslations(42), { wrapper })
    result.current.mutate('all')
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const predicate = invalidateSpy.mock.calls
      .map(([opts]) => (opts as { predicate?: (q: { queryKey: unknown[] }) => boolean }).predicate)
      .find((p) => typeof p === 'function')
    expect(predicate).toBeTypeOf('function')

    const jobKeys = [
      queryKeys.job.detail(1),
      queryKeys.job.translations(1),
      queryKeys.job.analysis(1),
      queryKeys.job.steps(1),
      queryKeys.job.leverage(1),
      queryKeys.job.qaReport(1),
    ]
    expect(jobKeys.filter((k) => !predicate!({ queryKey: [...k] }))).toEqual([])
    // Not a prefix match on the string: unrelated caches stay warm.
    expect(predicate!({ queryKey: [...queryKeys.tms.all] })).toBe(false)
    expect(predicate!({ queryKey: [...queryKeys.jobs.byProject(1)] })).toBe(false)
  })

  // The clear drops the AI comments of every row it touched. Neither comment
  // key starts with 'job', so the predicate above cannot reach them — an open
  // panel kept showing a comment the server had deleted.
  it('invalidates the comment list and counts, which the job predicate misses', async () => {
    const { invalidateSpy, wrapper } = wrap()
    const { result } = renderHook(() => useClearProjectTranslations(42), { wrapper })
    result.current.mutate('all')
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const keys = invalidateSpy.mock.calls
      .map(([opts]) => (opts as { queryKey?: unknown[] }).queryKey)
      .filter(Boolean)
      .map((k) => JSON.stringify(k))
    expect(keys).toContain(JSON.stringify([...queryKeys.comments.all]))
    expect(keys).toContain(JSON.stringify([...queryKeys.comments.allCounts]))
  })
})

describe('held segments', () => {
  it('a job clear says how many segments it kept because someone else has them open', async () => {
    mockClearJob.mockResolvedValue({ cleared: 3, held: 1 })
    const { wrapper } = wrap()
    const { result } = renderHook(() => useClearJobTranslations({ jobId: 7, projectId: 42 }), { wrapper })
    result.current.mutate('all')
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(toastSuccess).toHaveBeenCalledWith('Cleared 3 translations')
    expect(toastWarning).toHaveBeenCalledWith('1 segment was skipped because someone else is editing it.')
  })

  it('a project clear does the same', async () => {
    mockClearProject.mockResolvedValue({ cleared: 7, held: 2 })
    const { wrapper } = wrap()
    const { result } = renderHook(() => useClearProjectTranslations(42), { wrapper })
    result.current.mutate('all')
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(toastWarning).toHaveBeenCalledWith('2 segments were skipped because someone else is editing them.')
  })

  it('no note when nothing was held', async () => {
    const { wrapper } = wrap()
    const { result } = renderHook(() => useClearJobTranslations({ jobId: 7, projectId: 42 }), { wrapper })
    result.current.mutate('all')
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(toastWarning).not.toHaveBeenCalled()
  })
})
