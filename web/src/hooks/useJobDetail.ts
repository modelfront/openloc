import { useIsMutating, useQuery } from '@tanstack/react-query'
import { getJob } from '../api'
import { queryKeys } from '../lib/query-keys'
import { isPipelineRunning } from '../lib/project-logic'

/** How often the job is read again while a run is going. */
export const JOB_POLL_MS = 2000

/**
 * The CAT editor's job. It is read again every JOB_POLL_MS while a pipeline
 * runs, so the "Translating N / M" counter and everything that follows it stay
 * live. isPipelineRunning is current_step-aware: polling stops once the
 * pipeline writes a "* complete" label, though pipeline_status stays
 * 'in_progress' until Complete.
 *
 * It is also read while this editor has a request open that runs a phase
 * (Apply TM/MT, AI step): the run happens inside that request, so a job that
 * was stopped or finished when it started would otherwise not be read again
 * until the run ends.
 */
export function useJobDetail(jobId: number) {
  const runRequested = useIsMutating({ mutationKey: queryKeys.job.runRequest(jobId) }) > 0
  return useQuery({
    queryKey: queryKeys.job.detail(jobId),
    queryFn: () => getJob(jobId),
    refetchInterval: (query) => {
      const j = query.state.data
      return runRequested || (j && isPipelineRunning(j)) ? JOB_POLL_MS : false
    },
  })
}
