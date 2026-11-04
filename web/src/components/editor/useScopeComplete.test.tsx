import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { ScopeFile } from '@/api'

const mockComplete = vi.fn()
vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return { ...actual, completeEditorScope: (...a: unknown[]) => mockComplete(...a) }
})

import { useScopeComplete } from './useScopeComplete'

const ME = 1
const file = (jobId: number, stepId: number): ScopeFile => ({
  job_id: jobId, file_id: jobId * 10, file_name: `f${jobId}.json`, file_format: 'json', inline_html: false,
  first_row: 0, row_count: 1, step_name: 'Translation', step_assignee: ME, step_id: stepId, has_previous_step: false,
})

function setup(initialFiles: ScopeFile[]) {
  const onAllDone = vi.fn()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  const view = renderHook(
    (p: { files: ScopeFile[] }) => useScopeComplete({
      editorId: '5:1,2', projectId: 5, files: p.files, isManager: false, currentUserId: ME,
      beforeSend: () => Promise.resolve(), onAllDone,
    }),
    { wrapper, initialProps: { files: initialFiles } },
  )
  return { ...view, onAllDone }
}

beforeEach(() => { mockComplete.mockReset() })

describe('useScopeComplete', () => {
  it('a retry resends only the failed files, with the step ids taken when the dialog opened', async () => {
    mockComplete
      .mockResolvedValueOnce([
        { job_id: 1, outcome: 'advanced' },
        { job_id: 2, outcome: 'blocked', blocked: [{ id: 9, reason: 'held' }] },
      ])
      .mockResolvedValueOnce([{ job_id: 2, outcome: 'advanced' }])
    const { result, rerender, onAllDone } = setup([file(1, 101), file(2, 201)])

    act(() => result.current.openDialog())
    await act(() => result.current.mutation.mutateAsync({ comment: 'c', approveAll: false }))
    expect(mockComplete.mock.calls[0][1]).toMatchObject({ job_ids: [1, 2], steps: { 1: 101, 2: 201 } })
    expect(result.current.failures?.map(f => f.job_id)).toEqual([2])

    // The index reloads after the first try: file 1 moved on, and file 2's
    // step id changed under the editor. The retry still sends what the user saw.
    rerender({ files: [file(1, 102), file(2, 202)] })
    await act(() => result.current.mutation.mutateAsync({ comment: 'c', approveAll: false }))
    expect(mockComplete.mock.calls[1][1]).toEqual({ comment: 'c', approve_all: false, job_ids: [2], steps: { 2: 201 } })
    await waitFor(() => expect(onAllDone).toHaveBeenCalledOnce())
    expect(result.current.open).toBe(false)
  })

  it('sends files in batches of 25', async () => {
    const files = Array.from({ length: 26 }, (_, i) => file(i + 1, 100 + i))
    mockComplete.mockImplementation((_id: unknown, body: { job_ids: number[] }) =>
      Promise.resolve(body.job_ids.map(id => ({ job_id: id, outcome: 'advanced' }))))
    const { result } = setup(files)

    act(() => result.current.openDialog())
    await act(() => result.current.mutation.mutateAsync({ comment: '', approveAll: true }))
    expect(mockComplete.mock.calls.map(c => (c[1] as { job_ids: number[] }).job_ids.length)).toEqual([25, 1])
  })

  it('only offers files whose step the user may act on', () => {
    const other = { ...file(3, 301), step_assignee: 99 }
    const aiRunning = { ...file(4, 401), edit_block: { code: 'ai_step_running', message: '' } }
    const { result } = setup([file(1, 101), other, aiRunning, { ...file(5, 0), step_name: '' }])
    expect(result.current.completable.map(f => f.job_id)).toEqual([1])
  })
})
