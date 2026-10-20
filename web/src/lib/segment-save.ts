import { type QueryClient } from '@tanstack/react-query'
import type { Translation } from '../api/projects'
import type { JobLeverage, LeverageBand } from '../api/analyses'
import { advanceTranslationInCache } from './query-invalidation'
import { queryKeys } from './query-keys'

// Band keys mirror the GetSavedAnalysisBands SQL CASE in
// internal/analysis/queries.sql. Keep these in sync — if the SQL bands
// ever change, the optimistic helper below will silently drift otherwise.
type LeverageBandKey = 'tm_101' | 'tm_100' | 'tm_85_99' | 'tm_65_84' | 'new' | 'repetitions'

function pickLeverageBand(origin: string | null | undefined, tmPct: number | null | undefined): LeverageBandKey {
  if (origin === 'tm') {
    if (tmPct === 101) return 'tm_101'
    if (tmPct === 100) return 'tm_100'
    if (tmPct != null && tmPct >= 85) return 'tm_85_99'
    if (tmPct != null && tmPct >= 65) return 'tm_65_84'
  }
  return 'new'
}

// extractOrigin pulls the string out of Translation.origin (sqlc wraps it
// as { translation_origin, valid }). Returns null for invalid / missing.
function extractOrigin(t: Translation | undefined): string | null {
  if (!t) return null
  const o = (t as unknown as { origin?: { translation_origin?: string; valid?: boolean } }).origin
  return o?.valid ? o.translation_origin ?? null : null
}

/**
 * Optimistically adjust the CAT-editor status-bar leverage cache to reflect
 * a single segment's status transition WITHOUT waiting for the network
 * round-trip. Call right after `applySegmentSaveSuccess`; the subsequent
 * `invalidateJobContext` is still useful as a backstop (corrects any drift
 * from this synchronous patch when the canonical refetch lands).
 *
 * Why optimistic + invalidate (not one or the other):
 *   - Optimistic alone is fast but can drift if our local view of `prior`
 *     was stale (another tab edited the row first).
 *   - Invalidate alone forces the user to wait for the server. With
 *     Cmd+Enter spam the bar visibly lags several confirms behind.
 *   - Optimistic + invalidate: instant UX, with the truth landing ~100ms
 *     later as a quiet correction. Same pattern react-query optimistic
 *     mutation guides recommend.
 *
 * Moves the segment out of the band and status subset `prior` counted it in
 * and into the ones `saved` belongs to. A status flip moves only the subset;
 * a draft that changes origin or match % (a Resources-panel insert) also
 * moves the band totals.
 */
export function optimisticAdjustLeverage(
  queryClient: QueryClient,
  jobId: number,
  prior: Translation | undefined,
  saved: Translation | undefined,
  wordCount: number,
  charCount: number,
): void {
  if (!prior || !saved) return
  const from = pickLeverageBand(extractOrigin(prior), prior.tm_match_percent)
  const to = pickLeverageBand(extractOrigin(saved), saved.tm_match_percent)
  if (from === to && prior.status === saved.status) return

  queryClient.setQueryData<JobLeverage>(queryKeys.job.leverage(jobId), old => {
    if (!old) return old
    const next: JobLeverage = { ...old, [from]: { ...old[from] }, [to]: { ...old[to] } }
    const moveTotals = from !== to
    shiftLeverage(next[from], prior.status, -1, moveTotals, wordCount, charCount)
    shiftLeverage(next[to], saved.status, 1, moveTotals, wordCount, charCount)
    return next
  })
}

// 'approved' (human-confirmed) is in neither subset, only in the totals.
function shiftLeverage(
  band: LeverageBand, status: string, sign: 1 | -1, withTotals: boolean, words: number, chars: number,
): void {
  if (withTotals) {
    band.segments += sign
    band.words += sign * words
    band.characters += sign * chars
  }
  if (status === 'needs_review') {
    band.segments_needs_review += sign
    band.words_needs_review += sign * words
    band.characters_needs_review += sign * chars
  } else if (status === 'auto_approved') {
    band.segments_auto_approved += sign
    band.words_auto_approved += sign * words
    band.characters_auto_approved += sign * chars
  }
}

/**
 * Apply the canonical post-save sync for a CAT translation row. Three callers
 * in `SegmentRow.tsx` need to do the SAME four things on a successful save —
 * `saveMut.onSuccess` (Cmd+Enter), `saveDraft` inner success (auto-save /
 * blur-flush), and the conflict banner's "Use mine" handler.
 *
 * One helper that does the four steps every time. Each caller layers its own
 * extras (saveMut adds propagation + auto-QA, saveDraft adds the "saved!"
 * indicator, "Use mine" clears the conflict banner) AROUND this — they don't
 * have to remember the four core invariants.
 *
 * The four invariants:
 *   1. Advance `baselineUpdatedAtRef` so the next save's optimistic-lock check
 *      doesn't 409 against our own previous write.
 *   2. Patch the React Query cache synchronously (via `advanceTranslationInCache`)
 *      so the activate `useEffect` re-captures from a fresh `updated_at` when
 *      the user re-activates this row before the invalidation refetch lands.
 *   3. Sync `originalText` to the saved `active_text` so dirty detection
 *      (`editText !== originalText`) reflects post-save state.
 *   4. Clear `dirtyRef.current` so the next blur/flush/auto-save isn't a
 *      redundant write that races our own onSuccess.
 *
 * Returns nothing — caller drives further side effects.
 */
export function applySegmentSaveSuccess(args: {
  queryClient: QueryClient
  jobId: number
  saved: Translation | undefined
  baselineUpdatedAtRef: { current: string | null }
  dirtyRef: { current: boolean }
  setOriginalText: (text: string) => void
}): void {
  const { queryClient, jobId, saved, baselineUpdatedAtRef, dirtyRef, setOriginalText } = args
  if (saved?.updated_at) baselineUpdatedAtRef.current = saved.updated_at
  // The ONLY legitimate call site for advanceTranslationInCache. The lint
  // rule (eslint.config.js) pins this — everyone else must use this helper.
  // eslint-disable-next-line no-restricted-syntax
  if (saved) advanceTranslationInCache(queryClient, jobId, saved)
  if (saved?.active_text != null) setOriginalText(saved.active_text)
  dirtyRef.current = false
  // A confirm appends a `human` row to the segment's history, and a draft save
  // that takes an approval away appends an `unconfirmed` one. Both land here, so
  // one call site covers them; duplicating it across the three save paths would
  // not.
  if (saved) {
    void queryClient.invalidateQueries({ queryKey: queryKeys.segmentHistory.byTranslation(saved.id) })
  }
}

/**
 * Patch the React Query cache for siblings updated by auto-propagation.
 * Called from `saveMut.onSuccess.then` after `propagateTranslation` returns
 * the full rows it touched on the server.
 *
 * Propagation bumps `updated_at` on sibling rows server-side, but the React
 * Query cache holds the pre-propagation `updated_at` until the async
 * invalidation refetch resolves. Without this patch, a user re-activating a
 * propagated sibling within that window captures a stale
 * `baselineUpdatedAtRef` and gets a false-409 on the next save.
 *
 * Distinct from `applySegmentSaveSuccess`: siblings are NOT the active row.
 * No `dirtyRef` to clear, no `originalText` to sync, no `baselineUpdatedAtRef`
 * to advance — those refs live on each row's own SegmentRow instance and we
 * don't have access to them from the saving row's context. We only patch
 * the SHARED cache (translations list); when the user later activates a
 * sibling, its own activate effect captures the now-fresh `updated_at` from
 * the patched cache and the per-row refs initialise correctly.
 */
export function patchPropagatedSiblings(args: {
  queryClient: QueryClient
  jobId: number
  siblings: Translation[]
}): void {
  const { queryClient, jobId, siblings } = args
  for (const sibling of siblings) {
    // Same lint exemption pattern as applySegmentSaveSuccess: this helper IS
    // the chokepoint for sibling-cache patching after propagation.
    // eslint-disable-next-line no-restricted-syntax
    advanceTranslationInCache(queryClient, jobId, sibling)
  }
  // Each sibling gained a `propagation` history row. One prefix invalidation,
  // not one per sibling: invalidateQueries scans the whole query cache per
  // call, and a repetition-heavy file propagates to hundreds of rows at once.
  if (siblings.length > 0) {
    void queryClient.invalidateQueries({ queryKey: queryKeys.segmentHistory.all })
  }
}

/**
 * Patch the cache with the row a segment hold returned, when it is newer than
 * the cached one: a segment someone else was editing shows their final text
 * before it turns editable here. The editor's re-sync rule keeps any unsaved
 * typing.
 */
export function applyAcquiredRow(queryClient: QueryClient, jobId: number, row: Translation): void {
  const cached = queryClient.getQueryData<Translation[]>(queryKeys.job.translations(jobId))
  const current = cached?.find(r => r.id === row.id)
  if (!current || !(Date.parse(row.updated_at ?? '') > Date.parse(current.updated_at ?? ''))) return
  // The hold's own chokepoint into the row cache, like patchPropagatedSiblings.
  // eslint-disable-next-line no-restricted-syntax
  advanceTranslationInCache(queryClient, jobId, row)
}

