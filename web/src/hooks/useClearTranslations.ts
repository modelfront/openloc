import { useQueryClient } from '@tanstack/react-query'

import {
  clearJobTranslations,
  clearProjectTranslations,
  clearTranslation,
  type ClearScope,
} from '../api'
import { invalidateJobContext } from '../lib/query-invalidation'
import { queryKeys } from '../lib/query-keys'
import { warnHeldSkipped } from '../lib/segment-bulk'
import { useMutationWithToast } from './use-mutation-with-toast'
import { handleTaskReopened, taskLockFailure } from '../lib/task-edit'

function clearedToast(n: number): string {
  if (n === 0) return 'No translations to clear'
  return n === 1 ? 'Cleared 1 translation' : `Cleared ${n} translations`
}

export function useClearSegment(args: { fileId: number; jobId: number; projectId: number }) {
  const queryClient = useQueryClient()
  return useMutationWithToast({
    mutationFn: (translationId: number) => clearTranslation(args.fileId, translationId),
    successMessage: 'Translation cleared',
    onSuccess: ({ taskReopened }) => {
      invalidateJobContext(queryClient, args.projectId, args.jobId)
      if (taskReopened) handleTaskReopened(queryClient, args.projectId, args.jobId)
    },
    ...taskLockFailure(queryClient, args.projectId, args.jobId),
  })
}

export function useClearJobTranslations(args: { jobId: number; projectId: number }) {
  const queryClient = useQueryClient()
  return useMutationWithToast({
    mutationFn: (scope: ClearScope) => clearJobTranslations(args.jobId, scope),
    successMessage: data => clearedToast(data.cleared),
    onSuccess: (data) => {
      invalidateJobContext(queryClient, args.projectId, args.jobId)
      warnHeldSkipped(data.held)
    },
  })
}

export function useClearProjectTranslations(projectId: number) {
  const queryClient = useQueryClient()
  return useMutationWithToast({
    mutationFn: (scope: ClearScope) => clearProjectTranslations(projectId, scope),
    successMessage: data => clearedToast(data.cleared),
    onSuccess: (data) => {
      // Sweeps every job-scoped cache — a project-wide clear can touch any of
      // them, and there is no single job id to invalidate. The job keys are
      // 'job' for the detail and 'job-<thing>' for the rest (translations,
      // qa-report, leverage, …), so both shapes have to match; 'jobs' (the
      // per-project list) must not, hence the '-' and not a bare startsWith.
      queryClient.invalidateQueries({
        predicate: (q) => {
          const root = Array.isArray(q.queryKey) ? q.queryKey[0] : null
          return typeof root === 'string' && (root === 'job' || root.startsWith('job-'))
        },
      })
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.byProject(projectId) })
      queryClient.invalidateQueries({ queryKey: queryKeys.files.byProject(projectId) })
      queryClient.invalidateQueries({ queryKey: queryKeys.projectAnalysis.v2(projectId) })
      // The clear dropped the AI comments of every row it touched; neither
      // key starts with 'job', so the sweep above misses both.
      queryClient.invalidateQueries({ queryKey: queryKeys.comments.all })
      queryClient.invalidateQueries({ queryKey: queryKeys.comments.allCounts })
      warnHeldSkipped(data.held)
    },
  })
}
