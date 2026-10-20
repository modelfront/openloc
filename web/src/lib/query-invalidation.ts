import { type QueryClient } from '@tanstack/react-query'
import { queryKeys } from './query-keys'
import type { Translation } from '../api/projects'
import type { TMEntry } from '../api/tms'

/**
 * Patch the cached translations list for a job with the latest `updated_at`
 * (and any other fields) from a successful save. Call this from `saveMut.onSuccess`
 * BEFORE `invalidateJobContext` so the cached prop reflects the new value
 * immediately — refetches that arrive later just confirm what's already there.
 *
 * Without this, the cache holds the old `updated_at` until the refetch lands;
 * re-activating the segment in that window regresses `baselineUpdatedAtRef`
 * and the next save 409s against the user's OWN previous write.
 *
 * Test: `web/src/lib/query-invalidation.test.ts::advanceTranslationInCache`.
 */
export function advanceTranslationInCache(
  queryClient: QueryClient,
  jobId: number,
  saved: Translation,
) {
  queryClient.setQueryData(
    queryKeys.job.translations(jobId),
    // listJobTranslations unwraps `r.data`, so the cache holds Translation[]
    // directly — NOT `{ data: Translation[] }`. A shape mismatch makes this
    // helper a silent no-op.
    (old: Translation[] | undefined) => {
      if (!Array.isArray(old)) return old
      let changed = false
      const next = old.map(r => {
        if (r.id !== saved.id) return r
        changed = true
        // Spread saved over r so we keep any fields the response omits
        // while the response wins for `updated_at`, `active_text`,
        // `active_rich_text`, `status`, etc.
        return { ...r, ...saved }
      })
      return changed ? next : old
    },
  )
}

/**
 * Every cache that holds a project object, and therefore its `status`.
 *
 * Exported as a key list so the declarative `invalidateKeys` of
 * useMutationWithToast and the imperative helper below share one source of
 * truth — a project-shape cache added in one and missed in the other is the
 * bug class this prevents.
 */
export function projectCacheKeys(projectId: number) {
  return [
    // ProjectDetail reads `project` out of the legacy list.
    queryKeys.projects.all,
    // The /projects useInfiniteQuery cache — prefix-matches every filter variant.
    queryKeys.projects.paginated,
    // getProjectSettings embeds the full project inside `settings.project`.
    queryKeys.project.settings(projectId),
    // Status changes emit an audit event; the History tab can be mounted, so
    // its refetch-on-mount alone wouldn't show the new row.
    queryKeys.projectEvents.byProject(projectId),
  ]
}

/**
 * Call after any mutation that changes a project's set of jobs — completing a
 * job, deleting a job, deleting a file. Auto-complete can
 * flip `projects.status` behind any of those, and the new status is only
 * visible once these caches refetch.
 *
 * Deliberately NOT folded into invalidateJobContext: that runs on every
 * segment confirm/save, and refetching the project list on each keystroke-rate
 * save would be a lot of wasted traffic for a status that can't have changed.
 */
export function invalidateProjectStatus(queryClient: QueryClient, projectId: number) {
  for (const queryKey of projectCacheKeys(projectId)) {
    queryClient.invalidateQueries({ queryKey })
  }
}

/**
 * Invalidate all job-related queries. Call after any mutation that changes
 * job state: confirm translation, complete job, advance workflow step, run QA.
 */
export function invalidateJobContext(queryClient: QueryClient, projectId: number, jobId: number) {
  queryClient.invalidateQueries({ queryKey: queryKeys.job.detail(jobId) })
  queryClient.invalidateQueries({ queryKey: queryKeys.job.translations(jobId) })
  queryClient.invalidateQueries({ queryKey: queryKeys.job.analysis(jobId) })
  queryClient.invalidateQueries({ queryKey: queryKeys.job.steps(jobId) })
  // A step change resets or restores every segment's status and closes a
  // step round, so the previous-step chips move with it.
  queryClient.invalidateQueries({ queryKey: queryKeys.job.stepStatuses(jobId) })
  // CAT-editor status bar — flips its needs_review counts the moment a
  // confirm transitions a segment's status. Without this, the bar only
  // updated on its 15s refetchInterval, so Cmd+Enter spam confirmed
  // segments faster than the bar could observe them.
  queryClient.invalidateQueries({ queryKey: queryKeys.job.leverage(jobId) })
  // Stored QA findings follow the translations they describe: Clear and
  // Apply TM/MT replace the targets they were computed on, and the server
  // drops the findings of the rows they touched. The confirm path patches the
  // cache itself (patchQAReport cancels this refetch first), so the refetch
  // here is the catch-all, not the fast path.
  queryClient.invalidateQueries({ queryKey: queryKeys.job.qaReport(jobId) })
  // Every job-level write that replaces target text — Apply TM, Apply MT, an
  // AI step re-run, a clear — appends history rows. A prefix invalidation
  // because the caller does not know which translations were touched; the
  // panel is gated on being open, so at most one query actually refetches.
  queryClient.invalidateQueries({ queryKey: queryKeys.segmentHistory.all })
  queryClient.invalidateQueries({ queryKey: queryKeys.jobs.byProject(projectId) })
  queryClient.invalidateQueries({ queryKey: queryKeys.files.byProject(projectId) })
  queryClient.invalidateQueries({ queryKey: queryKeys.projectAnalysis.v2(projectId) })
  // Refresh the CAT-editor TM-matches sidebar. A confirm writes the
  // segment's source→target to the TM, so the active segment's matches change.
  // The prefix covers every cached source for the job, so the open segment
  // refetches and shows the just-written entry immediately.
  queryClient.invalidateQueries({ queryKey: queryKeys.tmMatches.byJobPrefix(jobId) })
  // The QE+APE step writes AI-authored comments, so a
  // pipeline run can add comments to segments the user never touched, and a
  // clear removes the ones describing text it just threw away. Refresh both
  // the per-file counts behind the segment-grid indicator and the open
  // panel's own list — the counts alone left a deleted comment on screen.
  queryClient.invalidateQueries({ queryKey: queryKeys.comments.allCounts })
  queryClient.invalidateQueries({ queryKey: queryKeys.comments.all })
}

/**
 * Rewrite the rows of every cached `useInfiniteQuery` list under `keyPrefix`,
 * in place of refetching it — a refetch reloads every page the user scrolled
 * through.
 *
 * Cache shape: `{ pages: Array<{ data: T[], cursor, has_more, total? }>, pageParams }`.
 * A list has one cache per filter combination, and all of them must change
 * together, or switching filters shows the old row. Caches under the prefix
 * with another shape (a detail query) are left alone.
 *
 * `patch` returns the page's rows unchanged (the same array) when it has
 * nothing to do. Rows it drops lower every page's `total` by as many; `total`
 * is the whole list's count, and some lists leave it out.
 */
export function patchInfiniteRows<T>(
  queryClient: QueryClient,
  keyPrefix: readonly unknown[],
  patch: (rows: T[]) => T[],
) {
  queryClient.setQueriesData({ queryKey: keyPrefix }, (old: unknown) => {
    if (!old || typeof old !== 'object') return undefined
    const data = old as { pages?: unknown; pageParams?: unknown }
    if (!Array.isArray(data.pages)) return undefined

    let changed = false
    let removed = 0
    const patched = data.pages.map((page) => {
      if (!page || typeof page !== 'object') return page
      const p = page as { data?: unknown }
      if (!Array.isArray(p.data)) return page
      const rows = patch(p.data as T[])
      if (rows === p.data) return page
      changed = true
      removed += p.data.length - rows.length
      return { ...page, data: rows }
    })
    // Undefined leaves the cache, and its timestamps, untouched.
    if (!changed) return undefined
    const pages = removed === 0 ? patched : patched.map((page) => {
      const t = (page as { total?: unknown } | null)?.total
      return typeof t === 'number' ? { ...(page as object), total: Math.max(0, t - removed) } : page
    })
    return { ...data, pages }
  })
}

/**
 * The TMBrowser counterpart of `advanceTranslationInCache`: without
 * it, re-editing the same cell before the refetch lands reads a stale
 * `updated_at` and the next save 409s against the user's own previous write.
 * It patches every filter combination's cache of the TM, and no other TM's.
 */
export function advanceTMEntryInCache(
  queryClient: QueryClient,
  tmId: number,
  saved: TMEntry,
) {
  patchInfiniteRows<TMEntry>(queryClient, queryKeys.tm.allEntries(tmId), (rows) =>
    rows.some(r => r.id === saved.id)
      ? rows.map(r => (r.id === saved.id ? { ...r, ...saved } : r))
      : rows,
  )
}

/**
 * Take a deleted entry out of every cached entry list of its TM and lower the
 * cached total, then refresh only what a delete can still change: the TM
 * list's entry counts, entry history, and the language pairs (the entry may
 * have been its pair's last). The list itself is not refetched — that would
 * reload every loaded page. The list cursor is the last entry id, so removing
 * a row does not shift the next page.
 */
export function removeTMEntryFromCache(
  queryClient: QueryClient,
  tmId: number,
  entryId: number,
) {
  patchInfiniteRows<TMEntry>(queryClient, queryKeys.tm.allEntries(tmId), (rows) =>
    rows.some(r => r.id === entryId) ? rows.filter(r => r.id !== entryId) : rows,
  )
  queryClient.invalidateQueries({ queryKey: queryKeys.tms.all })
  queryClient.invalidateQueries({ queryKey: ['tm-entry-history'] })
  queryClient.invalidateQueries({ queryKey: queryKeys.tm.langPairs(tmId) })
}

/**
 * Invalidate TM-level queries. Call after TM entry changes (import, job
 * complete with TM write-back, inline edit). A single-entry delete
 * uses `removeTMEntryFromCache` instead.
 *
 * Also invalidates per-entry history queries (prefix `tm-entry-history`) so an
 * open TMEntryHistoryDialog refetches after the user makes another edit. The
 * prefix invalidation is intentional — every entry-history query under any
 * (tmId, entryId) shares the prefix; we don't track which one might be open.
 */
export function invalidateTMContext(queryClient: QueryClient, tmId?: number) {
  queryClient.invalidateQueries({ queryKey: queryKeys.tms.all })
  queryClient.invalidateQueries({ queryKey: ['tm-entry-history'] })
  if (tmId) {
    queryClient.invalidateQueries({ queryKey: queryKeys.tm.allEntries(tmId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.tm.langPairs(tmId) })
  }
}

/**
 * Fan-out invalidation after a segment confirm wrote to one or more TMs.
 * The backend response (`updated_tm_ids`) carries the
 * exact list of TMs that received the new entry; we invalidate each one's
 * caches independently. Calling this when `tmIds` is empty is a safe
 * no-op — a save that didn't write to any TM (no profile assignment, all
 * upserts errored) doesn't need to invalidate anything.
 *
 * Global keys once, then per-TM keys: `tms.all` and `tm-entry-history` are
 * invalidated before the loop so the call count stays bounded for a large
 * `tmIds` list. Mirrors `invalidateJobContext`: one call, multiple downstream
 * invalidations.
 */
export function invalidateUpdatedTMs(queryClient: QueryClient, tmIds: number[]) {
  if (tmIds.length === 0) return
  // Global keys once, outside the loop.
  queryClient.invalidateQueries({ queryKey: queryKeys.tms.all })
  queryClient.invalidateQueries({ queryKey: ['tm-entry-history'] })
  for (const tmId of tmIds) {
    queryClient.invalidateQueries({ queryKey: queryKeys.tm.allEntries(tmId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.tm.langPairs(tmId) })
  }
}

// Throttle state for `notifyTMSaved`. Module-scoped so all save call sites
// (CAT confirm, "use mine" conflict resolution) share one window. Testable
// via the exported `__resetTMSavedThrottleForTest` (vitest only).
let lastTMSavedToastAt = 0
const TM_SAVED_TOAST_THROTTLE_MS = 3000

/**
 * Show a "TM updated" toast after a CAT confirm wrote to one or more TMs, so
 * the user sees the entry landed without opening the TM browser. The 3-second
 * throttle keeps back-to-back confirms from stacking toasts.
 *
 * Separate from `invalidateUpdatedTMs` so cache invalidation stays a pure
 * concern that other callers can use without notifying the user.
 */
export function notifyTMSaved(tmIds: number[], notify: (msg: string) => void) {
  if (tmIds.length === 0) return
  const now = Date.now()
  if (now - lastTMSavedToastAt < TM_SAVED_TOAST_THROTTLE_MS) return
  lastTMSavedToastAt = now
  // One fixed message regardless of count — non-spammy + locale-friendly.
  notify('TM updated')
}

/** Test-only: reset the throttle window. Production code never imports this. */
export function __resetTMSavedThrottleForTest() {
  lastTMSavedToastAt = 0
}

/**
 * Bundle the FE side effects that follow a successful segment confirm:
 * invalidate TM caches + notify the user.
 *
 * Both the main save path (`saveMut.mutationFn`) and the conflict-resolution
 * "use mine" path call this after `applySegmentSaveSuccess`, so neither can
 * get one side effect without the other. Mirrors `applySegmentSaveSuccess`'s
 * role of pinning the post-save invariants on the row itself.
 *
 * `notify` is injected (vs imported from sonner inside the helper) so
 * unit tests can assert call patterns without a sonner mock; production
 * passes `toast.success`.
 */
export function applyTMUpdatesAfterSave(
  queryClient: QueryClient,
  tmIds: number[],
  notify: (msg: string) => void,
) {
  invalidateUpdatedTMs(queryClient, tmIds)
  notifyTMSaved(tmIds, notify)
}
