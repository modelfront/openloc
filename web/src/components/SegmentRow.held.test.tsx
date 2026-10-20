// Another user has the segment open: the row is read-only, names them, and
// becomes editable when they leave. A save refused because of a hold shows
// the holder's message, not the version-conflict banner.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, screen, waitFor, within } from '@testing-library/react'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

const mockSaveDraft = vi.fn()
const mockUpdateHuman = vi.fn()
const mockPropagate = vi.fn()
vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    saveTranslationDraft: (...args: unknown[]) => mockSaveDraft(...args),
    updateTranslationHuman: (...args: unknown[]) => mockUpdateHuman(...args),
    propagateTranslation: (...args: unknown[]) => mockPropagate(...args),
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

const v1 = rowTranslation({ id: 801, active_text: 'Hallo Welt' })
const heldError = () => new ApiError(409, 'Ana is editing this segment. Try again when they have moved on.', {
  error: { code: 'segment_held', message: 'Ana is editing this segment. Try again when they have moved on.' },
  holder: { id: 2, name: 'Ana' },
})

beforeEach(() => {
  mockSaveDraft.mockReset()
  mockUpdateHuman.mockReset()
  mockPropagate.mockReset().mockResolvedValue({ propagated: 0, held: 0, siblings: [], taskReopened: false })
  vi.mocked(toast.error).mockClear()
  vi.mocked(toast.warning).mockClear()
})
afterEach(cleanup)

describe('SegmentRow — a segment another user holds', () => {
  it('is read-only and names the holder, then editable when they leave', async () => {
    const row = renderSegmentRow({ translation: v1, heldByName: 'Ana' })
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))

    expect(screen.getByTestId('segment-held-notice')).toHaveTextContent('Ana is editing this segment')
    const icon = screen.getByTestId('segment-held-icon')
    expect(icon).toHaveAttribute('tabindex', '0')
    expect(within(icon).getByLabelText('Ana is editing this segment')).toBeInTheDocument()
    expect(row.insertRef.current.replaceAll).toBeUndefined()
    expect(row.insertRef.current.confirm).toBeUndefined()

    row.rerender({ heldByName: null })
    await waitFor(() => expect(row.insertRef.current.replaceAll).toBeTypeOf('function'))
    expect(screen.queryByTestId('segment-held-notice')).toBeNull()
    act(() => row.insertRef.current.replaceAll!('Hallo, Welt'))
    await waitFor(() => expect(editorText()).toBe('Hallo, Welt'))
  })

  it('an inactive row shows only the icon', () => {
    renderSegmentRow({ translation: v1, heldByName: 'Ana', isActive: false })
    expect(screen.getByTestId('segment-held-icon')).toBeInTheDocument()
    expect(screen.queryByTestId('segment-held-notice')).toBeNull()
  })

  it('a confirm refused because of a hold toasts the holder, not the conflict banner', async () => {
    mockUpdateHuman.mockRejectedValueOnce(heldError())
    const row = renderSegmentRow({ translation: v1 })
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))
    act(() => row.insertRef.current.confirm!())
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Ana is editing this segment. Try again when they have moved on.'))
    expect(screen.queryByRole('button', { name: /Use mine/ })).toBeNull()
  })

  it('a confirm whose repetitions someone else has open says how many were skipped', async () => {
    mockUpdateHuman.mockResolvedValueOnce({ translation: { ...v1, status: 'approved' }, updatedTmIds: [], taskReopened: false })
    mockPropagate.mockResolvedValueOnce({ propagated: 1, held: 2, siblings: [], taskReopened: false })
    const row = renderSegmentRow({ translation: v1 })
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))
    act(() => row.insertRef.current.confirm!())
    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith('2 repetitions were skipped because someone else is editing them.'))
  })

  it('a draft refused because of a hold toasts the holder and keeps the typing', async () => {
    mockSaveDraft.mockRejectedValueOnce(heldError())
    const row = renderSegmentRow({ translation: v1 })
    await waitFor(() => expect(editorText()).toBe('Hallo Welt'))
    act(() => row.insertRef.current.replaceAll!('mine'))
    await waitFor(() => expect(editorText()).toBe('mine'))
    await act(() => row.flushDraftRef.current())
    expect(toast.error).toHaveBeenCalledWith('Ana is editing this segment. Try again when they have moved on.')
    expect(screen.queryByRole('button', { name: /Use mine/ })).toBeNull()
    expect(editorText()).toBe('mine')
  })
})
