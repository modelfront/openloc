import { describe, expect, it, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { IndexRow } from '../lib/editor-index'
import type { EditorIndex } from '../api'
import { refetchIndexWithPages, useEditorPages } from './useEditorRows'

const getEditorRows = vi.fn()
const getEditorIndex = vi.fn()
vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  getEditorRows: (...args: unknown[]) => getEditorRows(...args),
  getEditorIndex: (...args: unknown[]) => getEditorIndex(...args),
}))

const rowsOf = (ids: number[]) => ids.map(id => ({ segmentId: id }) as IndexRow)

beforeEach(() => {
  getEditorRows.mockReset().mockImplementation(async (_job: number, ids: number[]) =>
    ids.map(id => ({ segment: { id }, translation: null })))
})

describe('useEditorPages', () => {
  // A split replaces a row with two new ones and leaves the page's first row
  // where it was: the page is fetched again with the new ids, or the new rows
  // never load.
  it('fetches a page again when its segments change behind the same first row', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    const { result, rerender } = renderHook(({ rows }) => useEditorPages(7, rows, [0, 1, 2, 3]), {
      wrapper, initialProps: { rows: rowsOf([1, 2, 3]) },
    })
    await waitFor(() => expect(result.current.has(2)).toBe(true))

    rerender({ rows: rowsOf([1, 21, 22, 3]) })
    await waitFor(() => expect(result.current.has(21) && result.current.has(22)).toBe(true))
    expect(getEditorRows).toHaveBeenLastCalledWith(7, [1, 21, 22, 3])
  })
})

/** An editor index of these segments, one row each, in this order. */
function indexOf(ids: number[]): EditorIndex {
  const fill = <T,>(v: T) => ids.map(() => v)
  return {
    segment_ids: ids, translation_ids: ids, positions: ids.map((_, i) => i), sub_positions: fill(0),
    joins_next: fill(false), word_counts: fill(1), statuses: fill('needs_review'), has_target: fill(false),
    step_edited: fill(false), predicted: fill(false), origins: fill(''), tm_match: fill(null), locked: fill(false),
    in_flight: fill(''), confirmed_via: fill(''), qe_note_heads: fill(''), repetition: fill(0),
  } as unknown as EditorIndex
}

describe('refetchIndexWithPages', () => {
  // A split joined straight back gives a page its old segment ids again, whose
  // cached copy is still fresh from before the split: the page is fetched
  // anew, or the editor shows the row as it was before the split.
  it('fetches the pages on screen even when their key is cached and fresh', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    const { result } = renderHook(() => useEditorPages(7, rowsOf([1, 2]), [0, 1]), { wrapper })
    await waitFor(() => expect(result.current.has(2)).toBe(true))
    expect(getEditorRows).toHaveBeenCalledTimes(1)

    getEditorIndex.mockResolvedValue(indexOf([1, 2]))
    await refetchIndexWithPages(qc, 7)
    expect(getEditorRows).toHaveBeenCalledTimes(2)
  })

  // A page that fails to load fails the whole refresh, so the caller falls
  // back to invalidating the editor's rows instead of trusting the cache.
  it('rejects when a page on screen fails to load', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    const { result } = renderHook(() => useEditorPages(7, rowsOf([1, 2]), [0, 1]), { wrapper })
    await waitFor(() => expect(result.current.has(2)).toBe(true))

    getEditorIndex.mockResolvedValue(indexOf([1, 2]))
    getEditorRows.mockRejectedValueOnce(new Error('page failed'))
    await expect(refetchIndexWithPages(qc, 7)).rejects.toThrow('page failed')
  })
})
