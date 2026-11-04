// A confirm writes one row, and its response is patched into the loaded page,
// the index and the leverage bar, so none of them is refetched. The small
// aggregates (the job's counts) still are.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, waitFor } from '@testing-library/react'
import type { QueryClient } from '@tanstack/react-query'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

const mockUpdateHuman = vi.fn()
const mockPropagate = vi.fn()
vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    updateTranslationHuman: (...args: unknown[]) => mockUpdateHuman(...args),
    saveTranslationDraft: vi.fn(),
    propagateTranslation: (...args: unknown[]) => mockPropagate(...args),
    clearTranslation: vi.fn(),
    lookupTerms: vi.fn().mockResolvedValue([]),
    runTranslationQA: vi.fn().mockResolvedValue([]),
  }
})
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

import { renderSegmentRow, rowTranslation } from '../test/segment-row'
import { queryKeys } from '../lib/query-keys'
import type { EditorRow } from '../api'
import { indexLookup, type EditorIndexData, type IndexRow } from '../lib/editor-index'
import type { JobLeverage, LeverageBand } from '../api/analyses'

const JOB_ID = 7 // renderSegmentRow's job
const PROJECT_ID = 3

const ZERO: LeverageBand = {
  segments: 0, words: 0, characters: 0,
  segments_auto_approved: 0, words_auto_approved: 0, characters_auto_approved: 0,
  segments_needs_review: 0, words_needs_review: 0, characters_needs_review: 0,
}
// The row and its repetition, both new and needing review: two segments of two words, 11 characters.
const LEVERAGE: JobLeverage = {
  job_id: JOB_ID, target_language: 'de',
  tm_101: ZERO, tm_100: ZERO, tm_85_99: ZERO, tm_65_84: ZERO, repetitions: ZERO,
  new: { ...ZERO, segments: 2, words: 4, characters: 22, segments_needs_review: 2, words_needs_review: 4, characters_needs_review: 22 },
}
const needsReview = (qc: QueryClient) => qc.getQueryData<JobLeverage>(queryKeys.job.leverage(JOB_ID))!.new.segments_needs_review

beforeEach(() => {
  mockUpdateHuman.mockReset().mockImplementation((_f: number, _id: number, text: string) =>
    Promise.resolve({
      translation: rowTranslation({ active_text: text, status: 'approved', updated_at: '2026-10-01T10:00:05Z' }),
      updatedTmIds: [],
      taskReopened: false,
    }))
  mockPropagate.mockReset().mockResolvedValue({ propagated: 0, siblings: [], held: 0, taskReopened: false })
})
afterEach(cleanup)

async function confirmRow(propagated = 0) {
  if (propagated > 0) {
    mockPropagate.mockResolvedValue({
      propagated, siblings: [{ id: 100, segment_id: 100, status: 'approved', active_text: 'Hallo Welt', updated_at: '2026-10-01T10:00:06Z' }], held: 0, taskReopened: false,
    })
  }
  const r = renderSegmentRow({ translation: rowTranslation(), projectId: PROJECT_ID })
  const qc = r.queryClient
  const tr = rowTranslation()
  qc.setQueryData<EditorRow[]>(queryKeys.job.editorPage(JOB_ID, 0, String(tr.segment_id)), [{ segment: { id: tr.segment_id } as EditorRow['segment'], translation: tr, previous_step_text: null, placeables: [] }])
  const indexRows = [
    { segmentId: tr.segment_id, translationId: tr.id, status: tr.status, origin: '', tmMatch: null },
    { segmentId: 100, translationId: 100, status: 'needs_review', origin: '', tmMatch: null },
  ] as IndexRow[]
  qc.setQueryData(queryKeys.job.editorIndex(JOB_ID), { rows: indexRows, repetitionStats: { segments: 0, words: 0, characters: 0 }, lookup: indexLookup(indexRows), files: [] })
  qc.setQueryData(queryKeys.job.leverage(JOB_ID), LEVERAGE)
  qc.setQueryData(queryKeys.job.detail(JOB_ID), { cached: true })
  await waitFor(() => expect(r.insertRef.current.confirm).toBeTypeOf('function'))
  act(() => r.insertRef.current.confirm!())
  await waitFor(() => expect(r.onNavigate).toHaveBeenCalledWith('next'))
  await waitFor(() => expect(mockPropagate).toHaveBeenCalled())
  return { qc, tr }
}

describe('SegmentRow — confirm keeps the editor caches', () => {
  it('a confirm patches the page, the index and the leverage bar and refetches none of them', async () => {
    const { qc, tr } = await confirmRow()
    await waitFor(() => expect(qc.getQueryState(queryKeys.job.detail(JOB_ID))?.isInvalidated).toBe(true))
    expect(needsReview(qc)).toBe(1)
    expect(qc.getQueryState(queryKeys.job.leverage(JOB_ID))?.isInvalidated).toBe(false)
    expect(qc.getQueryState(queryKeys.job.editorIndex(JOB_ID))?.isInvalidated).toBe(false)
    expect(qc.getQueryState(queryKeys.job.editorPage(JOB_ID, 0, String(tr.segment_id)))?.isInvalidated).toBe(false)
    expect(qc.getQueryData<EditorRow[]>(queryKeys.job.editorPage(JOB_ID, 0, String(tr.segment_id)))?.[0].translation?.status).toBe('approved')
    expect(qc.getQueryData<EditorIndexData>(queryKeys.job.editorIndex(JOB_ID))?.rows[0].status).toBe('approved')
  })

  it('a confirm that propagates to repetitions does not refetch them either', async () => {
    const { qc, tr } = await confirmRow(1)
    await waitFor(() => expect(qc.getQueryData<EditorIndexData>(queryKeys.job.editorIndex(JOB_ID))?.rows[1].status).toBe('approved'))
    expect(needsReview(qc)).toBe(0)
    expect(qc.getQueryState(queryKeys.job.leverage(JOB_ID))?.isInvalidated).toBe(false)
    expect(qc.getQueryState(queryKeys.job.editorIndex(JOB_ID))?.isInvalidated).toBe(false)
    expect(qc.getQueryState(queryKeys.job.editorPage(JOB_ID, 0, String(tr.segment_id)))?.isInvalidated).toBe(false)
  })
})

// Cmd+Enter pressed again while the confirm is still saving belongs to the
// same row: it must not queue another save of it.
describe('SegmentRow — a repeated confirm while one is saving', () => {
  it('saves the row once and moves on once', async () => {
    let finish!: () => void
    mockUpdateHuman.mockImplementation((_f: number, _id: number, text: string) => new Promise(resolve => {
      finish = () => resolve({
        translation: rowTranslation({ active_text: text, status: 'approved', updated_at: '2026-10-01T10:00:05Z' }),
        updatedTmIds: [], taskReopened: false,
      })
    }))
    const r = renderSegmentRow({ translation: rowTranslation(), projectId: PROJECT_ID })
    await waitFor(() => expect(r.insertRef.current.confirm).toBeTypeOf('function'))

    act(() => r.insertRef.current.confirm!())
    await waitFor(() => expect(mockUpdateHuman).toHaveBeenCalledTimes(1))
    act(() => r.insertRef.current.confirm!())
    act(() => r.insertRef.current.confirm!())
    await act(async () => finish())

    await waitFor(() => expect(r.onNavigate).toHaveBeenCalledWith('next'))
    await waitFor(() => expect(mockPropagate).toHaveBeenCalled())
    expect(mockUpdateHuman).toHaveBeenCalledTimes(1)
    expect(r.onNavigate).toHaveBeenCalledTimes(1)
  })
})


// The user left the row (Cmd+G, a find result) while its confirm was saving:
// the editor stays where they went.
describe('SegmentRow — a confirm that lands after the row was left', () => {
  it('does not move on', async () => {
    let finish!: () => void
    mockUpdateHuman.mockImplementation((_f: number, _id: number, text: string) => new Promise(resolve => {
      finish = () => resolve({
        translation: rowTranslation({ active_text: text, status: 'approved', updated_at: '2026-10-01T10:00:05Z' }),
        updatedTmIds: [], taskReopened: false,
      })
    }))
    const r = renderSegmentRow({ translation: rowTranslation(), projectId: PROJECT_ID })
    await waitFor(() => expect(r.insertRef.current.confirm).toBeTypeOf('function'))

    act(() => r.insertRef.current.confirm!())
    await waitFor(() => expect(mockUpdateHuman).toHaveBeenCalledTimes(1))
    r.rerender({ isActive: false })
    await act(async () => finish())

    await waitFor(() => expect(mockPropagate).toHaveBeenCalled())
    expect(r.onNavigate).not.toHaveBeenCalled()
  })
})
