// The active row and a server change. While the user has unsaved typing, a
// newer version of the row arriving in the cache (a refetch, another user's
// save, the echo of the row's own autosave) never replaces what is in the
// editor, and the next save still names the version the typing started from,
// so it meets the optimistic lock. A row with no unsaved typing, or a
// read-only one, follows the server. Use theirs and Clear load their own
// version.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

const mockSaveDraft = vi.fn()
const mockUpdateHuman = vi.fn()
const mockClear = vi.fn()
vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    saveTranslationDraft: (...args: unknown[]) => mockSaveDraft(...args),
    updateTranslationHuman: (...args: unknown[]) => mockUpdateHuman(...args),
    propagateTranslation: vi.fn().mockResolvedValue({ propagated: 0, siblings: [], taskReopened: false }),
    clearTranslation: (...args: unknown[]) => mockClear(...args),
    lookupTerms: vi.fn().mockResolvedValue([]),
    runTranslationQA: vi.fn().mockResolvedValue([]),
  }
})
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

import { ApiError } from '../api/request'
import { editorText, renderSegmentRow, rowTranslation } from '../test/segment-row'
import type { Translation } from '../api'

const v1 = rowTranslation({ id: 701, active_text: 'first approval', status: 'approved', updated_at: '2026-10-01T10:00:00Z' })
const theirs = { ...v1, active_text: 'their text', updated_at: '2026-10-01T10:00:09Z' }

// The text and the optimistic-lock baseline of the nth draft save.
const draftSent = (n: number) => ({ text: mockSaveDraft.mock.calls[n][2], baseline: mockSaveDraft.mock.calls[n][5] })

beforeEach(() => {
  mockSaveDraft.mockReset().mockImplementation((_f: number, _id: number, text: string) => Promise.resolve({
    translation: { ...v1, active_text: text, status: 'needs_review', updated_at: '2026-10-01T10:00:20Z' },
    taskReopened: false,
  }))
  mockUpdateHuman.mockReset()
  mockClear.mockReset()
})
afterEach(cleanup)

describe('SegmentRow — a server change while the row is active', () => {
  it('keeps unsaved typing, and its baseline, when a newer version of the row arrives', async () => {
    const row = renderSegmentRow({ translation: v1 })
    await waitFor(() => expect(editorText()).toBe('first approval'))
    act(() => row.insertRef.current.replaceAll!('second edit'))
    await waitFor(() => expect(editorText()).toBe('second edit'))

    row.rerender({ translation: theirs })

    expect(editorText()).toBe('second edit')
    await act(() => row.flushDraftRef.current())
    expect(draftSent(0)).toEqual({ text: 'second edit', baseline: v1.updated_at })
  })

  it('keeps typing done while the autosave was in flight', async () => {
    let resolveSave: (v: { translation: Translation; taskReopened: boolean }) => void = () => {}
    mockSaveDraft.mockImplementationOnce(() => new Promise(r => { resolveSave = r }))
    const row = renderSegmentRow({ translation: v1 })
    await waitFor(() => expect(editorText()).toBe('first approval'))

    act(() => row.insertRef.current.replaceAll!('second'))
    await waitFor(() => expect(editorText()).toBe('second'))
    let flushed: Promise<void> = Promise.resolve()
    act(() => { flushed = row.flushDraftRef.current() })
    await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1))
    act(() => row.insertRef.current.replaceAll!('second edit'))
    await waitFor(() => expect(editorText()).toBe('second edit'))

    // The save answers with the text it sent; the cache echoes it to the row.
    const saved = { ...v1, active_text: 'second', status: 'needs_review' as const, updated_at: '2026-10-01T10:00:05Z' }
    await act(async () => { resolveSave({ translation: saved, taskReopened: false }); await flushed })
    row.rerender({ translation: saved })

    expect(editorText()).toBe('second edit')
    await act(() => row.flushDraftRef.current())
    expect(draftSent(1)).toEqual({ text: 'second edit', baseline: saved.updated_at })
  })

  it('follows the server when the editor has no unsaved typing', async () => {
    const row = renderSegmentRow({ translation: v1 })
    await waitFor(() => expect(editorText()).toBe('first approval'))

    row.rerender({ translation: theirs })

    await waitFor(() => expect(editorText()).toBe('their text'))
  })

  it('keeps unsaved typing when the row turns read-only with nothing new', async () => {
    const row = renderSegmentRow({ translation: v1 })
    await waitFor(() => expect(editorText()).toBe('first approval'))
    act(() => row.insertRef.current.replaceAll!('second edit'))
    await waitFor(() => expect(editorText()).toBe('second edit'))

    row.rerender({ readOnly: true })

    expect(editorText()).toBe('second edit')
  })

  it('a read-only row follows a newer version even over typing it can no longer save', async () => {
    const row = renderSegmentRow({ translation: v1 })
    await waitFor(() => expect(editorText()).toBe('first approval'))
    act(() => row.insertRef.current.replaceAll!('second edit'))
    await waitFor(() => expect(editorText()).toBe('second edit'))
    row.rerender({ readOnly: true })

    row.rerender({ translation: theirs })

    await waitFor(() => expect(editorText()).toBe('their text'))
  })

  it('Use theirs loads the version the banner shows, and the next save names it', async () => {
    const conflict = {
      id: 701, active_text: 'banner text', active_rich_text: null,
      updated_at: '2026-10-01T10:00:03Z', edited_by: 2, edited_by_name: 'Other',
    }
    mockUpdateHuman.mockRejectedValueOnce(new ApiError(409, 'conflict', {
      error: { code: 'Conflict', message: 'conflict' }, conflict,
    }))
    const row = renderSegmentRow({ translation: v1 })
    await waitFor(() => expect(editorText()).toBe('first approval'))
    act(() => row.insertRef.current.replaceAll!('mine'))
    await waitFor(() => expect(editorText()).toBe('mine'))
    act(() => row.insertRef.current.confirm!())

    const useTheirs = await screen.findByRole('button', { name: /Use theirs/ })
    act(() => useTheirs.click())
    await waitFor(() => expect(editorText()).toBe('banner text'))

    act(() => row.insertRef.current.replaceAll!('mine again'))
    await waitFor(() => expect(editorText()).toBe('mine again'))
    await act(() => row.flushDraftRef.current())
    expect(draftSent(0)).toEqual({ text: 'mine again', baseline: conflict.updated_at })
  })

  it('the Clear dialog clears the row, unsaved typing included, and the next save names the cleared version', async () => {
    const cleared = { ...v1, active_text: null, status: 'needs_review' as const, edited_by: null, updated_at: '2026-10-01T10:00:07Z' }
    mockClear.mockResolvedValue({ translation: cleared, taskReopened: false })
    const row = renderSegmentRow({ translation: v1, isAdmin: true, inFirstStep: true })
    await waitFor(() => expect(editorText()).toBe('first approval'))
    act(() => row.insertRef.current.replaceAll!('unsaved'))
    await waitFor(() => expect(editorText()).toBe('unsaved'))

    fireEvent.click(screen.getByTestId('clear-translation-btn'))
    fireEvent.click(await screen.findByRole('button', { name: 'Clear' }))
    await waitFor(() => expect(editorText()).toBe(''))
    expect(mockClear).toHaveBeenCalledTimes(1)

    act(() => row.insertRef.current.replaceAll!('neu'))
    await waitFor(() => expect(editorText()).toBe('neu'))
    await act(() => row.flushDraftRef.current())
    expect(draftSent(0)).toEqual({ text: 'neu', baseline: cleared.updated_at })
  })

  it('an emptied target clears the row and the next save names the cleared version', async () => {
    const cleared = { ...v1, active_text: null, status: 'needs_review' as const, edited_by: null, updated_at: '2026-10-01T10:00:07Z' }
    mockClear.mockResolvedValue({ translation: cleared, taskReopened: false })
    const row = renderSegmentRow({ translation: v1, isAdmin: true, inFirstStep: true })
    await waitFor(() => expect(editorText()).toBe('first approval'))

    act(() => row.insertRef.current.replaceAll!(''))
    await act(() => row.flushDraftRef.current())
    expect(mockClear).toHaveBeenCalledTimes(1)
    expect(mockSaveDraft).not.toHaveBeenCalled()

    act(() => row.insertRef.current.replaceAll!('neu'))
    await waitFor(() => expect(editorText()).toBe('neu'))
    await act(() => row.flushDraftRef.current())
    expect(draftSent(0)).toEqual({ text: 'neu', baseline: cleared.updated_at })
  })
})
