import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import {
  advanceJobStep,
  completeJob,
  confirmAllTranslations,
  rejectJobStep,
  type JobStepAssignment,
  type RejectStepInput,
} from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { invalidateJobContext, invalidateProjectStatus, invalidateTMContext } from '@/lib/query-invalidation'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { handleTaskLocked, handleTaskReopened, isTaskLockedError } from '@/lib/task-edit'
import { blockedSegmentNumbers, type ApproveBlocked } from '@/lib/step-completion'
import type { SegmentBulkResult } from '@/lib/segment-bulk'
import { segmentNumber, type IndexLookup, type IndexRow } from '@/lib/editor-index'

interface Options {
  jid: number
  pid: number
  jobSteps: JobStepAssignment[] | undefined
  indexLookup: IndexLookup
  rows: IndexRow[]
}

/**
 * Completing and rejecting the editor's workflow step: the Complete step and
 * Reject dialogs' open state and the mutations behind them.
 */
export function useStepCompletion({ jid, pid, jobSteps, indexLookup, rows }: Options) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const [completeDialogOpen, setCompleteDialogOpen] = useState(false)
  // Segments "Approve all and complete" could not approve; the step stays
  // open until they are approved.
  const [approveBlocked, setApproveBlocked] = useState<ApproveBlocked | null>(null)
  const [rejectDialogOpen, setRejectDialogOpen] = useState(false)

  const completeMut = useMutationWithToast({
    mutationFn: () => completeJob(jid),
    successMessage: data => (data.tm_saved_count > 0
      ? `Job completed. Saved ${data.tm_saved_count} segment(s) to TM.`
      : 'Job completed'),
    errorMessage: 'Complete failed',
    onSuccess: (data) => {
      invalidateJobContext(queryClient, pid, jid)
      // This job may have been the project's last unfinished one, in which
      // case the backend just auto-completed the project.
      invalidateProjectStatus(queryClient, pid)
      if (data.tm_saved_count > 0) invalidateTMContext(queryClient)
    },
  })

  const advanceMut = useMutationWithToast({
    mutationFn: (comment: string) => advanceJobStep(jid, comment),
    // The final advance's toast comes from completeMut.
    successMessage: (data) => {
      if (data.status === 'workflow_completed') return null
      // The next step's assignee name for the toast message
      const nextStep = jobSteps?.find(s => s.id === data.next_step_id)
      const assigneeMsg = nextStep?.assignee_name ? ` Assigned to ${nextStep.assignee_name}.` : ''
      return `Step completed — advanced to ${nextStep?.step_name ?? 'next step'}.${assigneeMsg}`
    },
    // The dialog stays open on failure, keeping the typed comment.
    errorMessage: 'Advance failed',
    onSuccess: (data) => {
      invalidateJobContext(queryClient, pid, jid)
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.all })
      setCompleteDialogOpen(false)
      if (data.status === 'workflow_completed') {
        // The final advance chains into /complete — that call is what closes
        // the job (status='completed', approved segments saved to TM). It's the
        // editor's only completion path. completeMut owns the toast (success
        // and error); this callback only leaves the editor, and only on
        // success — on failure the user stays put and can retry.
        completeMut.mutate(undefined, {
          onSuccess: () => navigate(`/projects/${pid}`),
        })
        return
      }
      // Navigate back to project detail (leave the editor after completing your step)
      navigate(`/projects/${pid}`)
    },
  })

  // "Approve all and complete": approve every translated segment, then complete
  // the step — unless a QA error, a change made meanwhile or someone else
  // editing it kept a segment unapproved, in which case the dialog stays open
  // and lists them.
  // The variable is the hand-off note the advance sends once all is approved.
  const approveAllMut = useMutation<SegmentBulkResult, Error, string>({
    mutationFn: () => confirmAllTranslations(jid),
    onSuccess: (res, comment) => {
      invalidateJobContext(queryClient, pid, jid)
      if (res.task_reopened) handleTaskReopened(queryClient, pid, jid)
      const blocked = blockedSegmentNumbers(res.skipped, id => indexLookup.byTranslation.get(id), idx => segmentNumber(rows[idx]))
      if (blocked) {
        setApproveBlocked(blocked)
        return
      }
      advanceMut.mutate(comment)
    },
    onError: (err: Error) => {
      if (isTaskLockedError(err)) {
        handleTaskLocked(queryClient, pid, jid, err)
        setCompleteDialogOpen(false)
        return
      }
      toast.error(`Approve all failed: ${err.message}`)
    },
  })

  const rejectMut = useMutationWithToast({
    mutationFn: (input: RejectStepInput) => rejectJobStep(jid, input),
    successMessage: (data) => {
      const to = jobSteps?.find(s => s.id === data.reopened_step_id)?.step_name ?? 'an earlier step'
      return `Sent back to ${to}.${data.rerun_ai ? ' The AI runs again.' : ''}`
    },
    // The dialog stays open on failure, keeping the typed reason.
    errorMessage: 'Reject failed',
    invalidateKeys: [queryKeys.job.steps(jid), queryKeys.job.detail(jid)],
    onSuccess: () => {
      // The reopened step gets its own statuses back, so the segments change.
      invalidateJobContext(queryClient, pid, jid)
      setRejectDialogOpen(false)
    },
  })

  return {
    completeDialogOpen,
    setCompleteDialogOpen,
    approveBlocked,
    setApproveBlocked,
    rejectDialogOpen,
    setRejectDialogOpen,
    advanceMut,
    approveAllMut,
    rejectMut,
  }
}

export type StepCompletion = ReturnType<typeof useStepCompletion>
