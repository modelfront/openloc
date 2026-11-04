import type { ComponentProps } from 'react'
import { toast } from 'sonner'
import type SpellCheckDialog from '@/components/SpellCheckDialog'
import CompleteStepDialog from '@/components/CompleteStepDialog'
import { RejectStepDialog } from '@/components/RejectStepDialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { checkJobSpelling } from '@/api'
import { rowForNumber, segmentNumber } from '@/lib/editor-index'
import { EditorToolDialogs } from './EditorToolDialogs'
import type { ActiveSegment } from './useActiveSegment'
import type { EditorData } from './useEditorData'
import type { EditorDialogs as EditorDialogsState } from './useEditorDialogs'
import type { StepCompletion } from './useStepCompletion'

type SpellCheckProps = ComponentProps<typeof SpellCheckDialog>

interface EditorDialogsProps {
  jobId: number
  data: EditorData
  dialogs: EditorDialogsState
  completion: StepCompletion
  active: Pick<ActiveSegment, 'activeSegmentIdx' | 'activeInsertRef' | 'switchActiveSegment'>
  /** The open row may not be written: locked, or held by someone else. */
  activeRowReadOnly: boolean
  /** The spell check dialog's job data. */
  spellCheck: Pick<SpellCheckProps, 'spellFixActions' | 'segmentIds' | 'filteredIndices' | 'translationBySegment' | 'filterActive' | 'selectedIds' | 'onApply' | 'onApplyAll'>
  scrollToRow: (segIdx: number) => Promise<boolean>
  scrollToTranslation: (translationId: number, opts?: { activate?: boolean }) => Promise<void>
}

/**
 * The editor's dialogs: Spell check, Concordance, term quick-add, Jump to
 * segment, Complete step and Reject.
 */
export function EditorDialogs({
  jobId: jid, data, dialogs, completion, active, activeRowReadOnly, spellCheck, scrollToRow, scrollToTranslation,
}: EditorDialogsProps) {
  const { job, jobSteps, task, isManager, canEdit, plainTextFile, isFinalStep, currentStepIndex, unapproved, rows } = data
  const {
    completeDialogOpen, setCompleteDialogOpen, approveBlocked, setApproveBlocked,
    rejectDialogOpen, setRejectDialogOpen, advanceMut, approveAllMut, rejectMut,
  } = completion
  const { jumpOpen, setJumpOpen, jumpInput, setJumpInput } = dialogs

  return (
    <>
      <EditorToolDialogs
        dialogs={dialogs}
        active={active}
        projectId={job?.project_id ?? null}
        sourceLanguage={job?.source_language ?? ''}
        targetLanguage={job?.target_language ?? ''}
        isManager={isManager}
        termJobId={job ? jid : undefined}
        plainText={plainTextFile}
        activeRowReadOnly={!canEdit || activeRowReadOnly}
        spellCheck={{ ...spellCheck, check: (ids, signal, opts) => checkJobSpelling(jid, ids, signal, opts), readOnly: !canEdit }}
        // Scrolls the row into view behind the modal without mounting an editor on it.
        onNavigate={id => void scrollToTranslation(id, { activate: false })}
      />

      {/* Jump-to-segment dialog (Ctrl+G) */}
      <Dialog open={jumpOpen} onOpenChange={setJumpOpen}>
        <DialogContent className="sm:max-w-xs">
          <DialogHeader>
            <DialogTitle>Jump to segment</DialogTitle>
          </DialogHeader>
          <form onSubmit={(e) => {
            e.preventDefault()
            // The number the grid shows ("12", or "12.1" for a piece of a
            // split), also with a filter on.
            const typed = jumpInput.trim()
            const idx = rowForNumber(rows, typed)
            if (idx === undefined) {
              toast.info(`There is no segment ${typed}.`)
              return
            }
            setJumpOpen(false)
            void scrollToRow(idx).then(shown => {
              if (!shown) toast.info(`Segment ${typed} is hidden by the filter.`)
            })
          }}>
            <Input
              inputMode="decimal"
              value={jumpInput}
              onChange={(e) => setJumpInput(e.target.value)}
              placeholder={rows.length > 0 ? `1–${segmentNumber(rows[rows.length - 1])}` : ''}
              autoFocus
              className="mb-3"
            />
            <div className="flex justify-end">
              <Button type="submit" size="sm" disabled={!jumpInput.trim()}>
                Go
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      {/* Step Completion Dialog — confirmation before advancing workflow */}
      <CompleteStepDialog
        open={completeDialogOpen}
        onOpenChange={setCompleteDialogOpen}
        stepName={task.step?.step_name ?? 'Step'}
        isFinalStep={isFinalStep}
        approvedCount={job ? job.counts.approved : null}
        unapproved={unapproved}
        canApproveAll={canEdit}
        approveBlocked={approveBlocked}
        pending={advanceMut.isPending || approveAllMut.isPending}
        onConfirm={comment => advanceMut.mutate(comment)}
        onApproveAll={comment => { setApproveBlocked(null); approveAllMut.mutate(comment) }}
      />

      {jobSteps && currentStepIndex >= 0 && (
        <RejectStepDialog
          open={rejectDialogOpen}
          onOpenChange={setRejectDialogOpen}
          steps={jobSteps}
          currentIndex={currentStepIndex}
          isManager={isManager}
          pending={rejectMut.isPending}
          onConfirm={input => rejectMut.mutate(input)}
        />
      )}
    </>
  )
}
