import { describe, it, expect, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

const mockRun = vi.fn()
vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    runEditorQA: (...a: unknown[]) => mockRun(...a),
    getEditorQAReport: async () => ({ summary: { error: 0, warning: 0, info: 0, ignored: 0 }, results: [], profile_assigned: true }),
  }
})

import { useScopeQARun } from './useScopeQARun'

describe('useScopeQARun', () => {
  it('runs a large editor\'s files in several requests and keeps every outcome', async () => {
    const jobIds = Array.from({ length: 30 }, (_, i) => i + 1)
    mockRun.mockImplementation(async (_id: string, opts: { jobIds: number[] }) =>
      opts.jobIds.map(job_id => ({ job_id, outcome: 'done', issues: 0 })))
    const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    const { result } = renderHook(() => useScopeQARun({
      editorId: '5:1,2', jobIds, fileName: String, beforeRun: () => Promise.resolve(),
    }), { wrapper })

    const out = await act(() => result.current.mutateAsync(undefined))
    expect(mockRun.mock.calls.map(c => c[1].jobIds.length)).toEqual([25, 5])
    expect(out.results.map(r => r.job_id)).toEqual(jobIds)
  })
})
