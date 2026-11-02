import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { queryKeys } from '../lib/query-keys'
import { isAIStepRunning, isPipelineRunning } from '../lib/project-logic'

/** The fewest milliseconds between two reloads while a run is going. */
export const LIVE_REFRESH_MS = 5000

/** Each reload reads the whole index, so a bigger file waits longer between
 *  reloads: LIVE_REFRESH_MS per 10,000 segments, at most 30 seconds. */
export function liveRefreshMs(segmentCount: number): number {
  return Math.min(30_000, LIVE_REFRESH_MS * Math.max(1, Math.ceil(segmentCount / 10_000)))
}

interface JobProgress {
  pipeline_status: string
  current_step?: string | null
  translated_count?: number | null
}

/**
 * Keeps the CAT editor's index and the rows on screen up to date with a
 * pipeline run. While TM or MT fills segments, each new progress report the
 * 2-second job poll brings reloads them, at most once per liveRefreshMs, so
 * the status counts follow the "Translating N / M" counter and new text shows
 * up in the grid. A report that changes nothing reloads nothing. When the run
 * ends, everything is reloaded once more, and so are the job's steps, since an
 * AI step may have completed or moved the task.
 *
 * Reloading a page never overwrites a segment the user is typing in: a row
 * with unsaved text keeps it.
 */
export function usePipelineEditorRefresh(jobId: number, job: JobProgress | undefined, segmentCount: number) {
  const queryClient = useQueryClient()
  const running = !!job && isPipelineRunning(job)
  // The AI step hides the grid behind a waiting screen, so nothing to follow.
  const progress = running && !isAIStepRunning(job)
    ? `${job.current_step ?? ''}|${job.translated_count ?? ''}`
    : null

  const wasRunning = useRef(false)
  const lastProgress = useRef<string | null>(null)
  const lastReload = useRef(0)
  const pending = useRef<ReturnType<typeof setTimeout> | null>(null)
  const interval = liveRefreshMs(segmentCount)

  useEffect(() => {
    const clearPending = () => {
      if (pending.current) clearTimeout(pending.current)
      pending.current = null
    }
    if (running) {
      wasRunning.current = true
    } else if (wasRunning.current) {
      wasRunning.current = false
      lastProgress.current = null
      clearPending()
      queryClient.invalidateQueries({ queryKey: queryKeys.job.translations(jobId) })
      queryClient.invalidateQueries({ queryKey: queryKeys.job.steps(jobId) })
      return
    }

    // The first report seen is what the editor loaded with.
    if (progress === null || lastProgress.current === null || progress === lastProgress.current) {
      lastProgress.current = progress
      return
    }
    lastProgress.current = progress
    if (pending.current) return
    const reload = () => {
      pending.current = null
      lastReload.current = Date.now()
      queryClient.invalidateQueries({ queryKey: queryKeys.job.editorIndex(jobId) })
      queryClient.invalidateQueries({ queryKey: queryKeys.job.editorRows(jobId) })
    }
    const wait = lastReload.current + interval - Date.now()
    if (wait <= 0) reload()
    else pending.current = setTimeout(reload, wait)
  }, [running, progress, interval, jobId, queryClient])

  useEffect(() => () => {
    if (pending.current) clearTimeout(pending.current)
  }, [])
}
