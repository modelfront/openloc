import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

const mockListJobs = vi.fn()
vi.mock('../api', () => ({
  listJobs: (...a: unknown[]) => mockListJobs(...a),
}))

import { useProjectJobs } from './useProjectJobs'
import type { Job } from '../api/analytics'

const job = (id: number, pipeline_status: string, current_step: string | null = null) =>
  ({ id, pipeline_status, current_step }) as unknown as Job

function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  mockListJobs.mockReset()
})
afterEach(() => vi.useRealTimers())

describe('useProjectJobs', () => {
  it('after the first full load, polls only the running jobs and merges them in', async () => {
    mockListJobs.mockImplementation((_pid: number, opts?: { ids?: number[] }) =>
      Promise.resolve(opts?.ids ? [job(2, 'completed')] : [job(1, 'completed'), job(2, 'in_progress', 'Translating')]))
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result } = renderHook(() => useProjectJobs(5, false), { wrapper: wrapper(qc) })

    await waitFor(() => expect(result.current.data).toHaveLength(2))
    await act(async () => { await vi.advanceTimersByTimeAsync(2500) })

    await waitFor(() => expect(result.current.data?.find(j => j.id === 2)?.pipeline_status).toBe('completed'))
    const fullCalls = mockListJobs.mock.calls.filter(c => !c[1]?.ids)
    const polls = mockListJobs.mock.calls.filter(c => c[1]?.ids)
    expect(fullCalls).toHaveLength(1)
    expect(polls.length).toBeGreaterThan(0)
    for (const c of polls) expect(c[1].ids).toEqual([2])

    // Nothing runs any more: the polling stops.
    const before = mockListJobs.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(mockListJobs.mock.calls.length).toBe(before)
  })

  it('drops a running job the poll no longer returns (deleted), and stops polling', async () => {
    mockListJobs.mockImplementation((_pid: number, opts?: { ids?: number[] }) =>
      Promise.resolve(opts?.ids ? [] : [job(1, 'completed'), job(2, 'in_progress', 'Translating')]))
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result } = renderHook(() => useProjectJobs(5, false), { wrapper: wrapper(qc) })

    await act(async () => { await vi.advanceTimersByTimeAsync(2500) })
    await waitFor(() => expect(result.current.data?.map(j => j.id)).toEqual([1]))
    expect(mockListJobs).toHaveBeenCalledWith(5, { ids: [2] })

    const before = mockListJobs.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(mockListJobs.mock.calls.length).toBe(before)
  })

  it('right after an upload, polls the full list, which is where new jobs appear', async () => {
    mockListJobs.mockResolvedValue([job(1, 'in_progress', 'Translating')])
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    renderHook(() => useProjectJobs(5, true), { wrapper: wrapper(qc) })

    await waitFor(() => expect(mockListJobs).toHaveBeenCalledTimes(1))
    await act(async () => { await vi.advanceTimersByTimeAsync(4500) })
    expect(mockListJobs.mock.calls.length).toBeGreaterThan(1)
    for (const c of mockListJobs.mock.calls) expect(c[1]?.ids).toBeUndefined()
  })
})
