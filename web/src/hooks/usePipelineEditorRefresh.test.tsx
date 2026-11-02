import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { queryKeys } from '../lib/query-keys'
import { LIVE_REFRESH_MS, liveRefreshMs, usePipelineEditorRefresh } from './usePipelineEditorRefresh'

const JOB = 7

interface Job { pipeline_status: string; current_step?: string | null; translated_count?: number | null }
const mt = (n: number): Job => ({ pipeline_status: 'in_progress', current_step: `MT: ${n} / 400 segments`, translated_count: n })
const done: Job = { pipeline_status: 'in_progress', current_step: 'Translation complete', translated_count: 400 }

// The editor index, one page of rows on screen and the job steps, mounted like
// the CAT editor mounts them, with each fetch counted: what the hook makes the
// editor load is what the test asserts.
function setup(first: Job, segmentCount = 400) {
  const fetches = { index: 0, rows: 0, steps: 0 }
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  const { rerender, unmount } = renderHook(({ job }) => {
    useQuery({ queryKey: queryKeys.job.editorIndex(JOB), queryFn: async () => ++fetches.index })
    useQuery({ queryKey: queryKeys.job.editorPage(JOB, 0, '1,2'), queryFn: async () => ++fetches.rows })
    useQuery({ queryKey: queryKeys.job.steps(JOB), queryFn: async () => ++fetches.steps })
    usePipelineEditorRefresh(JOB, job, segmentCount)
  }, { wrapper, initialProps: { job: first } })
  const report = async (job: Job) => {
    rerender({ job })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  }
  const wait = async (ms: number) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }
  return { fetches, report, wait, unmount }
}

beforeEach(() => { vi.useFakeTimers({ shouldAdvanceTime: false }) })
afterEach(() => { vi.useRealTimers() })

describe('usePipelineEditorRefresh', () => {
  it('reloads the index and the rows on screen when a run reports new progress', async () => {
    const { fetches, report, wait } = setup(mt(0))
    await wait(0)
    expect(fetches).toMatchObject({ index: 1, rows: 1 })

    await wait(LIVE_REFRESH_MS)
    await report(mt(30))
    expect(fetches).toMatchObject({ index: 2, rows: 2 })
  })

  it('does not reload while the progress stays the same', async () => {
    const { fetches, report, wait } = setup(mt(30))
    await wait(LIVE_REFRESH_MS)
    await report(mt(30))
    await wait(LIVE_REFRESH_MS * 3)
    expect(fetches.index).toBe(1)
  })

  it('reloads at most once per interval, and does not lose the last report', async () => {
    const { fetches, report, wait } = setup(mt(0))
    await wait(LIVE_REFRESH_MS)
    await report(mt(30))
    expect(fetches.index).toBe(2)

    await report(mt(60))
    await report(mt(90))
    await wait(LIVE_REFRESH_MS - 1)
    expect(fetches.index).toBe(2)

    await wait(1)
    expect(fetches).toMatchObject({ index: 3, rows: 3 })
  })

  it('waits longer between reloads on a bigger file', async () => {
    expect(liveRefreshMs(400)).toBe(LIVE_REFRESH_MS)
    expect(liveRefreshMs(25_000)).toBe(3 * LIVE_REFRESH_MS)
    expect(liveRefreshMs(500_000)).toBe(30_000)

    const { fetches, report, wait } = setup(mt(0), 25_000)
    await wait(30_000)
    await report(mt(30))
    expect(fetches.index).toBe(2)

    await report(mt(60))
    await wait(3 * LIVE_REFRESH_MS - 1)
    expect(fetches.index).toBe(2)
    await wait(1)
    expect(fetches.index).toBe(3)
  })

  it('reloads the index and the steps once when the run ends', async () => {
    const { fetches, report, wait } = setup(mt(0))
    await wait(0)
    await report(done)
    expect(fetches).toEqual({ index: 2, rows: 2, steps: 2 })

    await report(done)
    await wait(LIVE_REFRESH_MS * 2)
    expect(fetches).toEqual({ index: 2, rows: 2, steps: 2 })
  })

  it('does not follow the AI step, which hides the grid', async () => {
    const ai = (step: string): Job => ({ pipeline_status: 'in_progress', current_step: step, translated_count: null })
    const { fetches, report, wait } = setup(ai('Running AI step (QE)'))
    await wait(LIVE_REFRESH_MS)
    await report({ ...ai('Running AI step (QE)'), translated_count: 50 })
    await wait(LIVE_REFRESH_MS)
    expect(fetches.index).toBe(1)
  })
})
