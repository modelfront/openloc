import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider, useMutation } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { queryKeys } from '../lib/query-keys'
import { JOB_POLL_MS, useJobDetail } from './useJobDetail'

const getJob = vi.fn()
vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  getJob: (...args: unknown[]) => getJob(...args),
}))

const JOB = 7
const stopped = { id: JOB, pipeline_status: 'stopped', current_step: 'Stopped — 180 / 12180 segments translated; run Pre-translate again to resume' }

// The job query, and a request that runs a phase on the job and stays open
// until the run ends, mounted the way the CAT editor mounts them.
function setup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  const { result } = renderHook(() => ({
    job: useJobDetail(JOB),
    run: useMutation({ mutationKey: queryKeys.job.runRequest(JOB), mutationFn: () => new Promise(() => {}) }),
  }), { wrapper })
  const wait = async (ms: number) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }
  return { result, wait }
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: false })
  getJob.mockReset().mockResolvedValue(stopped)
})
afterEach(() => { vi.useRealTimers() })

describe('useJobDetail', () => {
  it('does not poll a job whose run stopped', async () => {
    const { wait } = setup()
    await wait(0)
    await wait(JOB_POLL_MS * 3)
    expect(getJob).toHaveBeenCalledTimes(1)
  })

  // Pre-translate on a stopped or finished job starts a run inside a request
  // that stays open until the run ends; the job must be read while it runs, or
  // the counter and the live counts stay hidden until the editor is reopened.
  it('polls while a request that runs a phase is open', async () => {
    const { result, wait } = setup()
    await wait(0)
    // The request claims the job and the run starts.
    getJob.mockResolvedValue({ ...stopped, pipeline_status: 'in_progress', current_step: 'MT: 30 / 12000 segments' })
    act(() => { result.current.run.mutate() })
    await wait(JOB_POLL_MS)
    expect(getJob).toHaveBeenCalledTimes(2)
    await vi.waitFor(() => expect(result.current.job.data?.pipeline_status).toBe('in_progress'))
  })
})
