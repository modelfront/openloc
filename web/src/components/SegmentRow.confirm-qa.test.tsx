// Cmd+Enter and the QA gate. The server re-runs QA on the text being confirmed
// and refuses with 422 when an error remains; the row's cached findings were
// computed on whatever text the last QA run saw, so they never decide a confirm.
//
// - A finding cached for an older text (Run QA flagged the empty target, then
//   the user typed a translation) must not stop the confirm request.
// - A real error still blocks: the server's 422 shows its findings in the
//   toast and the row does not advance.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, waitFor } from '@testing-library/react'

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

import { toast } from 'sonner'
import { ApiError } from '../api/request'
import { editorText, renderSegmentRow, rowTranslation } from '../test/segment-row'
import type { Translation } from '../api'
import type { QaFinding } from '../lib/qa-findings'

function translation(activeText: string): Translation {
  return rowTranslation({ active_text: activeText, status: activeText ? 'needs_review' : 'untranslated' })
}

function finding(checkType: string, message: string): QaFinding {
  return {
    id: 5, translation_id: 99, check_type: checkType, severity: 'error', message,
    created_at: '2026-10-01T09:00:00Z', ignored: false,
  }
}

const renderRow = (row: Translation, qaIssues: QaFinding[]) => renderSegmentRow({ translation: row, qaIssues })

beforeEach(() => {
  vi.mocked(toast.error).mockClear()
  mockUpdateHuman.mockReset().mockImplementation((_f: number, _id: number, text: string) =>
    Promise.resolve({
      translation: { ...translation(text), status: 'approved', updated_at: '2026-10-01T10:00:05Z' },
      updatedTmIds: [],
      taskReopened: false,
    }))
})
afterEach(cleanup)

describe('SegmentRow — Cmd+Enter with cached QA findings', () => {
  it('sends the confirm when the cached error was found on an older text', async () => {
    // Run QA flagged the empty target; the user then typed a translation.
    const { insertRef, onNavigate } = renderRow(translation(''), [finding('empty_target', 'Target segment is empty')])
    await waitFor(() => expect(insertRef.current.replaceAll).toBeTypeOf('function'))

    act(() => insertRef.current.replaceAll!('Hallo Welt'))
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))
    act(() => insertRef.current.confirm!())

    await waitFor(() => expect(mockUpdateHuman).toHaveBeenCalledTimes(1))
    expect(mockUpdateHuman.mock.calls[0].slice(0, 3)).toEqual([11, 99, 'Hallo Welt'])
    await waitFor(() => expect(onNavigate).toHaveBeenCalledWith('next'))
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('a real error still blocks: the server refuses and its findings fill the toast', async () => {
    mockUpdateHuman.mockReset().mockRejectedValue(new ApiError(422, 'fix QA errors before confirming', {
      error: { code: 'QAErrorsBlocked', message: 'fix QA errors before confirming' },
      qa_issues: [{ check_type: 'target_identical_to_source', severity: 'error', message: 'Target is identical to source (potentially untranslated)' }],
    }))
    const { insertRef, onNavigate } = renderRow(
      translation('Hello world'),
      [finding('target_identical_to_source', 'Target is identical to source (potentially untranslated)')],
    )
    await waitFor(() => expect(editorText()).toBe('Hello world'))

    act(() => insertRef.current.confirm!())

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(
      'Fix QA error before confirming: Target is identical to source (potentially untranslated)'))
    expect(mockUpdateHuman).toHaveBeenCalledTimes(1)
    expect(mockUpdateHuman.mock.calls[0].slice(0, 3)).toEqual([11, 99, 'Hello world'])
    expect(onNavigate).not.toHaveBeenCalled()
  })
})
