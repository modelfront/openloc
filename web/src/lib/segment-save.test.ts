import { describe, it, expect, vi } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { applyAcquiredRow, applySegmentSaveSuccess, leverageFacts, optimisticAdjustLeverage, patchPropagatedSiblings } from './segment-save'
import { queryKeys } from './query-keys'
import type { EditorId } from './editor-scope'
import type { Translation } from '../api/projects'
import type { EditorRow } from '../api/editor'
import { indexLookup, type EditorIndexData, type IndexRow } from './editor-index'
import type { JobLeverage, LeverageBand } from '../api/analyses'

/**
 * Unit tests for the post-save sync helper. The helper is the chokepoint that
 * three save call sites (saveMut.onSuccess, saveDraft success, conflict
 * "Use mine") rely on to converge on the same four invariants.
 * These tests pin all four invariants in one place so a refactor that drops a
 * step trips here.
 */

/** Seed one loaded page of editor rows holding these translations. */
function seedPage(qc: QueryClient, jobId: EditorId, trs: Translation[]) {
  qc.setQueryData<EditorRow[]>(queryKeys.job.editorPage(jobId, 0, String(trs[0]?.segment_id ?? 0)),
    trs.map(t => ({ segment: { id: t.segment_id } as EditorRow['segment'], translation: t, previous_step_text: null, placeables: [] })))
}

/** The translations of every loaded page. */
function pageTranslations(qc: QueryClient, jobId: EditorId): Translation[] {
  return qc.getQueriesData<EditorRow[]>({ queryKey: queryKeys.job.editorRows(jobId) })
    .flatMap(([, rows]) => rows ?? []).flatMap(r => (r.translation ? [r.translation] : []))
}

function makeTr(id: number, overrides: Partial<Translation> = {}): Translation {
  // Minimal Translation shape; only fields the helper reads. Cast away the
  // rest — vitest inputs don't need full schema fidelity for unit assertions.
  return {
    id,
    segment_id: id,
    target_language: 'es',
    status: 'approved',
    active_text: `text-${id}`,
    active_rich_text: null,
    tm_match_percent: null,
    locked: false,
    edit_distance: 0,
    editing_time_ms: 0,
    edited_by: null,
    origin: undefined,
    predict_outcome: null,
    created_at: '2026-04-30T00:00:00Z',
    updated_at: '2026-04-30T12:00:00Z',
    ...overrides,
  } as Translation
}

describe('applySegmentSaveSuccess', () => {
  it('advances baselineUpdatedAtRef to saved.updated_at', () => {
    const qc = new QueryClient()
    const baselineRef = { current: '2026-04-30T11:00:00Z' as string | null }
    const dirtyRef = { current: true }
    const setOriginalText = vi.fn()
    const saved = makeTr(1, { updated_at: '2026-04-30T12:34:56Z' })

    applySegmentSaveSuccess({
      queryClient: qc, jobId: 42, saved,
      baselineUpdatedAtRef: baselineRef,
      dirtyRef,
      setOriginalText,
      prior: undefined, wordCount: 1, charCount: 1,
    })

    expect(baselineRef.current,
      'baseline must advance to the new server-side updated_at — without this the next save 409s against our own write',
    ).toBe('2026-04-30T12:34:56Z')
  })

  it('patches the React Query translations cache so the next render reads the fresh updated_at', () => {
    const qc = new QueryClient()
    const jobId = 42
    seedPage(qc, jobId, [
      makeTr(1, { updated_at: '2026-04-30T11:00:00Z', active_text: 'old' }),
      makeTr(2, { updated_at: '2026-04-30T11:00:00Z' }),
    ])

    const saved = makeTr(1, { updated_at: '2026-04-30T12:00:00Z', active_text: 'new' })
    applySegmentSaveSuccess({
      queryClient: qc, jobId, saved,
      baselineUpdatedAtRef: { current: null },
      dirtyRef: { current: true },
      setOriginalText: vi.fn(),
      prior: undefined, wordCount: 1, charCount: 1,
    })

    const cached = pageTranslations(qc, jobId)
    expect(cached?.find(r => r.id === 1)?.updated_at).toBe('2026-04-30T12:00:00Z')
    expect(cached?.find(r => r.id === 1)?.active_text).toBe('new')
    // Other rows untouched.
    expect(cached?.find(r => r.id === 2)?.updated_at).toBe('2026-04-30T11:00:00Z')
  })

  it('syncs originalText to saved.active_text so dirty detection reflects the new baseline', () => {
    const setOriginalText = vi.fn()
    applySegmentSaveSuccess({
      queryClient: new QueryClient(),
      jobId: 42,
      saved: makeTr(1, { active_text: 'just-saved' }),
      baselineUpdatedAtRef: { current: null },
      dirtyRef: { current: true },
      setOriginalText,
      prior: undefined, wordCount: 1, charCount: 1,
    })
    expect(setOriginalText).toHaveBeenCalledWith('just-saved')
  })

  it('clears dirtyRef synchronously', () => {
    const dirtyRef = { current: true }
    applySegmentSaveSuccess({
      queryClient: new QueryClient(),
      jobId: 42,
      saved: makeTr(1),
      baselineUpdatedAtRef: { current: null },
      dirtyRef,
      setOriginalText: vi.fn(),
      prior: undefined, wordCount: 1, charCount: 1,
    })
    expect(
      dirtyRef.current,
      'dirty must clear synchronously so a follow-up flush within the same tick does not fire a redundant PUT',
    ).toBe(false)
  })

  it('is a no-op (does not crash) when saved is undefined', () => {
    const baselineRef = { current: 'unchanged' as string | null }
    const dirtyRef = { current: true }
    const setOriginalText = vi.fn()

    expect(() => applySegmentSaveSuccess({
      queryClient: new QueryClient(),
      jobId: 42,
      saved: undefined,
      baselineUpdatedAtRef: baselineRef,
      dirtyRef,
      setOriginalText,
      prior: undefined, wordCount: 1, charCount: 1,
    })).not.toThrow()

    // dirty still cleared (the optimistic-lock invariant holds regardless),
    // but baseline + originalText preserved (no payload to apply).
    expect(baselineRef.current).toBe('unchanged')
    expect(setOriginalText).not.toHaveBeenCalled()
    expect(dirtyRef.current).toBe(false)
  })

  it('skips originalText sync when saved.active_text is null (server returned no text — rare but possible)', () => {
    const setOriginalText = vi.fn()
    applySegmentSaveSuccess({
      queryClient: new QueryClient(),
      jobId: 42,
      // null is distinct from undefined here — backend returns null when the
      // row was cleared (e.g., legacy migration); we don't want to clobber
      // the editor's plain-text shadow with `null`.
      saved: makeTr(1, { active_text: null as unknown as string }),
      baselineUpdatedAtRef: { current: null },
      dirtyRef: { current: true },
      setOriginalText,
      prior: undefined, wordCount: 1, charCount: 1,
    })
    expect(setOriginalText).not.toHaveBeenCalled()
  })
})

/**
 * Unit tests for `patchPropagatedSiblings` — the cache patcher for siblings
 * touched by auto-propagation, so re-activating a sibling before the refetch
 * lands can't capture a stale `baselineUpdatedAtRef` → false-409.
 * The helper applies the same synchronous cache-patch pattern that
 * `applySegmentSaveSuccess` uses for the active row, but for N siblings.
 */
describe('patchPropagatedSiblings', () => {
  // A repetition-heavy file propagates to hundreds of rows at once; the index
  // of a large file holds tens of thousands. All siblings land in one write of
  // the index, not one copy of it per sibling.
  it('moves every sibling in the index with one write', () => {
    const qc = new QueryClient()
    const jobId = 42
    const row = (id: number): IndexRow => ({
      segmentId: id, translationId: id, position: id, subPosition: 0, joinsNext: false, wordCount: 1, status: 'needs_review',
      hasText: false, stepEdited: false, predicted: false, origin: '', tmMatch: null, locked: false,
      inFlight: '', confirmedVia: '', qeNoteHead: '', repetition: 2, fileIdx: 0,
    })
    const rows = [row(1), row(2), row(3)]
    qc.setQueryData<EditorIndexData>(queryKeys.job.editorIndex(jobId), {
      rows, repetitionStats: { segments: 2, words: 2, characters: 2 }, lookup: indexLookup(rows), files: [],
    })
    let writes = 0
    const unsubscribe = qc.getQueryCache().subscribe(e => {
      if (e.type === 'updated' && e.action.type === 'success' && e.query.queryKey[2] === 'index') writes++
    })

    patchPropagatedSiblings({ queryClient: qc, jobId, wordCount: 1, charCount: 1, siblings: [
      makeTr(2, { active_text: 'propagated', status: 'approved' }),
      makeTr(3, { active_text: 'propagated', status: 'approved' }),
    ] })
    unsubscribe()

    const idx = qc.getQueryData<EditorIndexData>(queryKeys.job.editorIndex(jobId))!.rows
    expect(idx.map(r => r.status)).toEqual(['needs_review', 'approved', 'approved'])
    expect(writes).toBe(1)
  })

  it('patches every sibling row in the cache with its post-propagation updated_at', () => {
    const qc = new QueryClient()
    const jobId = 42
    seedPage(qc, jobId, [
      makeTr(1, { updated_at: '2026-04-30T10:00:00Z', active_text: 'orig-1' }),
      makeTr(2, { updated_at: '2026-04-30T10:00:00Z', active_text: 'orig-2' }),
      makeTr(3, { updated_at: '2026-04-30T10:00:00Z', active_text: 'orig-3' }),
    ])

    // Backend's PropagateTranslation returned siblings 2 and 3 (1 was the
    // exclude_id — the row that was just saved by saveMut).
    // Origin is sticky automation-lineage only; human /
    // propagation events leave it alone. The propagated siblings here were
    // not previously TM/MT-stamped, so origin stays undefined.
    const siblings = [
      makeTr(2, { updated_at: '2026-04-30T11:00:00Z', active_text: 'propagated' }),
      makeTr(3, { updated_at: '2026-04-30T11:00:00Z', active_text: 'propagated' }),
    ]
    patchPropagatedSiblings({ queryClient: qc, jobId, siblings, wordCount: 1, charCount: 1 })

    const cached = pageTranslations(qc, jobId)
    expect(cached?.find(r => r.id === 2)?.updated_at).toBe('2026-04-30T11:00:00Z')
    expect(cached?.find(r => r.id === 2)?.active_text).toBe('propagated')
    expect(cached?.find(r => r.id === 3)?.updated_at).toBe('2026-04-30T11:00:00Z')
    expect(cached?.find(r => r.id === 3)?.active_text).toBe('propagated')
    // Row 1 (the excluded row, the one that was just saved) untouched by this
    // helper — its own saveMut.onSuccess applied applySegmentSaveSuccess.
    expect(cached?.find(r => r.id === 1)?.updated_at,
      "the saving row (exclude_id) is NOT in the siblings list and must not be touched here",
    ).toBe('2026-04-30T10:00:00Z')
  })

  it('is a no-op when siblings is empty', () => {
    const qc = new QueryClient()
    const jobId = 42
    seedPage(qc, jobId, [
      makeTr(1, { updated_at: 'unchanged' }),
    ])
    expect(() => patchPropagatedSiblings({ queryClient: qc, jobId, siblings: [], wordCount: 1, charCount: 1 })).not.toThrow()
    const cached = pageTranslations(qc, jobId)
    expect(cached?.[0]?.updated_at).toBe('unchanged')
  })
})

// A save moves its row in the leverage bar, and a propagation moves the
// repetitions, which share the confirmed segment's word and character counts.
// The index says which band and status each row was counted in, so the bar
// moves without a full-job refetch.
describe('leverage bar after a save', () => {
  const zero: LeverageBand = {
    segments: 0, words: 0, characters: 0,
    segments_auto_approved: 0, words_auto_approved: 0, characters_auto_approved: 0,
    segments_needs_review: 0, words_needs_review: 0, characters_needs_review: 0,
  }
  const jobId = 42
  const row = (id: number): IndexRow => ({
    segmentId: id, translationId: id, position: id, subPosition: 0, joinsNext: false, wordCount: 2, status: 'needs_review',
    hasText: true, stepEdited: false, predicted: false, origin: 'tm', tmMatch: 100, locked: false,
    inFlight: '', confirmedVia: '', qeNoteHead: '', repetition: 2, fileIdx: 0,
  })
  function seed(indexed: number[]): QueryClient {
    const qc = new QueryClient()
    const rows = indexed.map(row)
    qc.setQueryData<EditorIndexData>(queryKeys.job.editorIndex(jobId), {
      rows, repetitionStats: { segments: 2, words: 4, characters: 22 }, lookup: indexLookup(rows), files: [],
    })
    qc.setQueryData<JobLeverage>(queryKeys.job.leverage(jobId), {
      job_id: jobId, target_language: 'es',
      tm_101: zero, tm_100: { ...zero, segments: 2, words: 4, characters: 22, segments_needs_review: 2, words_needs_review: 4, characters_needs_review: 22 },
      tm_85_99: zero, tm_65_84: zero, new: zero, repetitions: zero,
    })
    return qc
  }
  const approved = (id: number) => makeTr(id, { status: 'approved', origin: { translation_origin: 'tm', valid: true }, tm_match_percent: 100 } as Partial<Translation>)

  it('moves each sibling from the status the index holds into approved, with no refetch', () => {
    const qc = seed([2, 3])
    patchPropagatedSiblings({ queryClient: qc, jobId, siblings: [approved(2), approved(3)], wordCount: 2, charCount: 11 })

    expect(qc.getQueryData<JobLeverage>(queryKeys.job.leverage(jobId))!.tm_100).toEqual({ ...zero, segments: 2, words: 4, characters: 22 })
    expect(qc.getQueryState(queryKeys.job.leverage(jobId))?.isInvalidated).toBe(false)
  })

  const save = (qc: QueryClient, saved: Translation, prior: Translation) => applySegmentSaveSuccess({
    queryClient: qc, jobId, saved, prior, wordCount: 2, charCount: 11,
    baselineUpdatedAtRef: { current: null }, dirtyRef: { current: true }, setOriginalText: vi.fn(),
  })
  const stalePrior = (id: number) => makeTr(id, { status: 'needs_review', origin: { translation_origin: 'tm', valid: true }, tm_match_percent: 100 } as Partial<Translation>)

  it("moves a saved row from the status the index holds, not the caller's copy", () => {
    const qc = seed([2])
    qc.setQueryData<EditorIndexData>(queryKeys.job.editorIndex(jobId), old => ({ ...old!, rows: [{ ...old!.rows[0], status: 'auto_approved' }] }))
    qc.setQueryData<JobLeverage>(queryKeys.job.leverage(jobId), old => ({
      ...old!, tm_100: { ...zero, segments: 1, words: 2, characters: 11, segments_auto_approved: 1, words_auto_approved: 2, characters_auto_approved: 11 },
    }))
    save(qc, approved(2), stalePrior(2))

    expect(qc.getQueryData<JobLeverage>(queryKeys.job.leverage(jobId))!.tm_100).toEqual({ ...zero, segments: 1, words: 2, characters: 11 })
  })

  it("moves a saved row from the caller's copy when the index does not hold it", () => {
    const qc = seed([])
    save(qc, approved(2), stalePrior(2))

    expect(qc.getQueryData<JobLeverage>(queryKeys.job.leverage(jobId))!.tm_100.segments_needs_review).toBe(1)
  })

  // A repetition confirmed while its source's propagation is running: the
  // propagation moves it first, and the confirm, holding the row as it was
  // when the key was pressed, must not move it a second time.
  it('counts a row a propagation already moved only once', () => {
    const qc = seed([2])
    patchPropagatedSiblings({ queryClient: qc, jobId, siblings: [approved(2)], wordCount: 2, charCount: 11 })
    save(qc, approved(2), stalePrior(2))

    expect(qc.getQueryData<JobLeverage>(queryKeys.job.leverage(jobId))!.tm_100.segments_needs_review).toBe(1)
  })

  it('refetches the bar when a sibling is not in the index', () => {
    const qc = seed([2])
    patchPropagatedSiblings({ queryClient: qc, jobId, siblings: [approved(2), approved(3)], wordCount: 2, charCount: 11 })

    expect(qc.getQueryState(queryKeys.job.leverage(jobId))?.isInvalidated).toBe(true)
  })
})

// A hold's acquire or renewal returns the row as the server read it, which can
// be older than what this tab already has (its own save landed in between).
describe('applyAcquiredRow', () => {
  const seed = (rows: Translation[]) => {
    const qc = new QueryClient()
    seedPage(qc, 7, rows)
    return qc
  }
  const cached = (qc: QueryClient, id: number) =>
    pageTranslations(qc, 7)?.find(r => r.id === id)

  it('patches a newer row', () => {
    const qc = seed([makeTr(1, { active_text: 'mine', updated_at: '2026-10-15T10:00:00.100Z' })])
    applyAcquiredRow(qc, 7, makeTr(1, { active_text: 'theirs', updated_at: '2026-10-15T10:00:05Z' }))
    expect(cached(qc, 1)?.active_text).toBe('theirs')
  })

  it('never rolls the cache back to an older row', () => {
    const qc = seed([makeTr(1, { active_text: 'my save', updated_at: '2026-10-15T10:00:05Z' })])
    applyAcquiredRow(qc, 7, makeTr(1, { active_text: 'before my save', updated_at: '2026-10-15T10:00:04.900Z' }))
    expect(cached(qc, 1)?.active_text).toBe('my save')
  })

  // A multi-file editor caches its rows under its scope id; the hold names
  // the row's job.
  it('patches a newer row in a multi-file editor that holds the job', () => {
    const qc = new QueryClient()
    seedPage(qc, '3:7,8', [makeTr(1, { active_text: 'mine', updated_at: '2026-10-15T10:00:00.100Z' })])
    applyAcquiredRow(qc, 7, makeTr(1, { active_text: 'theirs', updated_at: '2026-10-15T10:00:05Z' }))
    expect(pageTranslations(qc, '3:7,8').find(r => r.id === 1)?.active_text).toBe('theirs')
  })

  it('leaves a row the cache does not have alone', () => {
    const qc = seed([makeTr(1)])
    applyAcquiredRow(qc, 7, makeTr(2, { updated_at: '2026-10-15T10:00:05Z' }))
    expect(cached(qc, 2)).toBeUndefined()
  })
})


describe('optimisticAdjustLeverage', () => {
  const zero: LeverageBand = {
    segments: 0, words: 0, characters: 0,
    segments_auto_approved: 0, words_auto_approved: 0, characters_auto_approved: 0,
    segments_needs_review: 0, words_needs_review: 0, characters_needs_review: 0,
  }
  const band = (b: Partial<LeverageBand>): LeverageBand => ({ ...zero, ...b })
  const tmOrigin = { translation_origin: 'tm', valid: true }
  const mtOrigin = { translation_origin: 'mt', valid: true }

  function seed(bands: Partial<JobLeverage>): QueryClient {
    const qc = new QueryClient()
    qc.setQueryData<JobLeverage>(queryKeys.job.leverage(7), {
      job_id: 7, target_language: 'es',
      tm_101: zero, tm_100: zero, tm_85_99: zero, tm_65_84: zero, new: zero, repetitions: zero,
      ...bands,
    })
    return qc
  }
  const read = (qc: QueryClient) => qc.getQueryData<JobLeverage>(queryKeys.job.leverage(7))!

  it('moves a draft that changes band (a panel TM insert) out of its old band and into the new one', () => {
    const qc = seed({ new: band({ segments: 1, words: 2, characters: 11, segments_needs_review: 1, words_needs_review: 2, characters_needs_review: 11 }) })
    const prior = makeTr(1, { status: 'needs_review', origin: mtOrigin })
    const saved = makeTr(1, { status: 'needs_review', origin: tmOrigin, tm_match_percent: 100 })

    optimisticAdjustLeverage(qc, 7, leverageFacts(prior), leverageFacts(saved), 2, 11)

    expect(read(qc).new).toEqual(zero)
    expect(read(qc).tm_100).toEqual(band({ segments: 1, words: 2, characters: 11, segments_needs_review: 1, words_needs_review: 2, characters_needs_review: 11 }))
  })

  it('takes an auto-approved segment out of the band it was counted in, not the band it moves to', () => {
    const qc = seed({ new: band({ segments: 1, words: 2, characters: 11, segments_auto_approved: 1, words_auto_approved: 2, characters_auto_approved: 11 }) })
    const prior = makeTr(1, { status: 'auto_approved', origin: mtOrigin })
    const saved = makeTr(1, { status: 'needs_review', origin: tmOrigin, tm_match_percent: 87 })

    optimisticAdjustLeverage(qc, 7, leverageFacts(prior), leverageFacts(saved), 2, 11)

    expect(read(qc).new).toEqual(zero)
    expect(read(qc).tm_85_99).toEqual(band({ segments: 1, words: 2, characters: 11, segments_needs_review: 1, words_needs_review: 2, characters_needs_review: 11 }))
  })

  it('moves only the status subset when the band stays the same', () => {
    const qc = seed({ tm_100: band({ segments: 1, words: 2, characters: 11, segments_auto_approved: 1, words_auto_approved: 2, characters_auto_approved: 11 }) })
    const prior = makeTr(1, { status: 'auto_approved', origin: tmOrigin, tm_match_percent: 100 })
    const saved = makeTr(1, { status: 'needs_review', origin: tmOrigin, tm_match_percent: 100 })

    optimisticAdjustLeverage(qc, 7, leverageFacts(prior), leverageFacts(saved), 2, 11)

    expect(read(qc).tm_100).toEqual(band({ segments: 1, words: 2, characters: 11, segments_needs_review: 1, words_needs_review: 2, characters_needs_review: 11 }))
  })
})
