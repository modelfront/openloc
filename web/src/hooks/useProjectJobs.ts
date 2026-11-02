import { useMemo } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { listJobs, type Job } from '../api'
import { queryKeys } from '../lib/query-keys'
import { isPipelineRunning } from '../lib/project-logic'

const POLL_MS = 2000

/**
 * A project's jobs. The full list loads once (and on every invalidation);
 * while pipelines run, a 2 s poll asks only for the running jobs and writes
 * them into the list. While `recentUpload` the full list is polled instead,
 * since new jobs appear only there.
 */
export function useProjectJobs(pid: number, recentUpload: boolean) {
  const queryClient = useQueryClient()
  const key = queryKeys.jobs.byProject(pid)
  const jobs = useQuery({
    queryKey: key,
    queryFn: () => listJobs(pid),
    refetchInterval: recentUpload ? POLL_MS : false,
  })
  const runningIds = useMemo(() => (jobs.data ?? []).filter(isPipelineRunning).map(j => j.id), [jobs.data])
  useQuery({
    // Under the list's key, so an invalidation of the list refetches this too.
    queryKey: [...key, 'running', runningIds],
    queryFn: async () => {
      const fresh = await listJobs(pid, { ids: runningIds })
      queryClient.setQueryData<Job[]>(key, old => mergeJobs(old, runningIds, fresh))
      return fresh
    },
    enabled: runningIds.length > 0 && !recentUpload,
    refetchInterval: POLL_MS,
  })
  return jobs
}

/**
 * `list` with each job in `fresh` replaced by its fresh copy, order kept. A
 * job asked for (`asked`) that the server no longer returns was deleted, so
 * it leaves the list.
 */
function mergeJobs(list: Job[] | undefined, asked: number[], fresh: Job[]): Job[] | undefined {
  if (!list) return list
  const byId = new Map(fresh.map(j => [j.id, j]))
  const gone = new Set(asked.filter(id => !byId.has(id)))
  return list.filter(j => !gone.has(j.id)).map(j => byId.get(j.id) ?? j)
}
