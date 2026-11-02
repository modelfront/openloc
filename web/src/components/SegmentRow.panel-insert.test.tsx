// An insert from the Resources panel (a TM match or MT suggestion, by click or
// Ctrl+1..5) saves the draft at once, with the inserted text and the insert
// token, so the server records the match and the Match column shows it.
// Replacing the text without a token (concordance) stays an ordinary edit
// that the autosave picks up later. An exact TM match is then confirmed, as
// Cmd+Enter does, without moving to the next segment.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, waitFor } from '@testing-library/react'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

const mockSaveDraft = vi.fn()
const mockUpdateHuman = vi.fn()
vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    saveTranslationDraft: (...args: unknown[]) => mockSaveDraft(...args),
    updateTranslationHuman: (...args: unknown[]) => mockUpdateHuman(...args),
    propagateTranslation: vi.fn().mockResolvedValue({ propagated: 0, siblings: [], taskReopened: false }),
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
  mockUpdateHuman.mockReset().mockImplementation((_f: number, _id: number, text: string) =>
    Promise.resolve({
      translation: { ...translation, active_text: text, status: 'approved', updated_at: '2026-10-01T10:00:09Z' },
      updatedTmIds: [],
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

describe('SegmentRow — approving an exact TM match on insert', () => {
  it('saves the draft with its token, then confirms the inserted text and stays on the segment', async () => {
    const { insertRef, onNavigate } = renderSegmentRow({ translation })
    await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

    act(() => insertRef.current.replaceAll!('Hallo <ph id="1">{1}</ph>Welt', 'tm-token', true))

    await waitFor(() => expect(mockUpdateHuman).toHaveBeenCalledTimes(1))
    expect(mockSaveDraft).toHaveBeenCalledTimes(1)
    expect(mockSaveDraft.mock.invocationCallOrder[0]).toBeLessThan(mockUpdateHuman.mock.invocationCallOrder[0])
    const [, , draftText, , draftRich] = mockSaveDraft.mock.calls[0]
    const [fileId, id, text, , rich, expected] = mockUpdateHuman.mock.calls[0]
    expect([fileId, id]).toEqual([11, 99])
    // The match just inserted, not the text the row held before the click.
    expect(text).toBe(draftText)
    expect(rich).toBe(draftRich)
    expect(rich).toContain('<ph id="1">')
    // Against the version the draft left, so the confirm does not 409.
    expect(expected).toBe('2026-10-01T10:00:05Z')
    expect(onNavigate).not.toHaveBeenCalled()
  })

  it('does not confirm an insert it was not asked to approve', async () => {
    const { insertRef } = renderSegmentRow({ translation })
    await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

    act(() => insertRef.current.replaceAll!('Hallo, Welt', 'tm-token', false))

    await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1))
    await act(async () => { await Promise.resolve() })
    expect(mockUpdateHuman).not.toHaveBeenCalled()
  })

  it('does not confirm when the draft was refused', async () => {
    mockSaveDraft.mockRejectedValueOnce(new ApiError(409, 'gone', {
      error: { code: 'suggestion_gone', message: 'the inserted suggestion is no longer available' },
    }))
    const { insertRef } = renderSegmentRow({ translation })
    await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

    act(() => insertRef.current.replaceAll!('Hallo, Welt', 'tm-token', true))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    await act(async () => { await Promise.resolve() })
    expect(mockUpdateHuman).not.toHaveBeenCalled()
  })

  it('does not approve a match the user replaced before its draft was saved', async () => {
    // The first draft is slow; meanwhile a fuzzy match replaces the text.
    let release!: () => void
    const firstDraft = mockSaveDraft.getMockImplementation()!
    mockSaveDraft.mockImplementationOnce((...args: Parameters<typeof firstDraft>) =>
      new Promise(resolve => { release = () => resolve(firstDraft(...args)) }))
    const { insertRef } = renderSegmentRow({ translation })
    await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

    act(() => insertRef.current.replaceAll!('Hallo, Welt', 'exact-token', true))
    await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1))
    act(() => insertRef.current.replaceAll!('Hallo Welt, ungefähr', 'fuzzy-token', false))
    await act(async () => { release() })

    await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(2))
    await act(async () => { await Promise.resolve() })
    expect(mockSaveDraft.mock.calls[1][6]).toBe('fuzzy-token')
    expect(mockUpdateHuman).not.toHaveBeenCalled()
  })

  it('does not approve a match whose row was closed before its draft was saved', async () => {
    // With the row closed there is no editor to read the tags from, so a
    // confirm would approve the text without them.
    let release!: () => void
    const draft = mockSaveDraft.getMockImplementation()!
    mockSaveDraft.mockImplementationOnce((...args: Parameters<typeof draft>) =>
      new Promise(resolve => { release = () => resolve(draft(...args)) }))
    const { insertRef, rerender } = renderSegmentRow({ translation })
    await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

    act(() => insertRef.current.replaceAll!('Hallo <ph id="1">{1}</ph>Welt', 'exact-token', true))
    await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1))
    rerender({ isActive: false })
    await act(async () => { release() })

    await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1))
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect(mockUpdateHuman).not.toHaveBeenCalled()
  })
})

// Cmd+Enter pressed while an exact match's draft is saving queues behind it;
// the insert's own approve then starts while that confirm is still saving.
describe('SegmentRow — Cmd+Enter around an exact-match insert', () => {
  it('a confirm the insert approve overtakes still moves on, and the next Cmd+Enter saves', async () => {
    let releaseDraft!: () => void
    const draft = mockSaveDraft.getMockImplementation()!
    mockSaveDraft.mockImplementationOnce((...args: Parameters<typeof draft>) =>
      new Promise(resolve => { releaseDraft = () => resolve(draft(...args)) }))
    const puts: Array<() => void> = []
    const put = mockUpdateHuman.getMockImplementation()!
    mockUpdateHuman.mockImplementation((...args: Parameters<typeof put>) =>
      new Promise(resolve => { puts.push(() => resolve(put(...args))) }))
    const { insertRef, onNavigate } = renderSegmentRow({ translation })
    await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

    act(() => insertRef.current.replaceAll!('Hallo, Welt', 'exact-token', true))
    await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1))
    act(() => insertRef.current.confirm!())
    await act(async () => { releaseDraft() })
    await waitFor(() => expect(puts).toHaveLength(1))
    await act(async () => { puts[0]() })
    await waitFor(() => expect(puts).toHaveLength(2))
    await act(async () => { puts[1]() })
    await waitFor(() => expect(onNavigate).toHaveBeenCalledWith('next'))

    act(() => insertRef.current.confirm!())
    await waitFor(() => expect(mockUpdateHuman).toHaveBeenCalledTimes(3))
  })
})
