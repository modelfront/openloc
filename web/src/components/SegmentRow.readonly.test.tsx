// A read-only segment row writes nothing: not through the imperative handlers
// CATEditor's shortcuts and sidebar call (replaceAll, selectAndReplace), not
// through the draft flush that runs when the row is left, and not through
// Clear. The editable cases run the same calls first, so each read-only
// assertion is pinned against a path that demonstrably writes.
//
// Clear itself: always offered on an editable row. A saved translation is
// cleared on the server after a confirm; with nothing saved, only the editor
// is emptied.
//
// Anti-tautology RED proofs (each verified by editing SegmentRow.tsx):
//   - drop `effectiveReadOnly ? undefined :` from replaceAll → "exposes no
//     replaceAll" fails.
//   - drop `effectiveReadOnly ||` from selectAndReplace → the editor text
//     changes and "leaves the text alone" fails.
//   - drop `|| effectiveReadOnly` from saveDraft → the flush PUTs a draft.
//   - put `!isLocked` back in canClear → the Clear button renders.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

const mockSaveDraft = vi.fn()
const mockClear = vi.fn()
vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    saveTranslationDraft: (...args: unknown[]) => mockSaveDraft(...args),
    clearTranslation: (...args: unknown[]) => mockClear(...args),
    lookupTerms: vi.fn().mockResolvedValue([]),
    runTranslationQA: vi.fn().mockResolvedValue([]),
  }
})
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

import { editorText, renderSegmentRow, rowTranslation } from '../test/segment-row'
import type { Translation } from '../api'

const translation = rowTranslation({ edited_by: 3 })

// What a clear leaves behind, and what a row nobody translated looks like.
const untranslated: Translation = { ...translation, active_text: null, edited_by: null }

const renderRow = (readOnly: boolean, t: Translation = translation) =>
  renderSegmentRow({ translation: t, readOnly, isAdmin: true, inFirstStep: true })

beforeEach(() => {
  mockSaveDraft.mockReset().mockImplementation((_f: number, _id: number, text: string) =>
    Promise.resolve({ translation: { ...translation, active_text: text, updated_at: '2026-10-01T10:00:05Z' }, taskReopened: false }))
  mockClear.mockReset().mockResolvedValue({ translation: untranslated, taskReopened: false })
})
afterEach(cleanup)

describe('SegmentRow — editable (control)', () => {
  it('replaceAll puts text in the editor, and leaving the row saves it as a draft', async () => {
    const { insertRef, flushDraftRef } = renderRow(false)
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))

    act(() => insertRef.current.replaceAll!('Neuer Text'))
    await waitFor(() => expect(editorText()).toBe('Neuer Text'))
    await act(() => flushDraftRef.current())

    expect(mockSaveDraft).toHaveBeenCalledTimes(1)
    expect(mockSaveDraft.mock.calls[0].slice(0, 3)).toEqual([11, 99, 'Neuer Text'])
  })

  it('a term click writes into the editor', async () => {
    const { insertRef } = renderRow(false)
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))
    act(() => insertRef.current.selectAndReplace('world', 'Erde'))
    await waitFor(() => expect(editorText()).toContain('Erde'))
  })

  it('offers Clear to an admin in the first step', async () => {
    const { insertRef } = renderRow(false)
    expect(await screen.findByTestId('clear-translation-btn')).toBeInTheDocument()
    expect(insertRef.current.clear).toBeTypeOf('function')
  })
})

describe('SegmentRow — Clear', () => {
  it('stays on a row with no saved translation, so a cleared row can be cleared again', async () => {
    const { insertRef } = renderRow(false, untranslated)
    expect(await screen.findByTestId('clear-translation-btn')).toBeInTheDocument()
    expect(insertRef.current.clear).toBeTypeOf('function')
  })

  it('with nothing saved, empties the editor without asking and without a server call', async () => {
    const { insertRef, flushDraftRef } = renderRow(false, untranslated)
    act(() => insertRef.current.replaceAll!('Entwurf'))
    await waitFor(() => expect(editorText()).toBe('Entwurf'))

    fireEvent.click(await screen.findByTestId('clear-translation-btn'))

    await waitFor(() => expect(editorText()).toBe(''))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(mockClear).not.toHaveBeenCalled()
    // Nothing is left to save when the row is left.
    await act(() => flushDraftRef.current())
    expect(mockSaveDraft).not.toHaveBeenCalled()
  })

  it('with a saved translation, asks first and clears it on the server', async () => {
    renderRow(false)
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))

    fireEvent.click(screen.getByTestId('clear-translation-btn'))
    fireEvent.click(await screen.findByRole('button', { name: 'Clear' }))

    await waitFor(() => expect(mockClear).toHaveBeenCalledWith(11, 99))
  })
})

describe('SegmentRow — read-only', () => {
  it('exposes no replaceAll, and a term click leaves the text alone', async () => {
    const { insertRef } = renderRow(true)
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))

    expect(insertRef.current.replaceAll).toBeUndefined()
    expect(insertRef.current.confirm).toBeUndefined()
    expect(insertRef.current.copySource).toBeUndefined()
    act(() => insertRef.current.selectAndReplace('world', 'Erde'))
    expect(editorText()).toBe('Hallo Welt')
  })

  it('sends no draft when the row is left, even with an edit pending', async () => {
    const { flushDraftRef, dirtyRef } = renderRow(true)
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))

    dirtyRef.current = true
    await act(() => flushDraftRef.current())

    expect(mockSaveDraft).not.toHaveBeenCalled()
  })

  it('offers no Clear, by button or shortcut', async () => {
    const { insertRef } = renderRow(true)
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))
    expect(screen.queryByTestId('clear-translation-btn')).toBeNull()
    expect(insertRef.current.clear).toBeUndefined()
  })
})
