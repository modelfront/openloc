// A file whose strings are plain text ("Strings contain HTML" off): the editor
// loads, shows and saves its tags as typed, never as formatting or chips.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, screen, waitFor } from '@testing-library/react'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

const mockUpdateHuman = vi.fn()
vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    updateTranslationHuman: (...args: unknown[]) => mockUpdateHuman(...args),
    saveTranslationDraft: vi.fn(),
    propagateTranslation: vi.fn().mockResolvedValue({ propagated: 0, siblings: [], taskReopened: false }),
    clearTranslation: vi.fn(),
    lookupTerms: vi.fn().mockResolvedValue([]),
    runTranslationQA: vi.fn().mockResolvedValue([]),
  }
})
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

import { editorText, renderSegmentRow, rowTranslation } from '../test/segment-row'

beforeEach(() => {
  mockUpdateHuman.mockReset().mockImplementation((_f: number, _id: number, text: string) =>
    Promise.resolve({
      translation: rowTranslation({ active_text: text, status: 'approved', updated_at: '2026-10-01T10:00:05Z' }),
      updatedTmIds: [],
      taskReopened: false,
    }))
})
afterEach(cleanup)

// Loads a translation of "<strong>x</strong>", confirms it unchanged, and
// returns what the editor showed and what the save sent.
async function loadAndConfirm(plainText: boolean): Promise<{ shown: string | null; saved: string }> {
  const { insertRef } = renderSegmentRow({ translation: rowTranslation({ active_text: '<strong>x</strong>' }), plainText })
  await waitFor(() => expect(insertRef.current.confirm).toBeTypeOf('function'))
  const shown = editorText()
  act(() => insertRef.current.confirm!())
  await waitFor(() => expect(mockUpdateHuman).toHaveBeenCalledTimes(1))
  return { shown, saved: mockUpdateHuman.mock.calls[0][2] as string }
}

describe('SegmentRow — a plain-text file', () => {
  it('loads tags as typed and saves them back literally', async () => {
    expect(await loadAndConfirm(true)).toEqual({ shown: '<strong>x</strong>', saved: '<strong>x</strong>' })
  })

  it('an HTML file loads the same string as formatting', async () => {
    expect(await loadAndConfirm(false)).toEqual({ shown: 'x', saved: '<strong>x</strong>' })
  })

  it('an inactive row shows the tags as text', () => {
    renderSegmentRow({ translation: rowTranslation({ active_text: '<strong>x</strong>' }), isActive: false, plainText: true })
    expect(screen.getByText('<strong>x</strong>')).toBeInTheDocument()
  })

  it('copies the plain source, not its tags', async () => {
    const { insertRef } = renderSegmentRow({
      translation: rowTranslation({ active_text: '' }), plainText: true,
      segment: { source_rich_text: '<g id="1">Hello</g> world' },
    })
    await waitFor(() => expect(insertRef.current.copySource).toBeTypeOf('function'))
    act(() => insertRef.current.copySource!())
    await waitFor(() => expect(editorText()).toBe('Hello world'))
  })
})
