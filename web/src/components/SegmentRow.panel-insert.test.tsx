// An insert from the Resources panel (a TM match or MT suggestion, by click or
// Ctrl+1..5) saves the draft at once, with the inserted text and the insert
// token, so the server records the match and the Match column shows it.
// Replacing the text without a token (concordance) stays an ordinary edit
// that the autosave picks up later.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, waitFor } from '@testing-library/react'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

const mockSaveDraft = vi.fn()
vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    saveTranslationDraft: (...args: unknown[]) => mockSaveDraft(...args),
    lookupTerms: vi.fn().mockResolvedValue([]),
    runTranslationQA: vi.fn().mockResolvedValue([]),
  }
})
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

import { toast } from 'sonner'
import { ApiError } from '../api/request'
import { renderSegmentRow, rowTranslation } from '../test/segment-row'

const translation = rowTranslation({ edited_by: 3 })

beforeEach(() => {
  mockSaveDraft.mockReset().mockImplementation((_f: number, _id: number, text: string) =>
    Promise.resolve({
      translation: {
        ...translation, active_text: text, updated_at: '2026-10-01T10:00:05Z',
        origin: { translation_origin: 'tm', valid: true }, tm_match_percent: 92,
      },
      taskReopened: false,
    }))
})
afterEach(() => {
  cleanup()
  vi.mocked(toast.error).mockClear()
})

describe('SegmentRow — Resources panel insert', () => {
  it('saves the inserted text with its token straight away', async () => {
    const { insertRef } = renderSegmentRow({ translation })
    await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

    act(() => insertRef.current.replaceAll!('Hallo, Welt', 'tm-token'))

    await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1))
    const [fileId, id, text, , , expected, token] = mockSaveDraft.mock.calls[0]
    expect([fileId, id]).toEqual([11, 99])
    // The text just inserted, not the one the row held before the click.
    expect(text).toBe('Hallo, Welt')
    expect(expected).toBe(translation.updated_at)
    expect(token).toBe('tm-token')
  })

  it('saves even when the match equals the current text', async () => {
    // Nothing is dirty, but the insert still has a match to record.
    const { insertRef } = renderSegmentRow({ translation })
    await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

    act(() => insertRef.current.replaceAll!('Hallo Welt', 'mt-token'))

    await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1))
    expect(mockSaveDraft.mock.calls[0][6]).toBe('mt-token')
  })

  it('saves a tagged match as plain text plus its rich text, as typing does', async () => {
    // The plain text is what pre-translation and every typed save store, and
    // the edit distance of the next confirm is measured against it.
    const { insertRef } = renderSegmentRow({ translation })
    await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

    act(() => insertRef.current.replaceAll!('Hallo <ph id="1">{1}</ph>Welt', 'tm-token'))

    await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1))
    const [, , text, , rich] = mockSaveDraft.mock.calls[0]
    expect(text).not.toMatch(/<ph|<\/ph>/)
    expect(text).toContain('Welt')
    expect(rich).toContain('<ph id="1">')
  })

  it('says so when the inserted match is gone, and keeps the text as an ordinary edit', async () => {
    // The TM entry was deleted after the panel listed it: the server refuses
    // the insert, and the user must learn why its % did not land in Match.
    // The text stays dirty, so the autosave saves it without the token.
    mockSaveDraft.mockRejectedValueOnce(new ApiError(409, 'gone', {
      error: { code: 'suggestion_gone', message: 'the inserted suggestion is no longer available' },
    }))
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const { insertRef } = renderSegmentRow({ translation })
      await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

      act(() => insertRef.current.replaceAll!('Hallo, Welt', 'tm-token'))

      await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.stringMatching(/no longer in the translation memory/)))
      await act(async () => { vi.advanceTimersByTime(3000) })
      await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(2))
      expect(mockSaveDraft.mock.calls[1][2]).toBe('Hallo, Welt')
      expect(mockSaveDraft.mock.calls[1][6]).toBeUndefined()
    } finally {
      vi.useRealTimers()
    }
  })

  it('leaves a replace without a token to the autosave', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const { insertRef } = renderSegmentRow({ translation })
      await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

      act(() => insertRef.current.replaceAll!('Hallo, Welt'))
      await act(async () => { await Promise.resolve() })
      expect(mockSaveDraft).not.toHaveBeenCalled()

      await act(async () => { vi.advanceTimersByTime(3000) })
      await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1))
      expect(mockSaveDraft.mock.calls[0][6]).toBeUndefined()
    } finally {
      vi.useRealTimers()
    }
  })
})
