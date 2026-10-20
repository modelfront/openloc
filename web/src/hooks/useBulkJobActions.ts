import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { mapWithConcurrency } from '../lib/concurrency'
import { queryKeys } from '../lib/query-keys'
import { invalidateProjectStatus } from '../lib/query-invalidation'

// Client-side fan-out for bulk job actions. The per-job apply/assign/deadline
// endpoints are all single-job POSTs that enqueue async work; there is no
// batch endpoint, so the bulk bar loops
// over the selected jobs here, with a bounded concurrency cap, then does ONE
// round of cache invalidation at the end rather than a storm of per-job
// invalidations (each single-job hook invalidates job context; firing that N
// times would trigger N refetches of the same project lists).

// Cap concurrent in-flight requests. Each request only enqueues an async
// pipeline task, so this is about not hammering the server, not throughput.
const CONCURRENCY = 5

export interface BulkRunArgs<T> {
  /** Stable key for the action, used to drive per-button spinner state. */
  actionKey: string
  /** Human label for the toast summary, e.g. "Apply MT". */
  label: string
  /** Jobs the action will actually run against (already eligibility-filtered). */
  items: T[]
  /** Per-job worker — the api call. Throwing marks that job as failed. */
  worker: (item: T) => Promise<void>
  /** Count of selected jobs that were skipped as ineligible, with a reason. */
  skipped?: { count: number; reason: string }
  /** What `items` are, for the toast: 'job' (default), 'task' or 'file'. */
  unit?: 'job' | 'task' | 'file'
  /** What happened to each item, for the toast: 'done' (default), or 'queued'
   *  for an action that only schedules work. */
  outcome?: 'done' | 'queued'
}

export function useBulkJobActions(projectId: number) {
  const queryClient = useQueryClient()
  const [runningAction, setRunningAction] = useState<string | null>(null)

  const invalidate = () => {
    // Project-level keys touched by any of the bulk actions. Assign/deadline
    // also surface on the Tasks pages, so refresh those too.
    queryClient.invalidateQueries({ queryKey: queryKeys.jobs.byProject(projectId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.files.byProject(projectId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.projectAnalysis.v2(projectId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.tasks.all })
    // Complete step chains into /complete on a job's final step, and Delete
    // removes jobs outright — either can leave auto-complete holding a new
    // project status.
    invalidateProjectStatus(queryClient, projectId)
  }

  async function run<T>({ actionKey, label, items, worker, skipped, unit = 'job', outcome = 'done' }: BulkRunArgs<T>): Promise<void> {
    if (items.length === 0) {
      // Nothing eligible — tell the user why rather than silently no-op.
      toast.info(
        skipped && skipped.count > 0
          ? `${label}: nothing to do — ${skipped.count} selected ${unit}(s) ${skipped.reason}.`
          : `${label}: no eligible jobs selected.`,
      )
      return
    }

    setRunningAction(actionKey)
    try {
      const results = await mapWithConcurrency(items, CONCURRENCY, worker)
      const failedResults = results.filter(r => !r.ok)
      const ok = results.length - failedResults.length
      const failed = failedResults.length

      // Surface the shared backend reason (e.g. "No MT engine configured …
      // settings") instead of a generic "failed for all N" that says nothing.
      const failMsgs = [...new Set(failedResults.map(r => r.error?.message).filter(Boolean))]
      const reason = failMsgs.length === 1 ? failMsgs[0] : null

      const parts: string[] = []
      if (skipped && skipped.count > 0) parts.push(`${skipped.count} skipped (${skipped.reason})`)
      if (failed > 0) parts.push(`${failed} failed`)
      const suffix = parts.length > 0 ? ` — ${parts.join(', ')}` : ''

      if (ok === 0) {
        toast.error(
          reason ?? `${label} failed for all ${failed} ${unit}(s)${skipped && skipped.count > 0 ? ` (${skipped.count} skipped)` : ''}.`,
        )
      } else if (failed > 0) {
        toast.warning(`${label}: ${ok} ${unit}(s) ${outcome}${suffix}${reason ? ` — ${reason}` : ''}.`)
      } else {
        toast.success(`${label}: ${ok} ${unit}(s) ${outcome}${suffix}.`)
      }
    } finally {
      // Always refresh + clear the spinner, even if the whole batch threw.
      invalidate()
      setRunningAction(null)
    }
  }

  return { run, runningAction }
}
