// Find and replace over the files of an editor: each request goes to the job
// of the rows it touches, through the job's own find-replace endpoint, which
// keeps its task, lock and hold checks. A file whose task refuses the write is
// reported and the other files are still written.

import type { ApiError, ReplaceResult } from '../api'
import { mapChunks } from './segment-bulk'
import { sumReplaceCounts, replaceSummary, type ReplaceCounts } from './find-replace'
import { groupByJob } from './editor-scope'
import { count } from './plural'
import { isTaskLockedError } from './task-edit'

/** The editor's jobs, in grid order, and the job of each of its translations. */
export interface FindReplaceJobs {
  jobIds: readonly number[]
  jobOf: (translationId: number) => number | undefined
}

/** What a replace did across the files. */
export interface ScopeReplaceResult {
  result: ReplaceCounts
  /** The jobs a write reached. */
  writtenJobs: number[]
  /** The jobs whose completed task a write reopened. */
  reopenedJobs: number[]
  /** The jobs whose task refused the write. */
  locked: { jobId: number; err: ApiError }[]
  /** The jobs whose write failed for another reason. */
  failed: unknown[]
}

/** Each job with its own rows of `ids`, or every job with no ids. */
function perJob(jobs: FindReplaceJobs, ids: readonly number[] | undefined): [number, number[] | undefined][] {
  if (!ids) return jobs.jobIds.map(j => [j, undefined])
  const { byJob } = groupByJob(ids, id => {
    const jobId = jobs.jobOf(id)
    return jobId === undefined ? undefined : { jobId, canEdit: true }
  })
  return jobs.jobIds.flatMap(j => (byJob.has(j) ? [[j, byJob.get(j)]] : []))
}

/** Jobs asked at once: a large editor needs no hundred sequential round trips,
 *  and the server is not flooded either. */
const JOBS_AT_ONCE = 4

/** Runs fn on each job, on all its rows or on its selected ones in chunks the
 *  server accepts; a few jobs at a time, results in job order. */
async function eachJob<R>(jobs: FindReplaceJobs, ids: readonly number[] | undefined,
  fn: (jobId: number, ids?: number[]) => Promise<R>): Promise<[number, R[] | Error][]> {
  const todo = perJob(jobs, ids)
  const out: [number, R[] | Error][] = new Array(todo.length)
  let next = 0
  const worker = async () => {
    while (next < todo.length) {
      const i = next++
      const [jobId, own] = todo[i]
      try {
        out[i] = [jobId, own ? await mapChunks(own, part => fn(jobId, part)) : [await fn(jobId)]]
      } catch (err) {
        out[i] = [jobId, err as Error]
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(JOBS_AT_ONCE, todo.length) }, worker))
  return out
}

/** The matching translation ids of every job, in grid order. A failed job fails the find. */
export async function findInJobs(jobs: FindReplaceJobs, find: (jobId: number, ids?: number[]) => Promise<number[]>,
  ids?: readonly number[]): Promise<number[]> {
  const found: number[] = []
  for (const [, r] of await eachJob(jobs, ids, find)) {
    if (r instanceof Error) throw r
    found.push(...r.flat())
  }
  return found
}

function collect(parts: [number, ReplaceResult[] | Error][]): ScopeReplaceResult {
  const out: ScopeReplaceResult = { result: sumReplaceCounts([]), writtenJobs: [], reopenedJobs: [], locked: [], failed: [] }
  const results: ReplaceResult[] = []
  for (const [jobId, r] of parts) {
    if (r instanceof Error) {
      if (isTaskLockedError(r)) out.locked.push({ jobId, err: r })
      else out.failed.push(r)
      continue
    }
    results.push(...r)
    out.writtenJobs.push(jobId)
    if (r.some(p => p.taskReopened)) out.reopenedJobs.push(jobId)
  }
  out.result = sumReplaceCounts(results)
  // Nothing written and nothing refused by a task: the error says why.
  if (out.writtenJobs.length === 0 && out.locked.length === 0 && out.failed.length > 0) throw out.failed[0]
  return out
}

/** Replace all in every job, or in the selected rows of each. */
export async function replaceAllInJobs(jobs: FindReplaceJobs, replace: (jobId: number, ids?: number[]) => Promise<ReplaceResult>,
  ids?: readonly number[]): Promise<ScopeReplaceResult> {
  return collect(await eachJob(jobs, ids, replace))
}

/** Replace in one row, through its job. */
export async function replaceOneInJob(jobs: FindReplaceJobs, translationId: number,
  replace: (jobId: number, translationId: number) => Promise<ReplaceResult>): Promise<ScopeReplaceResult> {
  const jobId = jobs.jobOf(translationId)
  if (jobId === undefined) return collect([])
  return collect(await eachJob({ jobIds: [jobId], jobOf: jobs.jobOf }, undefined, j => replace(j, translationId)))
}

/** The result line, with the files left out. */
export function scopeReplaceSummary(out: ScopeReplaceResult): string {
  const parts = [replaceSummary(out.result)]
  if (out.locked.length > 0) parts.push(`${count(out.locked.length, 'file')} you cannot edit now`)
  if (out.failed.length > 0) parts.push(`${count(out.failed.length, 'file')} could not be saved, try again`)
  return parts.join(' · ')
}
