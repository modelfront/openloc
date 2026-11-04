import { describe, expect, it, vi } from 'vitest'
import { ApiError, type ReplaceResult } from '../api'
import { findInJobs, replaceAllInJobs, replaceOneInJob, scopeReplaceSummary, type FindReplaceJobs } from './find-replace-scope'

const zero = { replaced: 0, segments: 0, locked: 0, held: 0, skipped_tags: 0, conflicts: 0, rejected: 0, failed: 0 }
const counts = (replaced: number, segments: number, taskReopened = false): ReplaceResult => ({ ...zero, replaced, segments, taskReopened })
const locked = () => new ApiError(409, 'The task is completed', { error: { code: 'task_completed' } })

// Job 1 holds translations 10 and 11, job 2 holds 20 and 21.
const jobs: FindReplaceJobs = { jobIds: [1, 2], jobOf: id => (id < 20 ? 1 : 2) }

describe('findInJobs', () => {
  it('asks each job and lists the matches in grid order', async () => {
    const find = vi.fn(async (jobId: number) => (jobId === 1 ? [11] : [20, 21]))
    expect(await findInJobs(jobs, find)).toEqual([11, 20, 21])
    expect(find.mock.calls).toEqual([[1], [2]])
  })

  it('in a selection, asks each job only for its own selected rows', async () => {
    const find = vi.fn(async (_jobId: number, ids?: number[]) => ids ?? [])
    expect(await findInJobs(jobs, find, [21, 10])).toEqual([10, 21])
    expect(find.mock.calls).toEqual([[1, [10]], [2, [21]]])
  })
})

describe('replaceOneInJob', () => {
  it('sends the replace to the row\'s job', async () => {
    const replace = vi.fn(async () => counts(1, 1, true))
    const out = await replaceOneInJob(jobs, 21, replace)
    expect(replace).toHaveBeenCalledWith(2, 21)
    expect(out.reopenedJobs).toEqual([2])
    expect(out.writtenJobs).toEqual([2])
  })
})

describe('replaceAllInJobs', () => {
  it('runs per job and adds the counts up', async () => {
    const replace = vi.fn(async (jobId: number) => (jobId === 1 ? counts(2, 1) : counts(3, 2, true)))
    const out = await replaceAllInJobs(jobs, replace)
    expect(out.result).toEqual({ ...zero, replaced: 5, segments: 3 })
    expect(out.writtenJobs).toEqual([1, 2])
    expect(out.reopenedJobs).toEqual([2])
    expect(scopeReplaceSummary(out)).toBe('Replaced 5 occurrences in 3 segments')
  })

  it('a locked file does not stop the others, and is reported', async () => {
    const err = locked()
    const replace = vi.fn(async (jobId: number) => {
      if (jobId === 1) throw err
      return counts(1, 1)
    })
    const out = await replaceAllInJobs(jobs, replace)
    expect(replace).toHaveBeenCalledTimes(2)
    expect(out.writtenJobs).toEqual([2])
    expect(out.locked).toEqual([{ jobId: 1, err }])
    expect(scopeReplaceSummary(out)).toBe('Replaced 1 occurrence in 1 segment · 1 file you cannot edit now')
  })

  it('in a selection, sends each job its own selected rows', async () => {
    const replace = vi.fn(async (_jobId: number, ids?: number[]) => counts(ids?.length ?? 0, ids?.length ?? 0))
    await replaceAllInJobs(jobs, replace, [20, 11, 21])
    expect(replace.mock.calls).toEqual([[1, [11]], [2, [20, 21]]])
  })

  it('throws when no file could be written for another reason', async () => {
    const boom = new Error('network down')
    await expect(replaceAllInJobs(jobs, async () => { throw boom })).rejects.toBe(boom)
  })

  it('a file that failed is counted while the others are written', async () => {
    const out = await replaceAllInJobs(jobs, async jobId => {
      if (jobId === 2) throw new Error('boom')
      return counts(1, 1)
    })
    expect(scopeReplaceSummary(out)).toBe('Replaced 1 occurrence in 1 segment · 1 file could not be saved, try again')
  })
})

describe('several jobs at once', () => {
  it('asks a few jobs in parallel and keeps the grid order', async () => {
    let running = 0
    let most = 0
    const many: FindReplaceJobs = { jobIds: [1, 2, 3, 4, 5, 6, 7, 8], jobOf: () => undefined }
    const find = async (jobId: number) => {
      running++
      most = Math.max(most, running)
      await new Promise(r => setTimeout(r, (9 - jobId) * 2))
      running--
      return [jobId * 10]
    }
    expect(await findInJobs(many, find)).toEqual([10, 20, 30, 40, 50, 60, 70, 80])
    expect(most).toBeGreaterThan(1)
    expect(most).toBeLessThanOrEqual(4)
  })
})
