import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { getEditorQAReport, runEditorQA, type QAReport } from '@/api'
import type { ScopeRunJob } from '@/types/qa'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { SCOPE_JOBS_PER_REQUEST, type EditorId } from '@/lib/editor-scope'
import { mergeScopeRuns, qaRunToast, qaSelectionToast, scopeQARunWarning } from '@/lib/qa-findings'
import { queryKeys } from '@/lib/query-keys'
import { mapChunks } from '@/lib/segment-bulk'

interface RunOutcome {
  report: QAReport
  results: ScopeRunJob[]
  ids: number[] | undefined
}

/**
 * Run QA in the multi-file editor: on every file, or on a selection. Each job
 * is checked on its own; the fresh report replaces the cached one, and a file
 * QA could not check is named.
 */
export function useScopeQARun({ editorId, jobIds, fileName, beforeRun }: {
  editorId: EditorId
  /** The editor's jobs: a run on every file goes SCOPE_JOBS_PER_REQUEST at a time. */
  jobIds: readonly number[]
  fileName: (jobId: number) => string
  /** Saves the open segment first: QA reads the stored text. */
  beforeRun: () => Promise<unknown>
}) {
  const queryClient = useQueryClient()
  return useMutationWithToast<RunOutcome, number[] | undefined>({
    mutationFn: async ids => {
      await beforeRun()
      const results = ids
        ? mergeScopeRuns(await mapChunks(ids, part => runEditorQA(editorId, { translationIds: part })))
        : (await mapChunks(jobIds, part => runEditorQA(editorId, { jobIds: part }), SCOPE_JOBS_PER_REQUEST)).flat()
      return { report: await getEditorQAReport(editorId), results, ids }
    },
    successMessage: ({ report, ids }) => (ids ? qaSelectionToast(report.results, ids) : qaRunToast(report.summary)),
    errorMessage: 'QA failed',
    onSuccess: ({ report, results }) => {
      queryClient.setQueryData<QAReport>(queryKeys.job.qaReport(editorId), report)
      for (const r of results) queryClient.invalidateQueries({ queryKey: queryKeys.job.analysis(r.job_id) })
      const warning = scopeQARunWarning(results, fileName)
      if (warning) toast.warning(warning)
    },
  })
}
