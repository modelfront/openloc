// The filter bar's text search runs on the server. Until it answers the text
// in the box, and when it fails, the grid must show nothing it has not found:
// bulk actions and the spell check's filtered scope read the same rows.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

const mockSearch = vi.fn()
vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return { ...actual, searchEditor: (...args: unknown[]) => mockSearch(...args) }
})

import { ApiError, type EditorSearch } from '../api'
import { useEditorSearch } from './useEditorRows'

const search = (text: string, regex = false): EditorSearch => ({ text, source: true, target: true, caseSensitive: false, regex })

function render(initial: EditorSearch | null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  return renderHook(({ s }) => useEditorSearch(7, s), { wrapper, initialProps: { s: initial } })
}

beforeEach(() => { mockSearch.mockClear() })

describe('useEditorSearch', () => {
  it('matches nothing while the answer is pending, then the segments found', async () => {
    let answer: (ids: number[]) => void = () => {}
    mockSearch.mockReturnValue(new Promise<number[]>(r => { answer = r }))
    const { result } = render(search('dry'))

    expect(result.current).toEqual({ matches: new Set(), state: 'pending' })
    answer([4, 9])
    await waitFor(() => expect(result.current.state).toBeNull())
    expect(result.current.matches).toEqual(new Set([4, 9]))
  })

  it('does not keep the previous search\'s matches while a new one is pending', async () => {
    mockSearch.mockResolvedValueOnce([4])
    const { result, rerender } = render(search('dry'))
    await waitFor(() => expect(result.current.matches).toEqual(new Set([4])))

    let answer: (ids: number[]) => void = () => {}
    mockSearch.mockReturnValue(new Promise<number[]>(r => { answer = r }))
    rerender({ s: search('wet') })

    expect(result.current).toEqual({ matches: new Set(), state: 'pending' })
    answer([5])
    await waitFor(() => expect(result.current.matches).toEqual(new Set([5])))
  })

  it('says a refused pattern is invalid', async () => {
    mockSearch.mockImplementation(() => Promise.reject(new ApiError(400, 'invalid regular expression')))
    const { result } = render(search('(?=a)', true))
    await waitFor(() => expect(result.current.state).toBe('invalid'))
    expect(result.current.matches).toEqual(new Set())
  })

  it('says a search that could not run failed, rather than finding nothing', async () => {
    mockSearch.mockImplementation(() => Promise.reject(new ApiError(500, 'failed to search segments')))
    const { result } = render(search('dry'))
    await waitFor(() => expect(result.current.state).toBe('failed'))
    expect(result.current.matches).toEqual(new Set())
  })

  it('applies no text filter without search text', () => {
    const { result } = render(null)
    expect(result.current).toEqual({ matches: null, state: null })
    expect(mockSearch).not.toHaveBeenCalled()
  })
})
