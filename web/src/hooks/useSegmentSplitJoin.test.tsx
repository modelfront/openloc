import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

const mockSplit = vi.fn()
const mockJoin = vi.fn()
const mockIndex = vi.fn()
vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    splitSegment: (...a: unknown[]) => mockSplit(...a),
    joinSegments: (...a: unknown[]) => mockJoin(...a),
    getEditorIndex: (...a: unknown[]) => mockIndex(...a),
  }
})

import { indexLookup } from '@/lib/editor-index'
import { queryKeys } from '@/lib/query-keys'
import { useSegmentSplitJoin } from './useSegmentSplitJoin'

const SCOPE = '5:1,2'

function setup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  const view = renderHook(
    () => useSegmentSplitJoin({ editorId: SCOPE, projectId: 5, lookup: indexLookup([]), openTranslation: () => {} }),
    { wrapper },
  )
  return { ...view, qc }
}

beforeEach(() => {
  mockSplit.mockReset().mockResolvedValue({ translation_ids: [30] })
  mockJoin.mockReset().mockResolvedValue({ translation_ids: [20] })
  mockIndex.mockReset().mockResolvedValue({ segment_ids: [], files: [] })
})

describe('useSegmentSplitJoin in a multi-file editor', () => {
  it('splits through the row\'s own job and reloads the editor\'s index', async () => {
    const { result } = setup()
    await act(() => result.current.split.mutateAsync({ jobId: 2, req: { translation_id: 21, offset: 3, expected_updated_at: 'v' } }))
    expect(mockSplit).toHaveBeenCalledWith(2, { translation_id: 21, offset: 3, expected_updated_at: 'v' })
    await waitFor(() => expect(mockIndex).toHaveBeenCalledWith(SCOPE))
  })

  it('joins through the job with the versions the editor\'s rows hold', async () => {
    const { result, qc } = setup()
    qc.setQueryData([...queryKeys.job.editorRows(SCOPE), 0, 1], [
      { segment: { id: 1 }, translation: { id: 21, updated_at: 'v21' } },
      { segment: { id: 2 }, translation: { id: 22, updated_at: 'v22' } },
    ])
    act(() => result.current.joinTranslations(2, [21, 22]))
    await waitFor(() => expect(mockJoin).toHaveBeenCalledWith(2, [
      { translation_id: 21, expected_updated_at: 'v21' },
      { translation_id: 22, expected_updated_at: 'v22' },
    ]))
    await waitFor(() => expect(mockIndex).toHaveBeenCalledWith(SCOPE))
  })
})
