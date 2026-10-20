import { describe, it, expect, vi, beforeEach } from 'vitest'
import { QueryClient, type InfiniteData, type QueryKey } from '@tanstack/react-query'
import {
  invalidateJobContext,
  invalidateProjectStatus,
  invalidateTMContext,
  invalidateUpdatedTMs,
  notifyTMSaved,
  applyTMUpdatesAfterSave,
  __resetTMSavedThrottleForTest,
  advanceTranslationInCache,
  advanceTMEntryInCache,
  removeTMEntryFromCache,
} from './query-invalidation'
import { queryKeys } from './query-keys'
import type { Translation } from '../api/projects'
import type { TMEntriesPage, TMEntry } from '../api/tms'

/** The cached TM entry pages under `key`; throws when nothing is cached there. */
function entryPages(qc: QueryClient, key: QueryKey): TMEntriesPage[] {
  const cached = qc.getQueryData<InfiniteData<TMEntriesPage>>(key)
  if (!cached) throw new Error(`no cached TM entries under ${JSON.stringify(key)}`)
  return cached.pages
}

// Every cache invalidateJobContext marks stale, built from the key factories
// so a renamed factory moves the test with it. The TM-matches sidebar has no
// prefix factory: production invalidates the [name, jobId] prefix of
// queryKeys.tmMatches.byJob, derived here the same way.
const JOB_ID = 42
const PROJECT_ID = 10
const EXPECTED_JOB_INVALIDATIONS: ReadonlyArray<readonly unknown[]> = [
  queryKeys.job.detail(JOB_ID),
  queryKeys.job.translations(JOB_ID),
  queryKeys.job.analysis(JOB_ID),
  queryKeys.job.steps(JOB_ID),
  queryKeys.job.stepStatuses(JOB_ID), // previous-step chips
  queryKeys.job.leverage(JOB_ID),   // CAT status-bar needs_review counts
  queryKeys.job.qaReport(JOB_ID),   // stored QA findings the editor loads on open
  queryKeys.segmentHistory.all,     // Apply TM/MT and AI re-runs append history rows
  queryKeys.jobs.byProject(PROJECT_ID),
  queryKeys.files.byProject(PROJECT_ID),
  queryKeys.projectAnalysis.v2(PROJECT_ID),
  queryKeys.tmMatches.byJob(JOB_ID, '', null).slice(0, 2), // every cached source of the job
  queryKeys.comments.allCounts,     // segment-grid comment indicator — QE+APE writes AI comments
  queryKeys.comments.all,           // a clear deletes AI comments; the open panel's list has to follow
]

// Queries that hang under the prefix keys above: a cached source's TM matches,
// one translation's comments and its history. Stale through the prefix.
const PREFIXED_JOB_QUERIES: ReadonlyArray<readonly unknown[]> = [
  queryKeys.tmMatches.byJob(JOB_ID, 'a cached source', 7),
  queryKeys.comments.byTranslation(7),
  queryKeys.segmentHistory.byTranslation(7),
]

// Other jobs' and projects' caches, plus an unrelated key. A call that
// invalidated too widely would flip these as well.
const UNTOUCHED_JOB_QUERIES: ReadonlyArray<readonly unknown[]> = [
  queryKeys.job.detail(99),
  queryKeys.job.translations(99),
  queryKeys.jobs.byProject(11),
  queryKeys.files.byProject(11),
  queryKeys.projectAnalysis.v2(11),
  queryKeys.tmMatches.byJob(43, 'other job source', null),
  ['unrelated'],
]

function isInvalidated(qc: QueryClient, key: readonly unknown[]): boolean | undefined {
  return qc.getQueryState(key)?.isInvalidated
}

// Keys as a sorted list of their JSON form, so two sets compare as sets.
function keySet(keys: ReadonlyArray<readonly unknown[]>): string[] {
  return keys.map(k => JSON.stringify(k)).sort()
}

describe('invalidateJobContext', () => {
  it('marks exactly the documented job-related caches stale, no more and no less', () => {
    const qc = new QueryClient()
    for (const key of [...EXPECTED_JOB_INVALIDATIONS, ...PREFIXED_JOB_QUERIES, ...UNTOUCHED_JOB_QUERIES]) {
      qc.setQueryData(key, 'cached')
    }
    const spy = vi.spyOn(qc, 'invalidateQueries')

    invalidateJobContext(qc, PROJECT_ID, JOB_ID)

    // The calls made, as a set: a stray invalidation of a key nobody seeded
    // would not show in the cache below, so the call list is pinned too.
    const calledKeys = spy.mock.calls.map(c => (c[0] as { queryKey: readonly unknown[] }).queryKey)
    expect(keySet(calledKeys)).toEqual(keySet(EXPECTED_JOB_INVALIDATIONS))

    // The effect on real cached queries: exactly the expected keys plus what
    // hangs under the prefixes went stale, and nothing else did.
    const stale = qc.getQueryCache().getAll().filter(q => q.state.isInvalidated).map(q => q.queryKey)
    expect(keySet(stale)).toEqual(keySet([...EXPECTED_JOB_INVALIDATIONS, ...PREFIXED_JOB_QUERIES]))
    for (const key of UNTOUCHED_JOB_QUERIES) {
      expect(isInvalidated(qc, key), `${JSON.stringify(key)} should stay fresh`).toBe(false)
    }
  })
})

// The caches holding a project's
// `status`, which auto-complete can change behind any mutation that alters the
// project's job set.
const EXPECTED_PROJECT_STATUS_KEYS: ReadonlyArray<readonly (string | number)[]> = [
  ['projects'],                 // projects.all — ProjectDetail reads `project` from this
  ['projects-paginated'],       // projects.paginated — the /projects list
  ['project-settings', 10],     // project.settings — embeds the full project object
  ['project-events', 10],       // projectEvents.byProject — auto-complete emits an event
]

describe('invalidateProjectStatus', () => {
  it('invalidates exactly the caches that hold a project status (no more, no less)', () => {
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')

    invalidateProjectStatus(qc, 10)

    const invalidatedKeys = spy.mock.calls.map(c => c[0]?.queryKey)
    expect(invalidatedKeys).toEqual(expect.arrayContaining([...EXPECTED_PROJECT_STATUS_KEYS]))
    expect(
      invalidatedKeys,
      `expected exactly ${EXPECTED_PROJECT_STATUS_KEYS.length} invalidations, got ${invalidatedKeys.length}: ${JSON.stringify(invalidatedKeys)}`,
    ).toHaveLength(EXPECTED_PROJECT_STATUS_KEYS.length)
  })
})

describe('invalidateTMContext', () => {
  it('invalidates TM list + entries + per-entry history when tmId is provided', () => {
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')

    invalidateTMContext(qc, 5)

    const invalidatedKeys = spy.mock.calls.map(c => c[0]?.queryKey)
    const expectedKeys = [
      ['tms'],
      ['tm-entry-history'], // prefix invalidation for any open history dialog
      ['tm-entries', 5],
      ['tm-lang-pairs', 5],
    ]
    expect(invalidatedKeys).toEqual(expect.arrayContaining(expectedKeys))
    expect(
      invalidatedKeys,
      `expected exactly ${expectedKeys.length} invalidations, got ${invalidatedKeys.length}: ${JSON.stringify(invalidatedKeys)}`,
    ).toHaveLength(expectedKeys.length)
  })

  it('invalidates the global TM list AND the entry-history prefix when no tmId', () => {
    // The history-prefix invalidation isn't tmId-conditional — an open dialog
    // anywhere in the app should refresh after any TM mutation, even ones
    // that don't carry a specific tmId (e.g. global TMX operations).
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')

    invalidateTMContext(qc)

    const invalidatedKeys = spy.mock.calls.map(c => c[0]?.queryKey)
    expect(invalidatedKeys).toEqual([['tms'], ['tm-entry-history']])
  })
})

// Fan-out invalidation after a segment confirm wrote to multiple TMs
// (`updated_tm_ids`), so the TM browser shows the new entry without a
// refresh. Two invariants pinned:
//   (1) the loop calls invalidateTMContext once per ID — both TMs invalidated
//   (2) empty input is a safe no-op — no spurious 'tms' invalidations
describe('invalidateUpdatedTMs', () => {
  it('invalidates each provided TM ID once (fan-out per-TM, no cross-contamination)', () => {
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')

    invalidateUpdatedTMs(qc, [3, 7])

    const invalidatedKeys = spy.mock.calls.map(c => c[0]?.queryKey)
    // Pins one global invalidation per call, then per-TM keys: two TMs → 6
    // calls (2 globals + 2 per-TM × 2 TMs).
    expect(invalidatedKeys).toEqual(expect.arrayContaining([
      ['tms'],
      ['tm-entry-history'],
      ['tm-entries', 3],
      ['tm-lang-pairs', 3],
      ['tm-entries', 7],
      ['tm-lang-pairs', 7],
    ]))
    expect(invalidatedKeys).toHaveLength(6)
    // Anti-tautology guard against the helper "accidentally" calling
    // invalidateTMContext for the WRONG ID — assert per-ID counts.
    expect(invalidatedKeys.filter(k => Array.isArray(k) && k[0] === 'tm-entries')).toHaveLength(2)
    expect(invalidatedKeys.filter(k => Array.isArray(k) && k[0] === 'tm-lang-pairs')).toHaveLength(2)
    // Globals fire exactly once each (the lift fix).
    expect(invalidatedKeys.filter(k => Array.isArray(k) && k[0] === 'tms')).toHaveLength(1)
    expect(invalidatedKeys.filter(k => Array.isArray(k) && k[0] === 'tm-entry-history')).toHaveLength(1)
  })

  it('is a safe no-op when the IDs array is empty', () => {
    // The empty-IDs case maps to "save didn't write to any TM" (no profile
    // assignment, all upserts errored). Invalidating ['tms'] in that case
    // would be wasteful refetches across the app. The helper must do nothing.
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')

    invalidateUpdatedTMs(qc, [])

    expect(spy).not.toHaveBeenCalled()
  })

  it('handles a single ID identically to invalidateTMContext (no behavioural drift)', () => {
    // Anchors the helper to the per-TM contract of invalidateTMContext.
    // If invalidateTMContext gains an extra invalidation in the future, this
    // test ensures invalidateUpdatedTMs picks it up — no per-TM logic should
    // diverge between the two paths.
    const qc1 = new QueryClient()
    const qc2 = new QueryClient()
    const spy1 = vi.spyOn(qc1, 'invalidateQueries')
    const spy2 = vi.spyOn(qc2, 'invalidateQueries')

    invalidateUpdatedTMs(qc1, [42])
    invalidateTMContext(qc2, 42)

    const keys1 = spy1.mock.calls.map(c => c[0]?.queryKey)
    const keys2 = spy2.mock.calls.map(c => c[0]?.queryKey)
    expect(keys1).toEqual(keys2)
  })
})

// The "TM updated" toast, throttled to 1 per 3s so a translator confirming
// many segments doesn't see a wall of toasts. The throttle is module-scoped —
// all save paths share one window — so we reset it between tests.
describe('notifyTMSaved (toast throttle)', () => {
  beforeEach(() => {
    __resetTMSavedThrottleForTest()
  })

  it('fires the toast when at least one TM was written', () => {
    const notify = vi.fn()
    notifyTMSaved([5], notify)
    expect(notify).toHaveBeenCalledTimes(1)
    expect(notify).toHaveBeenCalledWith('TM updated')
  })

  it('does NOT fire when the IDs array is empty (nothing to confirm)', () => {
    // Save with no write-TMs configured → empty array → no toast. Without
    // this guard the user would see "TM updated" even after a save that
    // touched no TM, which would be misleading.
    const notify = vi.fn()
    notifyTMSaved([], notify)
    expect(notify).not.toHaveBeenCalled()
  })

  it('throttles back-to-back calls within the 3-second window', () => {
    // Use fake timers so the test is deterministic instead of sleep-based.
    // Spec §H requires "1 toast per 3s." We simulate three rapid confirms
    // (translator hitting Cmd+Enter quickly through a run of segments)
    // and assert only the first one triggered a toast.
    vi.useFakeTimers()
    try {
      const notify = vi.fn()
      notifyTMSaved([1], notify)            // t=0  → fires
      vi.advanceTimersByTime(500)
      notifyTMSaved([1], notify)            // t=500 → throttled
      vi.advanceTimersByTime(2000)
      notifyTMSaved([1], notify)            // t=2500 → still throttled
      expect(notify).toHaveBeenCalledTimes(1)

      // Past the 3-second mark → next call fires again.
      vi.advanceTimersByTime(600)           // t=3100
      notifyTMSaved([1], notify)
      expect(notify).toHaveBeenCalledTimes(2)
    } finally {
      vi.useRealTimers()
    }
  })

  it('throttle window starts ticking from the FIRST notify, not from each call', () => {
    // Anti-tautology: ensure the throttle is "absolute time since last toast"
    // and not "time since last call." If a developer accidentally moved the
    // window to slide forward on every call (whether it fired or not),
    // back-to-back rapid calls would push the next allowed toast indefinitely
    // — translators in a sustained confirm streak would never see the toast
    // again. This test pins the spec-correct behavior: window resets only
    // when a toast actually fires.
    vi.useFakeTimers()
    try {
      const notify = vi.fn()
      notifyTMSaved([1], notify)            // t=0 → fires, window starts
      for (let t = 100; t < 3000; t += 100) {
        vi.advanceTimersByTime(100)
        notifyTMSaved([1], notify)          // throttled
      }
      expect(notify).toHaveBeenCalledTimes(1)

      vi.advanceTimersByTime(100)           // t=3000
      notifyTMSaved([1], notify)
      expect(notify).toHaveBeenCalledTimes(2)
    } finally {
      vi.useRealTimers()
    }
  })
})

// `applyTMUpdatesAfterSave` is the single chokepoint that BOTH save paths
// (main saveMut + "use mine" conflict resolution) call after
// `applySegmentSaveSuccess`. These tests pin the contract: any caller of this
// helper gets BOTH side effects, in the right order, with no skipped step.
describe('applyTMUpdatesAfterSave (chokepoint for post-save TM effects)', () => {
  beforeEach(() => {
    __resetTMSavedThrottleForTest()
  })

  it('invalidates the TM caches AND fires the throttled toast in one call', () => {
    const qc = new QueryClient()
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
    const notify = vi.fn()

    applyTMUpdatesAfterSave(qc, [11, 22], notify)

    // The fan-out invalidation ran (audit-fix-N1 batched form).
    const keys = invalidateSpy.mock.calls.map(c => c[0]?.queryKey)
    expect(keys).toEqual(expect.arrayContaining([
      ['tms'],
      ['tm-entry-history'],
      ['tm-entries', 11],
      ['tm-lang-pairs', 11],
      ['tm-entries', 22],
      ['tm-lang-pairs', 22],
    ]))
    expect(keys).toHaveLength(6)
    // The toast fired (within throttle window, single TMset).
    expect(notify).toHaveBeenCalledWith('TM updated')
    expect(notify).toHaveBeenCalledTimes(1)
  })

  it('skips both side effects when the IDs array is empty (no spurious toast or wasteful invalidation)', () => {
    // The use-mine conflict path can resolve to "no TM was written" (e.g.
    // if profile had a write TM that was deleted between the original save
    // and the retry). The chokepoint must NOT toast "TM updated" in that
    // case, AND must not pollute the cache with empty-ID invalidations.
    const qc = new QueryClient()
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
    const notify = vi.fn()

    applyTMUpdatesAfterSave(qc, [], notify)

    expect(invalidateSpy).not.toHaveBeenCalled()
    expect(notify).not.toHaveBeenCalled()
  })

  it('shares one throttle window across both call sites (main save + use-mine)', () => {
    // A translator confirms a segment (main path fires toast t=0) then
    // immediately resolves a conflict on a sibling row (use-mine path at
    // t=500ms). Pins: only ONE toast within 3s, because both paths share the
    // module-level throttle.
    vi.useFakeTimers()
    try {
      const qc = new QueryClient()
      const notify = vi.fn()

      // Main save path
      applyTMUpdatesAfterSave(qc, [1], notify)
      expect(notify).toHaveBeenCalledTimes(1)

      vi.advanceTimersByTime(500)
      // Use-mine path on a sibling row
      applyTMUpdatesAfterSave(qc, [1], notify)
      expect(notify).toHaveBeenCalledTimes(1)  // throttled — still one toast total
    } finally {
      vi.useRealTimers()
    }
  })
})

// Regression guard: `advanceTranslationInCache` patches the cached
// translations list with the saved row's fresh `updated_at` SYNCHRONOUSLY, so
// re-activating the segment before the refetch can't regress
// `baselineUpdatedAtRef` and 409 against the user's own write.
//
// Tests below pin the specific cache-shape the helper touches AND prove
// that calling it preserves untouched rows (no friendly fire).
describe('advanceTranslationInCache', () => {
  function makeTr(id: number, updated_at: string, extra: Partial<Translation> = {}): Translation {
    return {
      id,
      segment_id: id,
      target_language: 'es',
      status: 'approved',
      active_text: 'old',
      active_rich_text: '',
      updated_at,
      created_at: '2026-01-01T00:00:00Z',
      ...extra,
    } as unknown as Translation
  }

  // The cache shape MUST mirror what `listJobTranslations` actually stores —
  // it unwraps `r.data` so the cache holds `Translation[]` directly, NOT
  // `{ data: Translation[] }`. A helper that checks for the wrong shape is a
  // *silent no-op*, so these tests pin the real cache shape.
  it('updates the matching row\'s updated_at + saved fields in the cache', () => {
    const qc = new QueryClient()
    const jobId = 42
    qc.setQueryData<Translation[]>(queryKeys.job.translations(jobId), [
      makeTr(1, 'T0', { active_text: 'original' }),
    ])

    const saved = makeTr(1, 'T1', { active_text: 'edited' })
    advanceTranslationInCache(qc, jobId, saved)

    const cache = qc.getQueryData<Translation[]>(queryKeys.job.translations(jobId))
    expect(cache?.[0].updated_at,
      'cached row\'s updated_at must reflect the just-saved value, ' +
      'so the next isActive flip captures fresh baseline (not stale T0)').toBe('T1')
    expect(cache?.[0].active_text).toBe('edited')
  })

  it('leaves untouched rows alone (no friendly fire)', () => {
    const qc = new QueryClient()
    const jobId = 42
    qc.setQueryData<Translation[]>(queryKeys.job.translations(jobId), [
      makeTr(1, 'T0'),
      makeTr(2, 'X0', { active_text: 'other' }),
    ])

    advanceTranslationInCache(qc, jobId, makeTr(1, 'T1'))

    const cache = qc.getQueryData<Translation[]>(queryKeys.job.translations(jobId))
    const row2 = cache!.find(r => r.id === 2)!
    expect(row2?.updated_at, 'row 2 not touched').toBe('X0')
    expect(row2.active_text, 'row 2 fields preserved').toBe('other')
  })

  it('is a no-op when the cache has no entry for the job (no save-before-load races)', () => {
    const qc = new QueryClient()
    advanceTranslationInCache(qc, 42, makeTr(1, 'T1'))
    expect(qc.getQueryData(queryKeys.job.translations(42))).toBeUndefined()
  })

  it('is a no-op when the cache shape is unexpected (defensive)', () => {
    const qc = new QueryClient()
    const jobId = 42
    qc.setQueryData(queryKeys.job.translations(jobId), { data: 'not an array' })
    advanceTranslationInCache(qc, jobId, makeTr(1, 'T1'))
    // The helper must NOT throw and must NOT mutate the unexpected shape.
    expect(qc.getQueryData(queryKeys.job.translations(jobId))).toEqual({ data: 'not an array' })
  })
})

// Same race shape as above, but in TMBrowser. After updateMut
// resolves, `baselineUpdatedAtRef` advances to saved.updated_at, but the
// cached TM entries list still holds the pre-save value until the
// invalidate-driven refetch lands. If the user double-clicks back into the
// same cell in that window, `startEdit` reads `entry.updated_at` from the
// stale cache — baseline regresses, next save 409s against the user's own
// previous write.
//
// Cache shape gotcha: TM entries
// live in a `useInfiniteQuery` cache as `InfiniteData<{ data: TMEntry[],
// cursor, has_more, total }>`. We must walk pages to find the matching row.
// AND there are multiple cache keys per TM (one per src/tgt/search filter
// combo) — so the helper iterates ALL caches matching the prefix
// `['tm-entries', tmId]` via `setQueriesData` with a predicate.
describe('advanceTMEntryInCache', () => {
  function makeEntry(id: number, updated_at: string, extra: Partial<TMEntry> = {}): TMEntry {
    return {
      id,
      tm_id: 5,
      source_language: 'en',
      target_language: 'es',
      source_text: 'hello',
      target_text: 'hola',
      created_at: '2026-01-01T00:00:00Z',
      updated_at,
      ...extra,
    }
  }

  it('updates the matching row\'s updated_at across paginated InfiniteData pages', () => {
    const qc = new QueryClient()
    const tmId = 5
    qc.setQueryData(queryKeys.tm.entries(tmId), {
      pages: [
        { data: [makeEntry(1, 'T0'), makeEntry(2, 'X0')], cursor: 2, has_more: true, total: 4 },
        { data: [makeEntry(3, 'Y0'), makeEntry(99, 'Z0', { target_text: 'old' })], cursor: 4, has_more: false, total: 4 },
      ],
      pageParams: [0, 2],
    })

    advanceTMEntryInCache(qc, tmId, makeEntry(99, 'Z1', { target_text: 'fresh' }))

    const updated = entryPages(qc, queryKeys.tm.entries(tmId))[1].data.find(e => e.id === 99)
    expect(updated?.updated_at,
      'cached row\'s updated_at must reflect the saved value across paginated pages').toBe('Z1')
    expect(updated?.target_text, 'saved fields win on conflict').toBe('fresh')
  })

  it('updates ALL filter-scoped caches for the same TM (so a refilter doesn\'t reveal stale data)', () => {
    // The user can filter by source/target lang or search. Each filter
    // combination produces a separate cache entry under
    // `['tm-entries', tmId, sourceLang, targetLang, search]`. After a save,
    // every one of those caches must reflect the new updated_at — otherwise
    // switching filters mid-edit re-introduces the stale baseline.
    const qc = new QueryClient()
    const tmId = 5
    const noFilter = queryKeys.tm.entries(tmId)
    const withSrc = queryKeys.tm.entries(tmId, 'en')
    const withSearch = queryKeys.tm.entries(tmId, undefined, undefined, 'foo')

    qc.setQueryData(noFilter, { pages: [{ data: [makeEntry(1, 'T0')] }], pageParams: [0] })
    qc.setQueryData(withSrc, { pages: [{ data: [makeEntry(1, 'T0')] }], pageParams: [0] })
    qc.setQueryData(withSearch, { pages: [{ data: [makeEntry(1, 'T0')] }], pageParams: [0] })

    advanceTMEntryInCache(qc, tmId, makeEntry(1, 'T1'))

    expect(entryPages(qc, noFilter)[0].data[0].updated_at).toBe('T1')
    expect(entryPages(qc, withSrc)[0].data[0].updated_at).toBe('T1')
    expect(entryPages(qc, withSearch)[0].data[0].updated_at).toBe('T1')
  })

  it('does NOT touch caches for a DIFFERENT TM (no cross-TM friendly fire)', () => {
    const qc = new QueryClient()
    qc.setQueryData(queryKeys.tm.entries(5), { pages: [{ data: [makeEntry(1, 'T0')] }], pageParams: [0] })
    qc.setQueryData(queryKeys.tm.entries(6), { pages: [{ data: [makeEntry(1, 'T0', { tm_id: 6 })] }], pageParams: [0] })

    advanceTMEntryInCache(qc, 5, makeEntry(1, 'T1'))

    expect(entryPages(qc, queryKeys.tm.entries(5))[0].data[0].updated_at).toBe('T1')
    expect(entryPages(qc, queryKeys.tm.entries(6))[0].data[0].updated_at,
      'must NOT clobber another TM\'s cache for an entry that happens to share an id').toBe('T0')
  })

  it('leaves untouched rows alone within the same TM page', () => {
    const qc = new QueryClient()
    const tmId = 5
    qc.setQueryData(queryKeys.tm.entries(tmId), {
      pages: [{ data: [makeEntry(1, 'T0'), makeEntry(2, 'X0', { target_text: 'other' })] }],
      pageParams: [0],
    })

    advanceTMEntryInCache(qc, tmId, makeEntry(1, 'T1'))

    const row2 = entryPages(qc, queryKeys.tm.entries(tmId))[0].data.find(e => e.id === 2)
    expect(row2?.updated_at, 'row 2 must not be touched').toBe('X0')
    expect(row2?.target_text, 'row 2 fields must be preserved').toBe('other')
  })

  it('is a no-op when no cache exists for the TM (save-before-first-list-load races)', () => {
    const qc = new QueryClient()
    advanceTMEntryInCache(qc, 5, makeEntry(1, 'T1'))
    expect(qc.getQueryData(queryKeys.tm.entries(5))).toBeUndefined()
  })

  it('is defensive against unexpected shapes (does not throw, does not mutate)', () => {
    const qc = new QueryClient()
    const tmId = 5
    // No `pages` array (e.g. partially-hydrated cache or a future shape change).
    qc.setQueryData(queryKeys.tm.entries(tmId), { pages: 'not-an-array' })
    advanceTMEntryInCache(qc, tmId, makeEntry(1, 'T1'))
    expect(qc.getQueryData(queryKeys.tm.entries(tmId))).toEqual({ pages: 'not-an-array' })
  })
})

describe('removeTMEntryFromCache', () => {
  const entry = (id: number, tm_id = 5): TMEntry => ({
    id, tm_id, source_language: 'en', target_language: 'es',
    source_text: 'hello', target_text: 'hola', created_at: '', updated_at: '',
  })

  it('drops the row from whichever page holds it and lowers the total on every page', () => {
    const qc = new QueryClient()
    qc.setQueryData(queryKeys.tm.entries(5), {
      pages: [
        { data: [entry(1), entry(2)], cursor: 2, has_more: true, total: 4 },
        { data: [entry(3), entry(4)], cursor: 4, has_more: false, total: 4 },
      ],
      pageParams: [0, 2],
    })

    removeTMEntryFromCache(qc, 5, 3)

    const pages = entryPages(qc, queryKeys.tm.entries(5))
    expect(pages.flatMap(p => p.data.map(e => e.id))).toEqual([1, 2, 4])
    expect(pages.map(p => p.total)).toEqual([3, 3])
    expect(pages[0].cursor, 'cursors are untouched').toBe(2)
  })

  it('leaves a total the server left out, and caches without the row, alone', () => {
    const qc = new QueryClient()
    const searched = queryKeys.tm.entries(5, undefined, undefined, 'foo')
    const otherFilter = queryKeys.tm.entries(5, 'de')
    qc.setQueryData(searched, { pages: [{ data: [entry(1), entry(2)], cursor: 2, has_more: false }], pageParams: [0] })
    const untouched = { pages: [{ data: [entry(7)], cursor: 7, has_more: false, total: 1 }], pageParams: [0] }
    qc.setQueryData(otherFilter, untouched)

    removeTMEntryFromCache(qc, 5, 1)

    const pages = entryPages(qc, searched)
    expect(pages[0].data.map(e => e.id)).toEqual([2])
    expect(pages[0].total).toBeUndefined()
    expect(qc.getQueryData(otherFilter)).toBe(untouched)
  })

  it('does not touch another TM\'s cache for an entry with the same id', () => {
    const qc = new QueryClient()
    qc.setQueryData(queryKeys.tm.entries(6), { pages: [{ data: [entry(1, 6)], total: 1 }], pageParams: [0] })
    removeTMEntryFromCache(qc, 5, 1)
    expect(entryPages(qc, queryKeys.tm.entries(6))[0].data).toHaveLength(1)
  })

  it('refreshes TM counts, history and language pairs, but not the entry list', () => {
    const qc = new QueryClient()
    const entries = queryKeys.tm.entries(5)
    qc.setQueryData(entries, { pages: [{ data: [entry(1)], total: 1 }], pageParams: [0] })
    qc.setQueryData(queryKeys.tms.all, [])
    qc.setQueryData(queryKeys.tm.langPairs(5), [])
    qc.setQueryData(['tm-entry-history', 5, 1], [])

    removeTMEntryFromCache(qc, 5, 1)

    expect(qc.getQueryState(queryKeys.tms.all)?.isInvalidated).toBe(true)
    expect(qc.getQueryState(queryKeys.tm.langPairs(5))?.isInvalidated).toBe(true)
    expect(qc.getQueryState(['tm-entry-history', 5, 1])?.isInvalidated).toBe(true)
    expect(qc.getQueryState(entries)?.isInvalidated).toBe(false)
  })
})
