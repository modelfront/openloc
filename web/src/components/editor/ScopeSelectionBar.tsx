// The multi-file editor's bar for a segment selection: Confirm, Lock and
// Unlock, each sent per file to that file's job, with the single-file
// editor's per-row rules (lib/segment-bulk). Rows of a file the caller may not
// edit now are left out and counted. Join takes rows of one file; Run QA,
// Spell check and Find & replace run on the selection in every file.
// Pre-translate and Clear work on one file: open it on its own.

import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { bulkSegmentAction } from '@/api'
import { SegmentEditButtons, SelectionActionButton } from '@/components/SegmentSelectionBar'
import { SelectionBar, SelectionBarDivider } from '@/components/list/SelectionBar'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import type { IndexRow } from '@/lib/editor-index'
import { groupByJob } from '@/lib/editor-scope'
import { invalidateJobsContext } from '@/lib/query-invalidation'
import { bulkSummary, mapChunks, mergeResults, type SegmentBulkAction, type SegmentBulkResult } from '@/lib/segment-bulk'
import { selectionLabel } from '@/lib/segment-selection'
import { handleTaskReopened } from '@/lib/task-edit'
import type { RowFile } from './SegmentGrid'

interface Props {
  projectId: number
  /** The selected translations the filter shows. */
  picked: number[]
  selectedCount: number
  rowOf: (translationId: number) => IndexRow | undefined
  rowFile: (fileIdx: number) => RowFile
  isManager: boolean
  /** Saves the open segment first. */
  beforeRun: () => Promise<unknown>
  onClearSelection: () => void
  /** Joins the selected rows; absent when the editor offers no join. */
  onJoin?: () => void
  /** Why the selection cannot be joined, or null. */
  joinRefusal: string | null
  onRunQA: () => void
  qaRunning: boolean
  /** Why Run QA is off, if it is. */
  qaDisabledReason?: string
  onSpellCheck: () => void
  onFindReplace: () => void
}

export function ScopeSelectionBar({
  projectId, picked, selectedCount, rowOf, rowFile, isManager, beforeRun, onClearSelection,
  onJoin, joinRefusal, onRunQA, qaRunning, qaDisabledReason, onSpellCheck, onFindReplace,
}: Props) {
  const queryClient = useQueryClient()
  // Every file the selection touches, at once: one reload of the editor.
  const invalidatePicked = () => {
    const jobIds = new Set(picked.flatMap(id => {
      const row = rowOf(id)
      return row ? [rowFile(row.fileIdx).jobId] : []
    }))
    invalidateJobsContext(queryClient, projectId, [...jobIds])
  }
  const mut = useMutationWithToast<{ action: SegmentBulkAction; res: SegmentBulkResult; readOnly: number }, SegmentBulkAction>({
    mutationFn: async action => {
      await beforeRun()
      const { byJob, readOnly } = groupByJob(picked, id => { const row = rowOf(id); return row && rowFile(row.fileIdx) })
      const results: SegmentBulkResult[] = []
      for (const [jobId, ids] of byJob) {
        const res = mergeResults(await mapChunks(ids, part => bulkSegmentAction(jobId, action, part)))
        if (res.workflow_reopened || res.task_reopened) handleTaskReopened(queryClient, projectId, jobId)
        results.push(res)
      }
      return { action, res: mergeResults(results), readOnly }
    },
    successMessage: ({ action, res }) => (res.applied.length > 0 ? bulkSummary(action, res) : null),
    errorMessage: 'The selection could not be updated',
    onSuccess: ({ action, res, readOnly }) => {
      invalidatePicked()
      if (res.applied.length === 0) toast.warning(bulkSummary(action, res))
      if (readOnly > 0) toast.warning(`${readOnly} selected ${readOnly === 1 ? 'segment is' : 'segments are'} in files you cannot edit now and ${readOnly === 1 ? 'was' : 'were'} left as ${readOnly === 1 ? 'it was' : 'they were'}.`)
    },
    // An earlier file may have been written before the failure; a file whose
    // task refused the edit says why on its header once the index reloads.
    onError: () => invalidatePicked(),
  })
  const busy = mut.isPending || qaRunning
  const toolButton = (testId: string, label: string, onClick: () => void, offReason?: string) => (
    <SelectionActionButton testId={testId} label={label} onClick={onClick} offReason={offReason} busy={busy} nothingShown={picked.length === 0} />
  )
  return (
    <SelectionBar className="mb-2" label={selectionLabel(picked.length, selectedCount)} onClear={onClearSelection} busy={busy} testIdPrefix="segment">
      <SegmentEditButtons
        canConfirm
        canLock={isManager}
        canUnlock
        shown={picked.length}
        lockedCount={picked.filter(id => rowOf(id)?.locked).length}
        busy={busy}
        running={mut.isPending ? mut.variables : undefined}
        onRun={action => mut.mutate(action)}
      />
      {onJoin && toolButton('segment-bulk-join', 'Join', onJoin, joinRefusal ?? undefined)}
      <SelectionBarDivider />
      {toolButton('segment-bulk-qa', qaRunning ? 'Checking…' : 'Run QA', onRunQA, qaDisabledReason)}
      {toolButton('segment-bulk-spellcheck', 'Spell check', onSpellCheck)}
      {toolButton('segment-bulk-find-replace', 'Find & replace', onFindReplace)}
    </SelectionBar>
  )
}
