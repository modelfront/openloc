// Tests for the mutation hook itself — does each of the three actions
// call the right backend, fire the right toast on success/error, and
// invalidate the right cache keys?
//
// What this file pins (and why each test matters):
//   - applyTM/MT/runAIStep each call applyJobTM/MT/runJobAIStep with the
//     jobId. A copy-paste regression where (say) the TM button is wired to
//     applyJobMT would still pass a visual smoke test but break the
//     feature; this is the load-bearing assertion.
//   - On success, the hook's own toast wording is used (the hook owns
//     it, so callers never re-write it locally).
//   - On success, invalidateJobContext is called with (projectId, jobId).
//     Without this, the job-status badge in the project list wouldn't
//     refresh after a TM/MT/AI-step run.
//   - On error, the error toast shows the backend message verbatim.
//     Backend 4xx responses are user-facing ("No MT engine configured
//     for en → de — add one in project settings"); an extra
//     "TM/MT/AI-step failed:" prefix would only duplicate the noun.
//     The red toast color + the button the user clicked carry the
//     "which action failed" context.
//
// Anti-tautology RED proofs (do these to verify a test is real, not a
// mock-of-a-mock):
//   - Swap applyJobTM and applyJobMT in the hook → the "TM calls
//     applyJobTM" test fails because applyJobMT was called instead.
//   - Drop invalidateJobContext from the success handler → the
//     invalidateQueries spy assertion fails.
//   - Change the success toast wording → the toast spy assertion fails.
//   - Drop the task_reopened branch from onAfter → "an apply that reopened
//     the task" fails; drop the isTaskLockedError branch from onFail → the
//     task-lock test fails (the job steps are not reloaded).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useJobPipelineActions } from './useJobPipelineActions'
import { ApiError } from '../api/request'
import { queryKeys } from '../lib/query-keys'

const mockApplyTM = vi.fn()
const mockApplyMT = vi.fn()
const mockRunAIStep = vi.fn()
const mockStopJob = vi.fn()
const mockRerunJob = vi.fn()

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    applyJobTM: (...args: unknown[]) => mockApplyTM(...args),
    applyJobMT: (...args: unknown[]) => mockApplyMT(...args),
    runJobAIStep: (...args: unknown[]) => mockRunAIStep(...args),
    stopJob: (...args: unknown[]) => mockStopJob(...args),
    rerunJob: (...args: unknown[]) => mockRerunJob(...args),
  }
})

const toastSuccess = vi.fn()
const toastError = vi.fn()
const toastInfo = vi.fn()
const toastWarning = vi.fn()
vi.mock('sonner', () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
    info: (m: string) => toastInfo(m),
    warning: (m: string) => toastWarning(m),
  },
}))

beforeEach(() => {
  mockApplyTM.mockReset().mockResolvedValue({ message: 'ok' })
  mockApplyMT.mockReset().mockResolvedValue({ message: 'ok' })
  mockRunAIStep.mockReset().mockResolvedValue({ message: 'ok' })
  mockStopJob.mockReset().mockResolvedValue({ status: 'stopping', job_id: 7 })
  mockRerunJob.mockReset().mockResolvedValue({ status: 'queued', phase: 'ai_step' })
  toastSuccess.mockReset()
  toastError.mockReset()
  toastInfo.mockReset()
  toastWarning.mockReset()
})

function setup() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
  const { result } = renderHook(() => useJobPipelineActions(7, 42), { wrapper })
  return { result, invalidateSpy, qc }
}

describe('useJobPipelineActions', () => {
  it('applyTM.mutate calls applyJobTM(jobId), fires the TM success toast, and invalidates job context', async () => {
    const { result, invalidateSpy } = setup()
    result.current.applyTM.mutate()
    await waitFor(() => expect(result.current.applyTM.isSuccess).toBe(true))

    expect(mockApplyTM).toHaveBeenCalledExactlyOnceWith(7)
    expect(mockApplyMT).not.toHaveBeenCalled()
    expect(mockRunAIStep).not.toHaveBeenCalled()
    expect(toastSuccess).toHaveBeenCalledWith('TM lookup complete')
    // invalidateJobContext invalidates 7 keys (see lib/query-invalidation.ts).
    // Assert two load-bearing ones rather than the full set, so this test
    // doesn't break every time someone tweaks invalidation surface:
    //   - ['jobs', 42] — the project-page job list (jobs.byProject) so
    //     the row's status badge refreshes after the apply.
    //   - any key including jobId 7 + a 'translation'-shaped segment —
    //     the CAT editor's translations cache, so reopening the editor
    //     after an apply doesn't show stale rows.
    const keys = invalidateSpy.mock.calls.map(([opts]) => (opts as { queryKey: unknown[] }).queryKey)
    expect(keys).toContainEqual(['jobs', 42])
    expect(keys.some(k =>
      Array.isArray(k)
        && k.includes(7)
        && k.some(p => typeof p === 'string' && p.toLowerCase().includes('translation'))
    )).toBe(true)
  })

  it('applyMT.mutate calls applyJobMT(jobId) with the MT success toast', async () => {
    const { result } = setup()
    result.current.applyMT.mutate()
    await waitFor(() => expect(result.current.applyMT.isSuccess).toBe(true))
    expect(mockApplyMT).toHaveBeenCalledExactlyOnceWith(7)
    expect(toastSuccess).toHaveBeenCalledWith('MT applied')
  })

  it('applyMT.mutate(ids) runs on the selection and says so', async () => {
    const { result } = setup()
    result.current.applyMT.mutate([3, 4])
    await waitFor(() => expect(result.current.applyMT.isSuccess).toBe(true))
    expect(mockApplyMT).toHaveBeenCalledExactlyOnceWith(7, [3, 4])
    expect(toastSuccess).toHaveBeenCalledWith('MT applied to the selection')
  })

  it('a selection over the request limit goes in chunks; nothing to fill in any of them is one notice', async () => {
    mockApplyTM.mockResolvedValue({ message: 'ok', job_id: 7, skip_reason: 'no_segments' })
    const ids = Array.from({ length: 501 }, (_, i) => i + 1)
    const { result } = setup()
    result.current.applyTM.mutate(ids)
    await waitFor(() => expect(result.current.applyTM.isSuccess).toBe(true))
    expect(mockApplyTM).toHaveBeenCalledTimes(2)
    expect(mockApplyTM).toHaveBeenNthCalledWith(1, 7, ids.slice(0, 500))
    expect(mockApplyTM).toHaveBeenNthCalledWith(2, 7, [501])
    expect(toastInfo).toHaveBeenCalledWith('Nothing in the selection to TM-match')
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('says how many segments the run skipped because someone else has them open', async () => {
    mockApplyMT.mockResolvedValue({ message: 'ok', job_id: 7, held: 2 })
    const { result } = setup()
    result.current.applyMT.mutate()
    await waitFor(() => expect(result.current.applyMT.isSuccess).toBe(true))
    expect(toastSuccess).toHaveBeenCalledWith('MT applied')
    expect(toastWarning).toHaveBeenCalledWith('2 segments were skipped because someone else is editing them.')
  })

  // The chunks run one after another, so a later chunk's failure leaves the
  // earlier chunks' targets written on the server.
  it.each([
    ['applyTM', mockApplyTM],
    ['applyMT', mockApplyMT],
  ] as const)('%s on a selection whose second chunk fails reloads the job context', async (action, apply) => {
    apply.mockResolvedValueOnce({ message: 'ok', job_id: 7 }).mockRejectedValueOnce(new Error('upstream timeout'))
    const ids = Array.from({ length: 501 }, (_, i) => i + 1)
    const { result, qc } = setup()
    qc.setQueryData(queryKeys.job.translations(7), [])
    qc.setQueryData(queryKeys.jobs.byProject(42), [])
    result.current[action].mutate(ids)
    await waitFor(() => expect(result.current[action].isError).toBe(true))
    expect(apply).toHaveBeenCalledTimes(2)
    expect(toastError).toHaveBeenCalledExactlyOnceWith('upstream timeout')
    expect(qc.getQueryState(queryKeys.job.translations(7))?.isInvalidated).toBe(true)
    expect(qc.getQueryState(queryKeys.jobs.byProject(42))?.isInvalidated).toBe(true)
  })

  it('runAIStep.mutate calls runJobAIStep(jobId) with the QE+APE success toast', async () => {
    const { result } = setup()
    result.current.runAIStep.mutate()
    await waitFor(() => expect(result.current.runAIStep.isSuccess).toBe(true))
    expect(mockRunAIStep).toHaveBeenCalledExactlyOnceWith(7)
    expect(toastSuccess).toHaveBeenCalledWith('QE+APE step complete')
  })

  it('on failure, the error toast shows the backend message verbatim', async () => {
    mockApplyTM.mockRejectedValueOnce(new Error('No TM configured for en → de — add one in project settings'))
    const { result } = setup()
    result.current.applyTM.mutate()
    await waitFor(() => expect(result.current.applyTM.isError).toBe(true))
    expect(toastError).toHaveBeenCalledWith('No TM configured for en → de — add one in project settings')
    // Success toast must NOT fire on failure — otherwise the user sees
    // both a green "TM lookup complete" and a red error.
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  // Anti-tautology RED proof: remove the skip_reason branch from the
  // hook (always call toast.success) — all three below fail.
  it('applyMT with skip_reason=no_segments fires the info toast, not success', async () => {
    mockApplyMT.mockResolvedValueOnce({ message: 'mt-apply complete', skip_reason: 'no_segments' })
    const { result } = setup()
    result.current.applyMT.mutate()
    await waitFor(() => expect(result.current.applyMT.isSuccess).toBe(true))
    expect(toastInfo).toHaveBeenCalledExactlyOnceWith('No segments to translate')
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('applyTM with skip_reason=no_segments fires the info toast, not success', async () => {
    mockApplyTM.mockResolvedValueOnce({ message: 'tm-apply complete', skip_reason: 'no_segments' })
    const { result } = setup()
    result.current.applyTM.mutate()
    await waitFor(() => expect(result.current.applyTM.isSuccess).toBe(true))
    expect(toastInfo).toHaveBeenCalledExactlyOnceWith('No segments to TM-match')
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('runAIStep with skip_reason=no_segments fires the info toast, not success', async () => {
    mockRunAIStep.mockResolvedValueOnce({ message: 'ai-step complete', skip_reason: 'no_segments' })
    const { result } = setup()
    result.current.runAIStep.mutate()
    await waitFor(() => expect(result.current.runAIStep.isSuccess).toBe(true))
    expect(toastInfo).toHaveBeenCalledExactlyOnceWith('No segments to score')
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  // Anti-tautology RED proof: drop the `data?.stopped` branch → this fails
  // (the green "MT applied" fires for a run that was cut short).
  it('an apply that resolves stopped:true fires the stopped info toast, not success', async () => {
    mockApplyMT.mockResolvedValueOnce({ message: 'mt-apply stopped', job_id: 7, stopped: true })
    const { result } = setup()
    result.current.applyMT.mutate()
    await waitFor(() => expect(result.current.applyMT.isSuccess).toBe(true))
    expect(toastInfo).toHaveBeenCalledExactlyOnceWith('Stopped — translated segments are kept')
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('stop.mutate calls stopJob(jobId), fires the stopping info toast, and refreshes the job list', async () => {
    const { result, invalidateSpy } = setup()
    result.current.stop.mutate()
    await waitFor(() => expect(result.current.stop.isSuccess).toBe(true))
    expect(mockStopJob).toHaveBeenCalledExactlyOnceWith(7)
    expect(toastInfo).toHaveBeenCalledExactlyOnceWith(
      'Stopping — the current chunk finishes first; translated segments are kept',
    )
    expect(toastSuccess).not.toHaveBeenCalled()
    // The project row and the editor header both read stop_requested_at from
    // these — without the refetch the badge stays "Translating" until the
    // next 2s poll tick.
    const keys = invalidateSpy.mock.calls.map(([opts]) => (opts as { queryKey: unknown[] }).queryKey)
    expect(keys).toContainEqual(['jobs', 42])
    expect(keys.some(k => Array.isArray(k) && k.includes(7))).toBe(true)
  })

  it('stop failure (409: nothing running / already stopping) shows the backend message verbatim', async () => {
    mockStopJob.mockRejectedValueOnce(new Error('no running pipeline to stop'))
    const { result } = setup()
    result.current.stop.mutate()
    await waitFor(() => expect(result.current.stop.isError).toBe(true))
    expect(toastError).toHaveBeenCalledExactlyOnceWith('no running pipeline to stop')
    expect(toastInfo).not.toHaveBeenCalled()
  })

  it('a pending stop does NOT count as isAnyApplying (Stop must stay live during a run)', async () => {
    let resolveStop: (v: { status: string; job_id: number }) => void = () => {}
    mockStopJob.mockImplementationOnce(() => new Promise(res => { resolveStop = res }))
    const { result } = setup()
    result.current.stop.mutate()
    await waitFor(() => expect(result.current.stop.isPending).toBe(true))
    expect(result.current.isAnyApplying).toBe(false)
    resolveStop({ status: 'stopping', job_id: 7 })
    await waitFor(() => expect(result.current.stop.isPending).toBe(false))
  })

  it('rerun.mutate calls rerunJob(jobId), says it is queued, and refreshes the job', async () => {
    const { result, invalidateSpy } = setup()
    result.current.rerun.mutate()
    await waitFor(() => expect(result.current.rerun.isSuccess).toBe(true))
    expect(mockRerunJob).toHaveBeenCalledExactlyOnceWith(7)
    expect(toastSuccess).toHaveBeenCalledExactlyOnceWith('Re-run queued')
    const keys = invalidateSpy.mock.calls.map(([opts]) => (opts as { queryKey: unknown[] }).queryKey)
    expect(keys).toContainEqual(queryKeys.job.detail(7))
  })

  it('an apply that reopened the task says so and reloads the task', async () => {
    mockApplyTM.mockResolvedValueOnce({ message: 'ok', job_id: 7, task_reopened: true })
    const { result, invalidateSpy } = setup()
    result.current.applyTM.mutate()
    await waitFor(() => expect(result.current.applyTM.isSuccess).toBe(true))
    expect(toastSuccess).toHaveBeenCalledWith('Task reopened')
    expect(toastSuccess).toHaveBeenCalledWith('TM lookup complete')
    const keys = invalidateSpy.mock.calls.map(([opts]) => (opts as { queryKey: unknown[] }).queryKey)
    expect(keys).toContainEqual(queryKeys.tasks.all)
  })

  it.each([
    ['applyTM', 'TM lookup complete'],
    ['applyMT', 'MT applied'],
    ['runAIStep', 'QE+APE step complete'],
  ] as const)('%s without task_reopened announces no reopen and leaves the task list alone', async (action, successToast) => {
    const { result, invalidateSpy } = setup()
    result.current[action].mutate()
    await waitFor(() => expect(result.current[action].isSuccess).toBe(true))
    expect(toastSuccess).toHaveBeenCalledExactlyOnceWith(successToast)
    const keys = invalidateSpy.mock.calls.map(([opts]) => (opts as { queryKey: unknown[] }).queryKey)
    expect(keys).not.toContainEqual(queryKeys.tasks.all)
  })

  it('a task-lock refusal toasts the server message and reloads the job steps', async () => {
    mockApplyMT.mockRejectedValueOnce(new ApiError(409, 'This task has an error.', { error: { code: 'task_errored', message: 'This task has an error.' } }))
    const { result, invalidateSpy } = setup()
    result.current.applyMT.mutate()
    await waitFor(() => expect(result.current.applyMT.isError).toBe(true))
    expect(toastError).toHaveBeenCalledExactlyOnceWith('This task has an error.')
    const keys = invalidateSpy.mock.calls.map(([opts]) => (opts as { queryKey: unknown[] }).queryKey)
    expect(keys).toContainEqual(queryKeys.job.steps(7))
  })

  // The demo organization's refusal already has one notice from the query
  // client; a second, red toast here would say the same thing twice.
  it.each(['applyTM', 'stop', 'rerun'] as const)('%s refused by the demo organization shows no error toast', async (action) => {
    const refusal = new ApiError(403, 'refused', { error: { code: 'demo_read_only' } })
    mockApplyTM.mockRejectedValueOnce(refusal)
    mockStopJob.mockRejectedValueOnce(refusal)
    mockRerunJob.mockRejectedValueOnce(refusal)
    const { result } = setup()
    result.current[action].mutate()
    await waitFor(() => expect(result.current[action].isError).toBe(true))
    expect(toastError).not.toHaveBeenCalled()
  })

  it('isAnyApplying flips true while any of the three mutations is pending', async () => {
    // Hold the promise open so we can observe the pending state.
    let resolveTM: (v: { message: string }) => void = () => {}
    mockApplyTM.mockImplementationOnce(() => new Promise(res => { resolveTM = res }))

    const { result } = setup()
    expect(result.current.isAnyApplying).toBe(false)
    result.current.applyTM.mutate()
    await waitFor(() => expect(result.current.isAnyApplying).toBe(true))
    resolveTM({ message: 'ok' })
    await waitFor(() => expect(result.current.isAnyApplying).toBe(false))
  })
})
