// The CAT editor's bar for a segment selection: count, clear, then the bulk
// actions the user may run here. Actions that make no sense for the role or
// the workflow step are hidden, not disabled.

import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { bulkSegmentAction, getQAReport, runQA, type QAReport, type Translation } from '../api'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { useJobPipelineActions } from '../hooks/useJobPipelineActions'
import { qaSelectionToast } from '../lib/qa-findings'
import { invalidateJobContext } from '../lib/query-invalidation'
import { queryKeys } from '../lib/query-keys'
import {
  bulkSummary, mapChunks, mergeResults,
  type SegmentBulkAction, type SegmentBulkResult,
} from '../lib/segment-bulk'
import { count } from '../lib/plural'
import { selectionLabel } from '../lib/segment-selection'
import { handleTaskReopened, taskLockFailure } from '../lib/task-edit'
import ConfirmDialog from './ConfirmDialog'
import { PretranslateItem } from './JobPipelineActions'
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { SelectionBar, SelectionBarDivider } from '@/components/list/SelectionBar'
import { Button } from '@/components/ui/button'

interface Props {
  jobId: number
  projectId: number
  /** Selected translation ids on screen, in display order — what actions run on. */
  picked: number[]
  /** Every selected id, including the ones the filter hides. */
  selectedCount: number
  translationsById: ReadonlyMap<number, Translation>
  /** Confirm: the task takes edits. */
  canConfirm: boolean
  /** Lock approves the segments: managers, while the task takes edits. */
  canLock: boolean
  /** Unlock: anyone, while the task takes edits. */
  canUnlock: boolean
  /** Clear: managers, on the first step, while the task takes edits. */
  canClear: boolean
  /** Pre-translate: first step, the task takes edits, no run holds the job. */
  canPretranslate: boolean
  /** Runs before any write or check: closes the open segment so its draft is
   *  saved first and its autosave can't race the batch. */
  beforeRun: () => Promise<void>
  /** Opens the spell check dialog on the selection. */
  onSpellCheck: () => void
  /** Opens Find & replace limited to the selection. */
  onFindReplace: () => void
  onClearSelection: () => void
}

const pending: Record<SegmentBulkAction, string> = { confirm: 'Confirming…', lock: 'Locking…', unlock: 'Unlocking…', clear: 'Clearing…' }

export function SegmentSelectionBar({
  jobId, projectId, picked, selectedCount, translationsById, canConfirm, canLock, canUnlock, canClear, canPretranslate, beforeRun, onSpellCheck, onFindReplace, onClearSelection,
}: Props) {
  const queryClient = useQueryClient()
  const [clearOpen, setClearOpen] = useState(false)
  const lockFailure = taskLockFailure(queryClient, projectId, jobId)

  const mut = useMutationWithToast<{ action: SegmentBulkAction; res: SegmentBulkResult }, { action: SegmentBulkAction; ids: number[] }>({
    mutationFn: async ({ action, ids }) => {
      await beforeRun()
      const results = await mapChunks(ids, part => bulkSegmentAction(jobId, action, part))
      return { action, res: mergeResults(results) }
    },
    // Nothing written is not a success: say why as a warning instead.
    successMessage: ({ action, res }) => (res.applied.length > 0 ? bulkSummary(action, res) : null),
    errorToast: lockFailure.errorToast,
    onSuccess: ({ action, res }) => {
      if (res.applied.length === 0) toast.warning(bulkSummary(action, res))
      invalidateJobContext(queryClient, projectId, jobId)
      if (res.workflow_reopened || res.task_reopened) handleTaskReopened(queryClient, projectId, jobId)
    },
    onError: (err) => {
      // An earlier chunk may have been written before the failure.
      invalidateJobContext(queryClient, projectId, jobId)
      lockFailure.onError(err)
    },
  })

  // QA reads the stored text, so the open segment's draft is saved first.
  const qaMut = useMutationWithToast<QAReport, number[]>({
    mutationFn: async (ids) => {
      await beforeRun()
      await mapChunks(ids, part => runQA(jobId, part))
      return getQAReport(jobId)
    },
    successMessage: report => qaSelectionToast(report.results, picked),
    errorMessage: 'QA failed',
    onSuccess: report => {
      queryClient.setQueryData(queryKeys.job.qaReport(jobId), report)
      queryClient.invalidateQueries({ queryKey: queryKeys.job.analysis(jobId) })
    },
  })
  // Apply TM / MT on the selection: the job's own pre-translate path, with ids.
  const { applyTM, applyMT } = useJobPipelineActions(jobId, projectId)
  const applying = applyTM.isPending || applyMT.isPending
  const pretranslate = async (apply: typeof applyTM) => {
    await beforeRun()
    apply.mutate(picked)
  }
  const busy = mut.isPending || qaMut.isPending || applying

  const running = mut.isPending ? mut.variables?.action : undefined
  const run = (action: SegmentBulkAction) => mut.mutate({ action, ids: picked })

  const rows = picked.map(id => translationsById.get(id))
  const lockedCount = rows.filter(t => t?.locked).length
  const confirmedCount = rows.filter(t => !t?.locked && t?.active_text && (t.status === 'approved' || t.status === 'auto_approved')).length

  // Every action runs on the selected rows the filter shows; when it hides
  // them all, the buttons say so.
  const nothingShown = picked.length === 0
  const actionButton = (testId: string, label: string, onClick: () => void, variant: 'outline' | 'destructive' = 'outline') => (
    <Button
      variant={variant}
      size="xs"
      onClick={onClick}
      disabled={busy || nothingShown}
      disabledReason={nothingShown ? 'The filter hides every selected segment.' : undefined}
      data-testid={testId}
    >
      {label}
    </Button>
  )
  const button = (action: SegmentBulkAction, label: string, onClick = () => run(action), variant: 'outline' | 'destructive' = 'outline') =>
    actionButton(`segment-bulk-${action}`, running === action ? pending[action] : label, onClick, variant)

  return (
    <>
      {/* Same gap below as the filter bar above it. */}
      <div className="mb-2">
        <SelectionBar
          label={selectionLabel(picked.length, selectedCount)}
          onClear={onClearSelection}
          busy={busy}
          testIdPrefix="segment"
        >
          {canConfirm && (
            <>
              {button('confirm', 'Confirm')}
              <SelectionBarDivider />
            </>
          )}
          {canLock && button('lock', 'Lock')}
          {canUnlock && button('unlock', 'Unlock')}
          <SelectionBarDivider />
          {actionButton('segment-bulk-qa', qaMut.isPending ? 'Checking…' : 'Run QA', () => qaMut.mutate(picked))}
          {actionButton('segment-bulk-spellcheck', 'Spell check', onSpellCheck)}
          {actionButton('segment-bulk-find-replace', 'Find & replace', onFindReplace)}
          {(canPretranslate || canClear) && <SelectionBarDivider />}
          {/* A menu trigger can't carry a disabled reason (its tooltip wraps
              the button), so with nothing shown it is the plain button. */}
          {canPretranslate && nothingShown && actionButton('segment-bulk-pretranslate', 'Pre-translate', () => {})}
          {canPretranslate && !nothingShown && (
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button variant="outline" size="xs" disabled={busy} data-testid="segment-bulk-pretranslate">
                    {applying ? 'Pre-translating…' : 'Pre-translate'}
                  </Button>
                }
              />
              <DropdownMenuContent className="min-w-72">
                <PretranslateItem
                  label="Apply TM"
                  description="Fill TM matches into the selected untranslated and MT-filled segments (preserves human edits and locked segments)"
                  running={applyTM.isPending}
                  disabled={busy}
                  onClick={() => void pretranslate(applyTM)}
                  testId="segment-bulk-apply-tm"
                />
                <PretranslateItem
                  label="Apply MT"
                  description="Translate the selected empty segments with the configured MT engine (skips locked segments)"
                  running={applyMT.isPending}
                  disabled={busy}
                  onClick={() => void pretranslate(applyMT)}
                  testId="segment-bulk-apply-mt"
                />
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          {canClear && button('clear', 'Clear translation', () => setClearOpen(true), 'destructive')}
        </SelectionBar>
      </div>
      <ConfirmDialog
        open={clearOpen}
        onOpenChange={setClearOpen}
        title={`Clear ${count(picked.length, 'translation')}?`}
        description="The selected segments go back to untranslated: MT, TM matches and human edits are removed. Translation memory entries are not affected."
        details={(confirmedCount > 0 || lockedCount > 0) && (
          <ul className="list-disc pl-5 text-sm text-muted-foreground">
            {confirmedCount > 0 && <li>{confirmedCount} of them {confirmedCount === 1 ? 'is' : 'are'} confirmed.</li>}
            {lockedCount > 0 && <li>{lockedCount} locked {lockedCount === 1 ? 'segment is' : 'segments are'} skipped.</li>}
          </ul>
        )}
        actionLabel="Clear"
        onConfirm={() => run('clear')}
      />
    </>
  )
}
