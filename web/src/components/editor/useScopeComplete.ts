import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { completeEditorScope, type ScopeCompleteResult, type ScopeFile } from '@/api'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { SCOPE_JOBS_PER_REQUEST, type EditorId } from '@/lib/editor-scope'
import { invalidateJobsContext, invalidateProjectStatus } from '@/lib/query-invalidation'
import { chunk } from '@/lib/segment-bulk'
import { OutcomeAdvanced, OutcomeCompleted } from '@/types/scopecomplete'

interface Options {
  editorId: EditorId
  projectId: number
  files: readonly ScopeFile[]
  isManager: boolean
  currentUserId: number | null
  /** Runs before the first request (closes the open segment). */
  beforeSend: () => Promise<unknown>
  /** Every file completed. */
  onAllDone: () => void
}

/**
 * The multi-file Complete step: which files it may complete, the dialog's
 * state, and the batched requests. The files and the step each was on are
 * taken when the dialog opens; a retry sends only the files that failed, with
 * those same step ids, so a file a lost answer already moved on is refused
 * instead of completing its next step too.
 */
export function useScopeComplete({ editorId, projectId, files, isManager, currentUserId, beforeSend, onAllDone }: Options) {
  const queryClient = useQueryClient()
  const completable = useMemo(() => files.filter(f =>
    f.step_name && f.edit_block?.code !== 'ai_step_running' && (isManager || f.step_assignee === currentUserId),
  ), [files, isManager, currentUserId])
  const [open, setOpen] = useState(false)
  const [failures, setFailures] = useState<ScopeCompleteResult[] | null>(null)
  const [targets, setTargets] = useState<ScopeFile[]>([])
  const openDialog = () => {
    setFailures(null)
    setTargets(completable)
    setOpen(true)
  }
  const invalidateCompleted = () => {
    invalidateJobsContext(queryClient, projectId, targets.map(f => f.job_id))
    invalidateProjectStatus(queryClient, projectId)
  }
  const mutation = useMutationWithToast({
    mutationFn: async ({ comment, approveAll }: { comment: string; approveAll: boolean }) => {
      await beforeSend()
      // In batches, so no one request runs long.
      const results: ScopeCompleteResult[] = []
      for (const part of chunk(targets, SCOPE_JOBS_PER_REQUEST)) {
        results.push(...await completeEditorScope(editorId, {
          comment,
          approve_all: approveAll,
          job_ids: part.map(f => f.job_id),
          steps: Object.fromEntries(part.flatMap(f => (f.step_id ? [[f.job_id, f.step_id]] : []))),
        }))
      }
      return results
    },
    successMessage: results => {
      const done = results.filter(r => r.outcome === OutcomeAdvanced || r.outcome === OutcomeCompleted).length
      return done > 0 ? `Step completed for ${done} ${done === 1 ? 'file' : 'files'}.` : null
    },
    errorMessage: 'Complete step failed',
    // A failed batch may follow batches that went through.
    onError: () => invalidateCompleted(),
    onSuccess: results => {
      invalidateCompleted()
      const failed = results.filter(r => r.outcome !== OutcomeAdvanced && r.outcome !== OutcomeCompleted)
      if (failed.length === 0) {
        setOpen(false)
        onAllDone()
        return
      }
      if (failed.length === results.length) toast.warning('No file was completed.')
      setFailures(failed)
      const failedIds = new Set(failed.map(r => r.job_id))
      setTargets(prev => prev.filter(f => failedIds.has(f.job_id)))
    },
  })

  return { completable, open, setOpen, openDialog, targets, failures, mutation }
}
