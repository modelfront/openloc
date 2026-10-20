import type React from 'react'
import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2Icon } from 'lucide-react'

import { advanceJobStep, completeJob, getMe, type Job } from '../api'
import { useJobPipelineActions } from '../hooks/useJobPipelineActions'
import { useClearJobTranslations } from '../hooks/useClearTranslations'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { queryKeys } from '../lib/query-keys'
import { invalidateJobContext, invalidateProjectStatus, invalidateTMContext } from '../lib/query-invalidation'
import { isManagerRole } from '../lib/roles'
import { isPipelineRunning } from '../lib/project-logic'
import { finalStepSentence } from '../lib/completion-copy'
import { Button } from './ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './ui/dropdown-menu'
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip'
import ClearTranslationsDialog from './ClearTranslationsDialog'
import ConfirmDialog from './ConfirmDialog'

// One row of the Pre-translate menu. Both rows carry the same three concerns
// (label, description, running state); writing them inline twice is what lets
// the two drift apart.
export function PretranslateItem({
  label, description, running, disabled, onClick, testId,
}: {
  label: string
  description: string
  running: boolean
  disabled: boolean
  onClick: (e: React.MouseEvent) => void
  testId: string
}) {
  return (
    <DropdownMenuItem onClick={onClick} disabled={disabled} data-testid={testId} className="items-start">
      <span className="flex flex-col">
        <span className="flex items-center gap-1.5">
          {label}
          {running && (
            <span role="status" className="flex items-center gap-1 text-xs font-normal text-gray-500 dark:text-gray-400">
              <Loader2Icon className="size-3 animate-spin" aria-hidden="true" />
              Running…
            </span>
          )}
        </span>
        <span className="text-xs text-gray-500 dark:text-gray-400">{description}</span>
      </span>
    </DropdownMenuItem>
  )
}

interface Props {
  job: Job
  projectId: number
  // Override for "first workflow step" gating. CATEditor computes this from
  // its loaded steps list + current task and passes the answer in. When
  // omitted, we derive from `job.workflow_step`: first step ⇔ no workflow OR
  // the step has step_order 0.
  inFirstStep?: boolean
  // False while the current task cannot be edited (lib/current-task
  // `canEditTask`): the actions that write translations are hidden then.
  // Defaults true.
  canEdit?: boolean
  size?: 'sm' | 'xs'
  // Wrap each onClick in preventDefault + stopPropagation so the buttons
  // don't trigger an enclosing <Link>'s navigation (project page job rows
  // are clickable Links to the CAT editor).
  stopPropagationOnClick?: boolean
  // Hide the per-job Complete button in contexts that already
  // surface their own workflow-advance UI (notably the CAT editor's
  // workflow banner). Project row keeps the default `false` so the button
  // shows there.
  hideComplete?: boolean
}

// Pipeline action group: Stop, Apply TM, Apply MT, Re-run AI step, Clear,
// Complete. Used by the CAT editor toolbar and the project page job rows.
// Gating:
//   - Stop: shown only while a pipeline task is queued/running on the job.
//     It's the one button that stays live during a run; it reads
//     "Stopping…" and disables once a stop is pending (`stop_requested_at`).
//   - Apply TM / Apply MT: always available while we're in the first
//     workflow step, so a translator who cleared a segment (or the whole
//     file) can re-pretranslate; the pipeline is a no-op when nothing is
//     eligible.
//   - Re-run AI step: manager-only, shown only when the job's active step
//     is a QE+APE step (workflow_step.is_ai_step). The step
//     normally auto-runs when the step starts; this button lets
//     a manager re-run it (e.g. after fixing MT output or the engine key).
//   - Apply TM / Apply MT, Re-run AI step and Clear write translations, so
//     they are hidden while the task cannot be edited (`canEdit`) and while
//     the job is failed — the CAT editor's task controls own Re-run then.
// Every other button is disabled while any action is in flight or a pipeline
// task is queued/running on the job — prevents stacking concurrent runs.
export function JobPipelineActions({
  job,
  projectId,
  inFirstStep,
  canEdit = true,
  size = 'sm',
  stopPropagationOnClick = false,
  hideComplete = false,
}: Props) {
  const { applyTM, applyMT, runAIStep, stop, isAnyApplying } = useJobPipelineActions(job.id, projectId)
  const clearMut = useClearJobTranslations({ jobId: job.id, projectId })
  const [clearDialogOpen, setClearDialogOpen] = useState(false)
  const [completeDialogOpen, setCompleteDialogOpen] = useState(false)
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const isManager = isManagerRole(me?.role)
  const queryClient = useQueryClient()

  // Per-job Complete button, same flow as the CAT editor's "Complete final
  // step ✓": advance the current workflow step; if the
  // server says workflow_completed, call /complete so the job closes out.
  const completeJobMut = useMutationWithToast({
    mutationFn: () => completeJob(job.id),
    successMessage: data =>
      data.tm_saved_count > 0 ? `Job completed. Saved ${data.tm_saved_count} segment(s) to TM.` : 'Job completed',
    errorMessage: 'Complete failed',
    onSuccess: (data) => {
      invalidateJobContext(queryClient, projectId, job.id)
      // This job may have been the project's last unfinished one, in which
      // case the backend just auto-completed the project.
      invalidateProjectStatus(queryClient, projectId)
      if (data.tm_saved_count > 0) invalidateTMContext(queryClient)
    },
  })

  const advanceMut = useMutationWithToast({
    mutationFn: () => advanceJobStep(job.id),
    // The final step's notice comes from completeJobMut.
    successMessage: data => (data.status === 'workflow_completed' ? null : 'Step completed — advanced to next step'),
    errorMessage: 'Advance failed',
    invalidateKeys: [queryKeys.projects.all],
    onSuccess: (data) => {
      invalidateJobContext(queryClient, projectId, job.id)
      // Final step → chain to /complete so the job is actually completed.
      if (data.status === 'workflow_completed') completeJobMut.mutate()
    },
  })

  const computedInFirstStep =
    inFirstStep ?? (!job.workflow_step || job.workflow_step.step_order === 0)

  const pipelineRunning = isPipelineRunning(job)
  const counts = job.counts
  const writable = canEdit && job.pipeline_status !== 'failed'

  // Stop renders alone while a run is live (the Apply/Clear/Complete gates
  // all go dark then), so it must count towards the early return below.
  const showStop = pipelineRunning
  const stopPending = !!job.stop_requested_at

  // Apply TM + Apply MT share one gate and one decision ("which
  // pre-translation source?"), so they're one menu rather than two buttons —
  // the CAT editor toolbar and the project bulk bar both ran out of horizontal
  // room otherwise.
  const showPretranslate = writable && computedInFirstStep
  // Manager re-run of the active QE+APE step. Backend re-checks the
  // engine + role and 400s if the step isn't a connected QE+APE step.
  const showRunAIStep = writable && isManager && job.workflow_step?.is_ai_step === true && !pipelineRunning
  const hasClearable = counts.total > counts.untranslated
  const showClear = writable && isManager && computedInFirstStep && hasClearable
  // Complete button shows whenever the job has an active workflow
  // step AND isn't already done. The backend HandleAdvanceStep + HandleComplete
  // re-check eligibility (assigned user / role / not-already-completed) and
  // reject with 403/409 if the click is illegitimate — keeping the FE gate
  // permissive avoids hiding the button from users who should be allowed
  // but whose role-membership info isn't on the row.
  const isFinalStep =
    job.workflow_step != null &&
    job.workflow_step.is_last_step === true
  const showComplete =
    !hideComplete &&
    job.workflow_step != null &&
    job.pipeline_status !== 'completed' &&
    !pipelineRunning
  // Exact counts from the backend (locked + empty rows already excluded, and
  // matching the clear query's predicate), so the dialog shows the real number.
  const clearCounts = {
    non_confirmed: counts.clearable_non_confirmed,
    all: counts.clearable_all,
  }

  if (!showStop && !showPretranslate && !showRunAIStep && !showClear && !showComplete) return null

  const advancing = advanceMut.isPending || completeJobMut.isPending
  const disabled = isAnyApplying || pipelineRunning || clearMut.isPending || advancing

  const tmRunning = applyTM.isPending
  const mtRunning = applyMT.isPending
  // Reopening the menu mid-run is how the user finds out WHICH action is
  // running, so an in-flight apply of our own must not disable the trigger —
  // every other reason to be disabled (a pipeline run we didn't start, a clear,
  // a step advance) still does. The items keep the full `disabled` gate, so an
  // open menu can't stack a second run.
  const triggerDisabled = disabled && !isAnyApplying

  const wrap = (action: () => void) => (e: React.MouseEvent) => {
    if (stopPropagationOnClick) {
      e.preventDefault()
      e.stopPropagation()
    }
    action()
  }

  return (
    <>
      {showStop && (
        <Tooltip>
          <TooltipTrigger
            render={
              // Own gate, not the shared `disabled`: that one includes
              // pipelineRunning, which is exactly when Stop must be clickable.
              <Button
                onClick={wrap(() => stop.mutate())}
                disabled={stop.isPending || stopPending}
                variant="outline"
                size={size}
                data-testid="stop-job-btn"
              />
            }
          >
            {(stop.isPending || stopPending) && (
              <Loader2Icon className="animate-spin" aria-hidden="true" data-testid="stop-job-spinner" />
            )}
            {stopPending ? 'Stopping…' : 'Stop'}
          </TooltipTrigger>
          <TooltipContent>
            Stop after the current chunk. Translated segments are kept; Pre-translate resumes from there.
          </TooltipContent>
        </Tooltip>
      )}
      {showPretranslate && (
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              // `wrap` stays on the TRIGGER (not just the items): this is the
              // element inside the project row's <Link>, and stopPropagation
              // here doesn't stop base-ui's own same-element handler, so the
              // menu still opens.
              <Button
                onClick={wrap(() => {})}
                disabled={triggerDisabled}
                variant="outline"
                size={size}
                title="Fill this job from translation memory or machine translation"
                data-testid="pretranslate-btn"
              >
                {/* Bare icon: Button's size variant sets both the gap and the
                    svg size, so hardcoding either here would desync `size`
                    from its own spacing. The label stays "Pre-translate" while
                    running — which action it is belongs in the menu, where
                    there's room to say it. */}
                {(tmRunning || mtRunning) && (
                  <Loader2Icon className="animate-spin" aria-hidden="true" data-testid="pretranslate-spinner" />
                )}
                Pre-translate
              </Button>
            }
          />
          <DropdownMenuContent className="min-w-72">
            <PretranslateItem
              label="Apply TM"
              description="Fill TM matches into untranslated and MT-filled segments (preserves human edits)"
              running={tmRunning}
              disabled={disabled}
              onClick={wrap(() => applyTM.mutate())}
              testId="apply-tm-btn"
            />
            <PretranslateItem
              label="Apply MT"
              description="Translate empty segments with the configured MT engine (skips TM lookup)"
              running={mtRunning}
              disabled={disabled}
              onClick={wrap(() => applyMT.mutate())}
              testId="apply-mt-btn"
            />
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      {showRunAIStep && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                onClick={wrap(() => runAIStep.mutate())}
                disabled={disabled}
                variant="outline"
                size={size}
                data-testid="run-ai-step-btn"
              />
            }
          >
            {runAIStep.isPending ? 'Running…' : 'Re-run AI step'}
          </TooltipTrigger>
          <TooltipContent>
            Re-run QE + APE on this step's segments (auto-runs when the step starts)
          </TooltipContent>
        </Tooltip>
      )}
      {showClear && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                onClick={wrap(() => setClearDialogOpen(true))}
                disabled={disabled}
                variant="destructive"
                size={size}
                data-testid="clear-translations-btn"
              />
            }
          >
            Clear
          </TooltipTrigger>
          <TooltipContent>
            Revert translations so Apply TM/MT can re-fire
          </TooltipContent>
        </Tooltip>
      )}
      {showComplete && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                onClick={wrap(() => setCompleteDialogOpen(true))}
                disabled={disabled}
                variant="success-outline"
                size={size}
                data-testid="complete-step-btn"
              />
            }
          >
            {advancing ? 'Completing…' : isFinalStep ? 'Complete ✓' : 'Complete step →'}
          </TooltipTrigger>
          <TooltipContent>
            {isFinalStep
              ? finalStepSentence({ approvedCount: counts.approved })
              : `Complete the current "${job.workflow_step?.step_name ?? 'workflow'}" step and advance to the next`}
          </TooltipContent>
        </Tooltip>
      )}
      <ClearTranslationsDialog
        open={clearDialogOpen}
        onOpenChange={setClearDialogOpen}
        title="Clear translations?"
        description={`Revert translations for ${job.file_name} → ${job.target_language}.`}
        counts={clearCounts}
        onConfirm={(scope) => clearMut.mutate(scope)}
      />
      <ConfirmDialog
        open={completeDialogOpen}
        onOpenChange={setCompleteDialogOpen}
        title={isFinalStep ? 'Complete this job?' : `Complete "${job.workflow_step?.step_name ?? 'step'}"?`}
        description={
          isFinalStep
            ? finalStepSentence({
                approvedCount: counts.approved,
                fileLabel: `${job.file_name} (${job.target_language})`,
              })
            : `This will advance ${job.file_name} (${job.target_language}) to the next workflow step.`
        }
        actionLabel="Complete"
        variant="default"
        onConfirm={() => advanceMut.mutate()}
      />
    </>
  )
}
