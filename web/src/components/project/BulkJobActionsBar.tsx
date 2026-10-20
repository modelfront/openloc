// BulkJobActionsBar — appears above the Tasks matrix once tasks are
// selected, and owns every action on them. Each action fans out across the
// selection via useBulkJobActions. The CAT editor keeps its own per-job
// buttons, among them the manager re-run of a healthy AI step; Re-run here
// repeats the automation that failed a job.
//
// Two kinds of action. The step actions — Assign, Set deadline, Complete
// step — act on the exact steps picked (`stepTargets`), so a later Review
// step can be assigned before it starts. The job actions — Download,
// Import XLIFF, Pre-translate, Stop, Re-run, Skip AI step, Clear, Delete —
// run on the jobs those tasks belong to; Import XLIFF takes exactly one.
//
// Eligibility is computed the same way the CAT editor buttons gate themselves
// (see lib/project-logic and lib/current-task): Apply TM/MT need first-step +
// no running pipeline, Stop needs a running pipeline with no stop pending,
// Re-run needs a selected task in error, Skip AI step a selected Hybrid task
// whose AI run failed, Clear needs something clearable,
// Complete counts a task only when it is its job's active step, Assign skips
// finished steps, and Assign and Set deadline skip AI steps. Ineligible
// selections are skipped and reported in the summary toast, never acted on.
//
// Permission gating mirrors the routes (internal/api/api.go): Assign, Set
// deadline, Clear, Delete and Skip AI step are manager-only; Re-run of an
// AI-step error is manager-only, Re-run of a pre-translation error is open; Apply*, Stop and
// Complete are open (the backend re-checks assignee/role). Import XLIFF is
// open to everyone (the backend checks a linguist holds the task).

import { useState } from 'react'
import { Upload } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'

import {
  advanceJobStep,
  applyJobMT,
  applyJobTM,
  assignJobStep,
  clearJobTranslations,
  completeJob,
  deleteJob,
  listMembers,
  rerunJob,
  skipJobAIStep,
  setStepDeadline,
  stopJob,
  type ClearScope,
  type Job,
} from '../../api'
import { useBulkJobActions } from '../../hooks/useBulkJobActions'
import { queryKeys } from '../../lib/query-keys'
import { roleLabel } from '../../lib/roles'
import { BULK_COMPLETE_CLAUSE } from '../../lib/completion-copy'
import { warnHeldSkipped } from '../../lib/segment-bulk'
import { canRerunTask, canSkipAIStep } from '../../lib/current-task'
import type { TaskState } from '../../lib/task-matrix'
import {
  canApplyTMorMT,
  canClearJob,
  canCompleteJob,
  canStopJob,
} from '../../lib/project-logic'
import { Button } from '../ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../ui/dropdown-menu'
import ClearTranslationsDialog from '../ClearTranslationsDialog'
import ConfirmDialog from '../ConfirmDialog'
import { DownloadMenu } from './DownloadMenu'
import { BulkButton } from './BulkButton'
import { ImportXLIFFAction } from './ImportXLIFFAction'
import { BulkDeadlineButton } from './BulkDeadlineButton'
import { SelectionBar, SelectionBarDivider } from '@/components/list/SelectionBar'
import { isAutomatedStep } from '@/lib/workflow-steps'

/** One job's assignment for one workflow step, as a target of the step actions. */
export interface StepTarget {
  job: Job
  /** job_step_assignments.id */
  assignmentId: number
  status: string
  /** An `ai` step runs by itself: nobody is assigned to it, and it has no deadline. */
  stepType: string
  /** The task's matrix state — `error` is the job's failure painted on its current task. */
  state: TaskState
}

interface Props {
  projectId: number
  /** The resolved Job objects for the current selection. */
  selectedJobs: Job[]
  /** The selected tasks — what the step actions act on. */
  stepTargets: StepTarget[]
  isManager: boolean
  onClear: () => void
}

export function BulkJobActionsBar({ projectId, selectedJobs, stepTargets, isManager, onClear }: Props) {
  const { run, runningAction } = useBulkJobActions(projectId)
  const [clearOpen, setClearOpen] = useState(false)
  const [completeOpen, setCompleteOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)

  const { data: members } = useQuery({
    queryKey: queryKeys.members.all,
    queryFn: listMembers,
    enabled: isManager,
  })

  const total = selectedJobs.length
  const targetTotal = stepTargets.length

  // Eligible subsets per action, using the lib/project-logic predicates.
  const tmMtEligible = selectedJobs.filter(canApplyTMorMT)
  // A finished step has nobody left to hand it to.
  // An automated step runs by itself: it takes neither a person nor a deadline.
  const assignEligible = stepTargets.filter(t => t.status !== 'completed' && !isAutomatedStep(t.stepType))
  const deadlineEligible = stepTargets.filter(t => !isAutomatedStep(t.stepType))
  const clearEligible = selectedJobs.filter(canClearJob)
  // Complete advances a job's active step, so a task counts only when it is
  // that step.
  const completeEligible = [
    ...new Map(stepTargets.filter(t => t.assignmentId === t.job.workflow_step?.id).map(t => [t.job.id, t.job])).values(),
  ].filter(canCompleteJob)
  const stopEligible = selectedJobs.filter(canStopJob)
  // Re-run repeats a failed job's automation, so it counts each job once,
  // from a selected task in error.
  const rerunEligible = [
    ...new Map(stepTargets.filter(t => t.state === 'error').map(t => [t.job.id, t.job])).values(),
  ].filter(j => canRerunTask(j, isManager))
  // Skip AI step clears a hybrid step's AI error, from a selected task in error.
  const skipAIEligible = [
    ...new Map(stepTargets
      .filter(t => t.state === 'error' && canSkipAIStep(t.job, { status: t.status, step_type: t.stepType }, isManager))
      .map(t => [t.job.id, t.job])).values(),
  ]
  // Summed upper-bound counts for the clear dialog (server skips locked rows).
  const clearCounts = {
    non_confirmed: clearEligible.reduce((n, j) => n + j.counts.clearable_non_confirmed, 0),
    all: clearEligible.reduce((n, j) => n + j.counts.clearable_all, 0),
  }

  const busy = runningAction !== null

  // Runs worker on each eligible job. A job's answer may carry `held` (its
  // segments someone else has open, left alone); the counts add up to one
  // warning at the end.
  const runApply = (
    actionKey: string,
    label: string,
    eligible: Job[],
    worker: (id: number) => Promise<unknown>,
    reason: string,
  ) => {
    let held = 0
    return run({
      actionKey,
      label,
      items: eligible,
      // Jobs run in parallel: read each answer before adding it, so no job's
      // count is lost.
      worker: async (j) => {
        const res = await worker(j.id)
        if (typeof res === 'object' && res !== null && 'held' in res && typeof res.held === 'number') held += res.held
      },
      skipped: { count: total - eligible.length, reason },
    }).then(() => warnHeldSkipped(held))
  }

  const runAssign = (userId: number) =>
    run({
      actionKey: 'assign',
      label: 'Assign',
      unit: 'task',
      items: assignEligible,
      worker: async (t) => { await assignJobStep(t.job.id, t.assignmentId, userId) },
      skipped: { count: targetTotal - assignEligible.length, reason: 'are completed or AI steps' },
    })

  const runClear = (scope: ClearScope) =>
    runApply('clear', 'Clear', clearEligible, id => clearJobTranslations(id, scope), 'nothing to clear / not first step')

  const runComplete = () =>
    run({
      actionKey: 'complete',
      label: 'Complete step',
      items: completeEligible,
      // Advance the active step; if that finished the final step, chain
      // /complete so the job actually closes out (same flow as the CAT editor's
      // "Complete final step").
      worker: async (j) => {
        const data = await advanceJobStep(j.id)
        if (data.status === 'workflow_completed') await completeJob(j.id)
      },
      skipped: { count: total - completeEligible.length, reason: 'no active step / already done' },
    })

  const runDelete = () =>
    run({
      actionKey: 'delete',
      label: 'Delete',
      items: selectedJobs,
      worker: async (j) => { await deleteJob(j.id) },
    }).then(() => onClear()) // deleted ids are gone — drop them from the selection

  const onPickDeadline = (iso: string | null) => {
    run({
      actionKey: 'deadline',
      label: iso ? 'Set deadline' : 'Clear deadline',
      unit: 'task',
      items: deadlineEligible,
      worker: async (t) => { await setStepDeadline(t.job.id, t.assignmentId, iso) },
      skipped: { count: targetTotal - deadlineEligible.length, reason: 'are AI steps, which take no deadline' },
    })
  }

  return (
    <SelectionBar
      label={`${targetTotal} ${targetTotal === 1 ? 'task' : 'tasks'} selected`}
      onClear={onClear}
      busy={busy}
    >

      {/* Download leads the bar: it's the action users hunt for, and unlike the
          rest it needs no eligibility from the workflow — any selection has
          something to download. */}
      <DownloadMenu
        projectId={projectId}
        jobs={selectedJobs}
        fileIds={selectedJobs.map(j => j.file_id)}
        testidPrefix="bulk-download"
      />
      <ImportXLIFFAction projectId={projectId} job={total === 1 ? selectedJobs[0] : null}>
        {(pick, pending) => (
          <BulkButton
            label="Import XLIFF"
            icon={<Upload />}
            pending={pending}
            eligibleCount={total === 1 ? 1 : 0}
            disabled={busy || pending}
            tooltip="Bring back this task's bilingual XLIFF after editing it in another CAT tool"
            ineligibleTooltip="Select one task to import its XLIFF"
            onClick={pick}
            testid="bulk-reimport-btn"
          />
        )}
      </ImportXLIFFAction>

      <SelectionBarDivider />

      {/* Apply TM + Apply MT are one menu, not two buttons: they're the same
          decision (which pre-translation source), they share one eligibility
          rule, and eight top-level buttons wrapped the bar onto a second row.
          The trigger keeps the running label so the spinner stays visible
          without opening the menu. */}
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="outline"
              size="xs"
              disabled={busy || tmMtEligible.length === 0}
              title={tmMtEligible.length === 0
                ? 'No selected jobs are in their first step / all are still running'
                : `Pre-translate ${tmMtEligible.length} eligible job(s) from TM or MT`}
              data-testid="bulk-pretranslate-btn"
            >
              {runningAction === 'tm' ? 'Apply TM…' : runningAction === 'mt' ? 'Apply MT…' : 'Pre-translate'}
            </Button>
          }
        />
        <DropdownMenuContent className="min-w-64">
          <DropdownMenuItem
            onClick={() => runApply('tm', 'Apply TM', tmMtEligible, applyJobTM, 'not in first step / still running')}
            data-testid="bulk-apply-tm-btn"
            className="items-start"
          >
            <span className="flex flex-col">
              <span>Apply TM{tmMtEligible.length > 1 ? ` (${tmMtEligible.length})` : ''}</span>
              <span className="text-xs text-gray-500 dark:text-gray-400">
                Fill TM matches into untranslated and MT-filled segments
              </span>
            </span>
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => runApply('mt', 'Apply MT', tmMtEligible, applyJobMT, 'not in first step / still running')}
            data-testid="bulk-apply-mt-btn"
            className="items-start"
          >
            <span className="flex flex-col">
              <span>Apply MT{tmMtEligible.length > 1 ? ` (${tmMtEligible.length})` : ''}</span>
              <span className="text-xs text-gray-500 dark:text-gray-400">
                Translate empty segments with the configured MT engine
              </span>
            </span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {/* Stop only appears when something in the selection is actually
          running — it's a no-op for idle jobs and would otherwise sit greyed
          out in every idle selection. No confirm: the stop is cooperative and
          keeps what's translated so far. */}
      {stopEligible.length > 0 && (
        <BulkButton
          label="Stop"
          pending={runningAction === 'stop'}
          eligibleCount={stopEligible.length}
          disabled={busy}
          tooltip="Stop the running pipeline after its current chunk; translated segments are kept and Pre-translate resumes from there"
          onClick={() => runApply('stop', 'Stop', stopEligible, stopJob, 'not running / already stopping')}
          testid="bulk-stop-btn"
          variant="warning-outline"
        />
      )}

      {/* Like Stop, Re-run appears only when the selection holds a task it can
          act on. */}
      {rerunEligible.length > 0 && (
        <BulkButton
          label="Re-run"
          pending={runningAction === 'rerun'}
          eligibleCount={rerunEligible.length}
          disabled={busy}
          tooltip="Run the failed AI step or pre-translation again"
          onClick={() => run({
            actionKey: 'rerun',
            label: 'Re-run',
            items: rerunEligible,
            worker: async (j) => { await rerunJob(j.id) },
            skipped: { count: total - rerunEligible.length, reason: 'have no error you can re-run' },
            outcome: 'queued',
          })}
          testid="bulk-rerun-btn"
        />
      )}

      {skipAIEligible.length > 0 && (
        <BulkButton
          label="Skip AI step"
          pending={runningAction === 'skip-ai'}
          eligibleCount={skipAIEligible.length}
          disabled={busy}
          tooltip="Clear the AI error on these Hybrid steps and work them without the AI pass"
          onClick={() => run({
            actionKey: 'skip-ai',
            label: 'Skip AI step',
            items: skipAIEligible,
            worker: async (j) => { await skipJobAIStep(j.id) },
            skipped: { count: total - skipAIEligible.length, reason: 'have no Hybrid step AI error you can skip' },
          })}
          testid="bulk-skip-ai-btn"
        />
      )}

      <SelectionBarDivider />

      <BulkButton
        label="Complete step"
        pending={runningAction === 'complete'}
        eligibleCount={completeEligible.length}
        disabled={busy}
        tooltip={`Complete the current workflow step across the selected jobs (on the final step, ${BULK_COMPLETE_CLAUSE})`}
        onClick={() => setCompleteOpen(true)}
        testid="bulk-complete-btn"
        variant="success-outline"
      />

      {isManager && (
        <>
          <SelectionBarDivider />
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  variant="outline"
                  size="xs"
                  disabled={busy || assignEligible.length === 0}
                  data-testid="bulk-assign-btn"
                >
                  {runningAction === 'assign' ? 'Assigning…' : 'Assign'}
                </Button>
              }
            />
            <DropdownMenuContent>
              {(members ?? []).map(m => (
                <DropdownMenuItem
                  key={m.id}
                  onClick={() => runAssign(m.id)}
                  data-testid={`bulk-assign-user-${m.id}`}
                >
                  <span className="truncate">{m.name}</span>
                  <span className="ml-auto text-xs text-gray-400">{roleLabel(m.role)}</span>
                </DropdownMenuItem>
              ))}
              {(members ?? []).length === 0 && (
                <DropdownMenuItem disabled>No members</DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>

          <BulkDeadlineButton
            onApply={onPickDeadline}
            pending={runningAction === 'deadline'}
            disabled={busy || deadlineEligible.length === 0}
          />

          <SelectionBarDivider />

          <BulkButton
            label="Clear"
            pending={runningAction === 'clear'}
            eligibleCount={clearEligible.length}
            disabled={busy}
            tooltip="Revert translations across the selected jobs so Apply TM/MT can re-fire"
            onClick={() => setClearOpen(true)}
            testid="bulk-clear-btn"
            variant="destructive"
          />
          <BulkButton
            label="Delete"
            pending={runningAction === 'delete'}
            eligibleCount={total}
            disabled={busy}
            tooltip="Delete the selected jobs (removes their translations; other language jobs are unaffected)"
            onClick={() => setDeleteOpen(true)}
            testid="bulk-delete-btn"
            variant="destructive"
          />
        </>
      )}

      <ClearTranslationsDialog
        open={clearOpen}
        onOpenChange={setClearOpen}
        title="Clear translations?"
        description={`Revert translations across ${clearEligible.length} selected job(s).`}
        counts={clearCounts}
        onConfirm={(scope) => runClear(scope)}
      />
      <ConfirmDialog
        open={completeOpen}
        onOpenChange={setCompleteOpen}
        title={`Complete step for ${completeEligible.length} job(s)?`}
        description={`Advances each selected job's current workflow step. Jobs on their final step are completed — ${BULK_COMPLETE_CLAUSE}.`}
        actionLabel="Complete"
        variant="default"
        onConfirm={() => runComplete()}
      />
      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={`Delete ${total} job(s)?`}
        description="This removes all translations for the selected jobs. Segments and other language jobs are not affected. This cannot be undone."
        actionLabel="Delete"
        onConfirm={() => runDelete()}
      />
    </SelectionBar>
  )
}
