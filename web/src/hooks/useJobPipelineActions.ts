import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { applyJobMT, runJobAIStep, applyJobTM, rerunJob, skipJobAIStep, stopJob } from '../api'
import { mergeApplyResponses, type ApplyPhaseResponse } from '../api/analytics'
import { useMutationWithToast } from './use-mutation-with-toast'
import { invalidateJobContext } from '../lib/query-invalidation'
import { mapChunks, warnHeldSkipped } from '../lib/segment-bulk'
import { handleTaskReopened, taskLockFailure } from '../lib/task-edit'

// Owner of the phase-apply mutations (Apply TM / Apply MT / re-run QE+APE
// AI step), the Re-run of a failed job's automation, and the cooperative Stop
// of a running pipeline, for the CAT editor. It owns the success-toast
// wording, error-toast prefixing, and cache-invalidation surface, so every
// caller gets the same behavior for the same backend call.
//
// A synchronous apply resolves with `stopped: true` when the run was stopped
// mid-way; the segments translated before the stop are kept.
const STOPPED_MSG = 'Stopped — translated segments are kept'

export function useJobPipelineActions(jobId: number, projectId: number) {
  const queryClient = useQueryClient()

  // A stopped run and an empty one get an info notice instead of the success
  // toast, so `successMessage` returns null for them.
  const afterApply = (data: ApplyPhaseResponse, emptyMsg: string) => {
    invalidateJobContext(queryClient, projectId, jobId)
    if (data?.task_reopened) handleTaskReopened(queryClient, projectId, jobId)
    if (data?.stopped) toast.info(STOPPED_MSG)
    else if (data?.skip_reason === 'no_segments') toast.info(emptyMsg)
    warnHeldSkipped(data?.held)
  }
  const applied = (data: ApplyPhaseResponse, successMsg: string) =>
    data?.stopped || data?.skip_reason === 'no_segments' ? null : successMsg

  // Errors show the backend message verbatim. Backend 4xx responses carry
  // user-facing text ("No MT engine configured for en → de — add one
  // in project settings"); a "TM/MT/Predict failed:" prefix would just
  // duplicate the action noun. The red toast color + the button the user
  // clicked supply the "which action failed" context. A task-lock refusal
  // reloads the task, so the editor shows why it is locked, and has its own
  // toast.
  const failure = taskLockFailure(queryClient, projectId, jobId)
  // A selection runs in chunks one after another, so a failure can come after
  // earlier chunks were written: reload the job before handling the error.
  const applyFailure = {
    errorToast: failure.errorToast,
    onError: (err: Error) => {
      invalidateJobContext(queryClient, projectId, jobId)
      failure.onError(err)
    },
  }

  // The variable is the segment selection (translation ids); none = the whole job.
  const onSelection = (apply: typeof applyJobTM) => async (ids: number[] | void): Promise<ApplyPhaseResponse> => {
    if (!ids) return apply(jobId)
    return mergeApplyResponses(await mapChunks(ids, part => apply(jobId, part)))
  }
  const applyTM = useMutationWithToast<ApplyPhaseResponse, number[] | void>({
    mutationFn: onSelection(applyJobTM),
    successMessage: (data, ids) => applied(data, ids ? 'TM lookup complete on the selection' : 'TM lookup complete'),
    onSuccess: (data, ids) => afterApply(data, ids ? 'Nothing in the selection to TM-match' : 'No segments to TM-match'),
    ...applyFailure,
  })
  const applyMT = useMutationWithToast<ApplyPhaseResponse, number[] | void>({
    mutationFn: onSelection(applyJobMT),
    successMessage: (data, ids) => applied(data, ids ? 'MT applied to the selection' : 'MT applied'),
    onSuccess: (data, ids) => afterApply(data, ids ? 'Nothing in the selection to translate' : 'No segments to translate'),
    ...applyFailure,
  })
  const runAIStep = useMutationWithToast({
    mutationFn: () => runJobAIStep(jobId),
    successMessage: data => applied(data, 'QE+APE step complete'),
    onSuccess: data => afterApply(data, 'No segments to score'),
    ...failure,
  })

  // Stop is cooperative: the backend answers 202 right away and the run lands
  // at the next chunk boundary, so the job keeps polling as in_progress with
  // `stop_requested_at` set until then. Deliberately NOT folded into
  // isAnyApplying — Stop is the one action meant to be live while a run is.
  const stop = useMutationWithToast({
    mutationFn: () => stopJob(jobId),
    // An info notice, not a success: the run is still going.
    successMessage: () => null,
    onSuccess: () => {
      invalidateJobContext(queryClient, projectId, jobId)
      toast.info('Stopping — the current chunk finishes first; translated segments are kept')
    },
    ...failure,
  })

  // Re-run is asynchronous: the job goes back to queued, and the editor's
  // pipeline polling picks the run up from there.
  const rerun = useMutationWithToast({
    mutationFn: () => rerunJob(jobId),
    successMessage: 'Re-run queued',
    onSuccess: () => invalidateJobContext(queryClient, projectId, jobId),
    ...failure,
  })

  // Skip clears the AI-step error; the refetched job turns the task editable.
  const skipAIStep = useMutationWithToast({
    mutationFn: () => skipJobAIStep(jobId),
    successMessage: 'AI step skipped — the step is open for editing',
    onSuccess: () => invalidateJobContext(queryClient, projectId, jobId),
    ...failure,
  })

  return {
    applyTM,
    applyMT,
    runAIStep,
    rerun,
    skipAIStep,
    stop,
    isAnyApplying: applyTM.isPending || applyMT.isPending || runAIStep.isPending,
  }
}
