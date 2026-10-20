// Editing time: it counts only while the segment is active, adds up across
// visits, and is sent once, with the confirm. A draft save sends 0. The time of
// a visit survives the row unmounting when it scrolls out of view.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, screen, waitFor } from '@testing-library/react'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

const mockUpdateHuman = vi.fn()
const mockSaveDraft = vi.fn()
vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    updateTranslationHuman: (...args: unknown[]) => mockUpdateHuman(...args),
    saveTranslationDraft: (...args: unknown[]) => mockSaveDraft(...args),
    propagateTranslation: vi.fn().mockResolvedValue({ propagated: 0, siblings: [], taskReopened: false }),
    clearTranslation: vi.fn(),
    lookupTerms: vi.fn().mockResolvedValue([]),
    runTranslationQA: vi.fn().mockResolvedValue([]),
  }
})
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

import { toast } from 'sonner'
import { ApiError } from '../api/request'
import { editorText, renderSegmentRow, rowTranslation } from '../test/segment-row'

const qaBlocked = () => new ApiError(422, 'fix QA errors before confirming', {
  error: { code: 'QAErrorsBlocked', message: 'fix QA errors before confirming' },
  qa_issues: [{ check_type: 'target_identical_to_source', severity: 'error', message: 'Target is identical to source (potentially untranslated)' }],
})

const confirmed = (_f: number, _id: number, text: string) => Promise.resolve({
  translation: rowTranslation({ active_text: text, status: 'approved', updated_at: '2026-10-01T10:00:05Z' }),
  updatedTmIds: [],
  taskReopened: false,
})

let now = 1_000_000
beforeEach(() => {
  now = 1_000_000
  vi.spyOn(Date, 'now').mockImplementation(() => now)
  vi.mocked(toast.error).mockClear()
  mockUpdateHuman.mockReset().mockImplementation(confirmed)
  mockSaveDraft.mockReset().mockImplementation((_f: number, _id: number, text: string) => Promise.resolve({
    translation: rowTranslation({ active_text: text, updated_at: '2026-10-01T10:00:04Z' }),
    taskReopened: false,
  }))
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('SegmentRow — editing time', () => {
  it('adds up visits across an unmount; the draft sends 0 and the confirm sends the sum', async () => {
    // Visit 1: 30 s active, then the user moves on and the row scrolls away.
    const first = renderSegmentRow({ translation: rowTranslation({ id: 501 }) })
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))
    now += 30_000
    first.rerender({ isActive: false })
    now += 120_000 // on other segments: not counted
    first.unmount()
    now += 60_000 // still away, the row is not mounted: not counted

    // Visit 2: the row mounts again, 60 s active, a draft save, then the confirm.
    const second = renderSegmentRow({ translation: rowTranslation({ id: 501 }), isActive: false })
    second.rerender({ isActive: true })
    await waitFor(() => expect(second.insertRef.current.replaceAll).toBeTypeOf('function'))
    now += 20_000
    act(() => second.insertRef.current.replaceAll!('Hallo Welt!'))
    await waitFor(() => expect(editorText()).toBe('Hallo Welt!'))
    await act(() => second.flushDraftRef.current())
    expect(mockSaveDraft).toHaveBeenCalledTimes(1)
    expect(mockSaveDraft.mock.calls[0][3]).toBe(0)

    now += 40_000
    act(() => second.insertRef.current.confirm!())
    await waitFor(() => expect(mockUpdateHuman).toHaveBeenCalledTimes(1))
    expect(mockUpdateHuman.mock.calls[0][3]).toBe(90_000)
  })

  it('a refused confirm keeps its editing time for the next confirm', async () => {
    mockUpdateHuman.mockReset().mockRejectedValueOnce(qaBlocked()).mockImplementation(confirmed)
    const { insertRef, onNavigate } = renderSegmentRow({ translation: rowTranslation({ id: 502, active_text: 'Hello world' }) })
    await waitFor(() => expect(editorText()).toBe('Hello world'))

    now += 5_000
    act(() => insertRef.current.confirm!())
    await waitFor(() => expect(toast.error).toHaveBeenCalled())

    now += 3_000
    act(() => insertRef.current.confirm!())
    await waitFor(() => expect(onNavigate).toHaveBeenCalledWith('next'))

    expect(mockUpdateHuman).toHaveBeenCalledTimes(2)
    expect(mockUpdateHuman.mock.calls[0][3]).toBe(5_000)
    expect(mockUpdateHuman.mock.calls[1][3]).toBe(8_000)
  })

  it('"Use mine" after a conflict reports the editing time the refused confirm held', async () => {
    mockUpdateHuman.mockReset().mockRejectedValueOnce(new ApiError(409, 'conflict', {
      error: { code: 'Conflict', message: 'conflict' },
      conflict: {
        id: 503, active_text: 'Hallo Welt (theirs)', active_rich_text: null,
        updated_at: '2026-10-01T10:00:03Z', edited_by: 2, edited_by_name: 'Other',
      },
    })).mockImplementation(confirmed)
    const { insertRef } = renderSegmentRow({ translation: rowTranslation({ id: 503, active_text: 'Hello world' }) })
    await waitFor(() => expect(editorText()).toBe('Hello world'))

    now += 5_000
    act(() => insertRef.current.confirm!())
    const useMine = await screen.findByRole('button', { name: /Use mine/ })

    now += 2_000
    act(() => useMine.click())
    await waitFor(() => expect(mockUpdateHuman).toHaveBeenCalledTimes(2))
    expect(mockUpdateHuman.mock.calls[0][3]).toBe(5_000)
    expect(mockUpdateHuman.mock.calls[1][3]).toBe(7_000)
  })
})
