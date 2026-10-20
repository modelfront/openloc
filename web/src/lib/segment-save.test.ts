import { describe, it, expect, vi } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { applyAcquiredRow, applySegmentSaveSuccess, optimisticAdjustLeverage, patchPropagatedSiblings } from './segment-save'
import { queryKeys } from './query-keys'
import type { Translation } from '../api/projects'
import type { JobLeverage, LeverageBand } from '../api/analyses'

/**
 * Unit tests for the post-save sync helper. The helper is the chokepoint that
 * three save call sites (saveMut.onSuccess, saveDraft success, conflict
 * "Use mine") rely on to converge on the same four invariants.
 * These tests pin all four invariants in one place so a refactor that drops a
 * step trips here.
 */

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
    })

    expect(baselineRef.current,
      'baseline must advance to the new server-side updated_at — without this the next save 409s against our own write',
    ).toBe('2026-04-30T12:34:56Z')
  })

  it('patches the React Query translations cache so the next render reads the fresh updated_at', () => {
    const qc = new QueryClient()
    const jobId = 42
    qc.setQueryData(queryKeys.job.translations(jobId), [
      makeTr(1, { updated_at: '2026-04-30T11:00:00Z', active_text: 'old' }),
      makeTr(2, { updated_at: '2026-04-30T11:00:00Z' }),
    ])

    const saved = makeTr(1, { updated_at: '2026-04-30T12:00:00Z', active_text: 'new' })
    applySegmentSaveSuccess({
      queryClient: qc, jobId, saved,
      baselineUpdatedAtRef: { current: null },
      dirtyRef: { current: true },
      setOriginalText: vi.fn(),
    })

    const cached = qc.getQueryData<Translation[]>(queryKeys.job.translations(jobId))
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
  it('patches every sibling row in the cache with its post-propagation updated_at', () => {
    const qc = new QueryClient()
    const jobId = 42
    qc.setQueryData(queryKeys.job.translations(jobId), [
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
    patchPropagatedSiblings({ queryClient: qc, jobId, siblings })

    const cached = qc.getQueryData<Translation[]>(queryKeys.job.translations(jobId))
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
    qc.setQueryData(queryKeys.job.translations(jobId), [
      makeTr(1, { updated_at: 'unchanged' }),
    ])
    expect(() => patchPropagatedSiblings({ queryClient: qc, jobId, siblings: [] })).not.toThrow()
    const cached = qc.getQueryData<Translation[]>(queryKeys.job.translations(jobId))
    expect(cached?.[0]?.updated_at).toBe('unchanged')
  })
})

// A hold's acquire or renewal returns the row as the server read it, which can
// be older than what this tab already has (its own save landed in between).
describe('applyAcquiredRow', () => {
  const seed = (rows: Translation[]) => {
    const qc = new QueryClient()
    qc.setQueryData(queryKeys.job.translations(7), rows)
    return qc
  }
  const cached = (qc: QueryClient, id: number) =>
    qc.getQueryData<Translation[]>(queryKeys.job.translations(7))?.find(r => r.id === id)

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

    optimisticAdjustLeverage(qc, 7, prior, saved, 2, 11)

    expect(read(qc).new).toEqual(zero)
    expect(read(qc).tm_100).toEqual(band({ segments: 1, words: 2, characters: 11, segments_needs_review: 1, words_needs_review: 2, characters_needs_review: 11 }))
  })

  it('takes an auto-approved segment out of the band it was counted in, not the band it moves to', () => {
    const qc = seed({ new: band({ segments: 1, words: 2, characters: 11, segments_auto_approved: 1, words_auto_approved: 2, characters_auto_approved: 11 }) })
    const prior = makeTr(1, { status: 'auto_approved', origin: mtOrigin })
    const saved = makeTr(1, { status: 'needs_review', origin: tmOrigin, tm_match_percent: 87 })

    optimisticAdjustLeverage(qc, 7, prior, saved, 2, 11)

    expect(read(qc).new).toEqual(zero)
    expect(read(qc).tm_85_99).toEqual(band({ segments: 1, words: 2, characters: 11, segments_needs_review: 1, words_needs_review: 2, characters_needs_review: 11 }))
  })

  it('moves only the status subset when the band stays the same', () => {
    const qc = seed({ tm_100: band({ segments: 1, words: 2, characters: 11, segments_auto_approved: 1, words_auto_approved: 2, characters_auto_approved: 11 }) })
    const prior = makeTr(1, { status: 'auto_approved', origin: tmOrigin, tm_match_percent: 100 })
    const saved = makeTr(1, { status: 'needs_review', origin: tmOrigin, tm_match_percent: 100 })

    optimisticAdjustLeverage(qc, 7, prior, saved, 2, 11)

    expect(read(qc).tm_100).toEqual(band({ segments: 1, words: 2, characters: 11, segments_needs_review: 1, words_needs_review: 2, characters_needs_review: 11 }))
  })
})
