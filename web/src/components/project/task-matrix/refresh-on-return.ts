// The CAT editor opens in a new tab, where a job's progress, its steps and the
// project's status change. The Tasks tab's ways in (a step row, the expanded
// grid's Open) arm this, and the job and file lists refresh when the user
// comes back.

import { useQueryClient } from '@tanstack/react-query'

import { useRefreshOnReturn } from '@/hooks/useRefreshOnReturn'
import { invalidateProjectStatus } from '@/lib/query-invalidation'
import { queryKeys } from '@/lib/query-keys'

export function useProjectRefreshOnReturn(projectId: number): () => void {
  const queryClient = useQueryClient()
  return useRefreshOnReturn(() => {
    queryClient.invalidateQueries({ queryKey: queryKeys.jobs.byProject(projectId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.files.byProject(projectId) })
    invalidateProjectStatus(queryClient, projectId)
  })
}
