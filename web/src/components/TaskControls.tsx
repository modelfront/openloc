// The CAT editor's current task: the state of the task the editor works on,
// and what can be done with it. The breadcrumb names it (TaskStepChip, then
// TaskDeadlineChip); its state and actions sit at the right end of the same
// line (TaskActions); an error, a "sent back" reason and the previous step's
// hand-off note get a row under the toolbar (TaskNotes). All of them read the
// `CurrentTask` from lib/current-task, so they and the editor's read-only
// state never disagree.

import { useId, useState } from 'react'
import type { JobStepAssignment } from '../api'
import { HANDOFF_NOTE_TITLE, taskStepTooltip, type CurrentTask, type TaskMode, type TooltipJob } from '../lib/current-task'
import { deadlineChipProps } from '../lib/deadline'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Chip, chipSurfaceClass, chipTextClass, type ChipColor } from '@/components/ui/chip'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { isAutomatedStep } from '@/lib/workflow-steps'

/** The step chip's colour per mode: in progress, completed, error, not started. */
const TASK_MODE_CHIP_COLOR: Record<TaskMode, ChipColor> = {
  loading: 'grey',
  editable: 'blue',
  automation_running: 'blue',
  reopenable: 'green',
  read_only: 'green',
  errored: 'red',
  none: 'grey',
}

interface TaskStepChipProps {
  task: CurrentTask
  steps: JobStepAssignment[]
  job: TooltipJob
  isFinalStep: boolean
}

/**
 * The current task's step, coloured by its mode. Before anything starts there
 * is no current task, so the chip names the workflow's first step, in grey.
 */
export function TaskStepChip({ task, steps, job, isFinalStep }: TaskStepChipProps) {
  const step = task.step ?? (task.mode === 'none' ? firstStep(steps) : null)
  if (!step || task.mode === 'loading') return null
  return (
    <Tooltip>
      {/* The chip is the trigger: a wrapping button would sit it on the text
          baseline, below the chips beside it. */}
      <TooltipTrigger
        render={
          <Chip color={TASK_MODE_CHIP_COLOR[task.mode]} data-testid="cat-current-step" data-mode={task.mode} className="cursor-help" tabIndex={0}>
            {step.step_name}
          </Chip>
        }
      />
      <TooltipContent side="bottom" className="max-w-64 text-xs">
        {taskStepTooltip(task, step, job, isFinalStep)}
      </TooltipContent>
    </Tooltip>
  )
}

function firstStep(steps: JobStepAssignment[]): JobStepAssignment | null {
  return steps.reduce<JobStepAssignment | null>((a, s) => (a === null || s.step_order < a.step_order ? s : a), null)
}

/** The deadline of a task in progress, after the step chip. An automated step has none. */
export function TaskDeadlineChip({ task }: { task: CurrentTask }) {
  const step = task.step
  if (!step || isAutomatedStep(step.step_type) || (task.mode !== 'editable' && task.mode !== 'automation_running')) return null
  const { title, ...chip } = deadlineChipProps(step.deadline)
  return (
    <Tooltip>
      <TooltipTrigger render={<Chip {...chip} data-testid="cat-step-deadline" className="cursor-help" tabIndex={0} />} />
      <TooltipContent side="bottom" className="max-w-64 text-xs">{title}</TooltipContent>
    </Tooltip>
  )
}

export interface TaskActionsProps {
  task: CurrentTask
  isFinalStep: boolean
  /** Re-run is offered (lib/current-task `canRerunTask`). */
  canRerun: boolean
  /** Some earlier step can take the job back (lib/rollback `canRollback`). */
  canReject: boolean
  rerunPending?: boolean
  completePending?: boolean
  rejectPending?: boolean
  onRerun: () => void
  /** Opens the complete dialog. */
  onComplete: () => void
  /** Opens the reject dialog. */
  onReject: () => void
}

/** The current task's state and actions, for the breadcrumb's `actions` slot. */
export function TaskActions(props: TaskActionsProps) {
  if (props.task.mode === 'loading') return null
  return (
    <div className="flex items-center gap-2" data-testid="task-actions" data-mode={props.task.mode}>
      <ActionsBody {...props} />
    </div>
  )
}

function ActionsBody({
  task, isFinalStep, canRerun, canReject, rerunPending, completePending, rejectPending,
  onRerun, onComplete, onReject,
}: TaskActionsProps) {
  const step = task.step
  const complete = <CompleteButton isFinalStep={isFinalStep} pending={completePending} onClick={onComplete} />
  switch (task.mode) {
    case 'editable':
    case 'automation_running':
      if (!step) return null
      return (
        <>
          {canReject && (
            <Button
              onClick={onReject}
              disabled={rejectPending}
              variant="destructive"
              size="sm"
              className="h-7 text-xs"
              data-testid="task-reject-btn"
            >
              ← Reject
            </Button>
          )}
          {complete}
        </>
      )
    case 'reopenable':
      return (
        <>
          <Chip color="green">Completed</Chip>
          <span className="text-xs text-gray-500 dark:text-gray-400">Edit a segment to reopen this task.</span>
        </>
      )
    case 'read_only':
      return (
        <>
          <Chip color="green">Completed</Chip>
          <Chip>Read-only</Chip>
        </>
      )
    case 'errored':
      return (
        <>
          {canRerun && (
            <Button onClick={onRerun} disabled={rerunPending} variant="outline" size="sm" className="h-7 text-xs" data-testid="task-rerun-btn">
              {rerunPending ? 'Re-running…' : 'Re-run'}
            </Button>
          )}
          {/* Completing accepts the error. A completed task has no step left to complete. */}
          {step?.status === 'in_progress' && complete}
        </>
      )
    case 'none':
      return <Chip>Not started</Chip>
    default:
      return null
  }
}

function CompleteButton({ isFinalStep, pending, onClick }: { isFinalStep: boolean; pending?: boolean; onClick: () => void }) {
  return (
    <Button
      onClick={onClick}
      disabled={pending}
      variant={isFinalStep ? 'final' : 'success'}
      size="sm"
      className="h-7 text-xs"
      data-testid="task-complete-btn"
      data-guide="editor.complete"
    >
      {isFinalStep ? 'Complete final step ✓' : 'Complete step →'}
    </Button>
  )
}

/**
 * The rows under the toolbar: the job's error while the task is errored, the
 * reason a later step sent the job back, and, while the task is in progress,
 * the comment the previous step left on completing.
 */
export interface TaskNotesProps {
  task: CurrentTask
  steps: JobStepAssignment[]
  errorMessage?: string
  /** Offers Skip AI step in the error row (lib/current-task `canSkipAIStep`). */
  onSkipAIStep?: () => void
  skipAIStepPending?: boolean
}

export function TaskNotes({ task, steps, errorMessage, onSkipAIStep, skipAIStepPending }: TaskNotesProps) {
  const rejected = steps.find(s => s.status === 'rejected' && s.rejection_reason)
  const handoffNote = task.step?.status === 'in_progress' ? task.step.handoff_note : null
  return (
    <>
      {task.mode === 'errored' && (
        <div
          className={cn('mb-3 flex items-center gap-3 px-4 py-2.5 rounded-lg border text-sm', chipSurfaceClass('red'), chipTextClass('red'))}
          data-testid="task-error"
        >
          <p className="flex-1 min-w-0 wrap-anywhere">{errorMessage || 'The automation failed.'}</p>
          {onSkipAIStep && (
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    onClick={onSkipAIStep}
                    disabled={skipAIStepPending}
                    variant="outline"
                    size="sm"
                    className="h-7 text-xs shrink-0"
                    data-testid="task-skip-ai-btn"
                  >
                    {skipAIStepPending ? 'Skipping…' : 'Skip AI step'}
                  </Button>
                }
              />
              <TooltipContent side="bottom" className="max-w-64 text-xs">
                Clear the error and work this step without the AI pass.
              </TooltipContent>
            </Tooltip>
          )}
        </div>
      )}
      {rejected && (
        <TaskNoteBanner variant="sent_back" title={`Sent back from ${rejected.step_name}`} text={rejected.rejection_reason} />
      )}
      {handoffNote && <TaskNoteBanner variant="handoff" title={HANDOFF_NOTE_TITLE} text={handoffNote} />}
    </>
  )
}

// Surface and text per variant, both from the chip palette: the banner is a
// surface carrying a state, like a task-matrix cell.
const NOTE_BANNER_CLASS = {
  sent_back: cn(chipSurfaceClass('red'), chipTextClass('red')),
  handoff: cn(chipSurfaceClass('blue'), chipTextClass('blue')),
} as const

interface TaskNoteBannerProps {
  variant: keyof typeof NOTE_BANNER_CLASS
  title: string
  text: string
}

const NOTE_BANNER_TESTID = {
  sent_back: 'task-sent-back',
  handoff: 'task-handoff-note',
} as const

/**
 * A note about the task as a banner under the toolbar: the title on a
 * show/hide toggle above the text. It opens shown, and hiding it lasts only
 * until the editor is opened again.
 */
function TaskNoteBanner({ variant, title, text }: TaskNoteBannerProps) {
  const [open, setOpen] = useState(true)
  const bodyId = useId()
  const testId = NOTE_BANNER_TESTID[variant]
  return (
    <div className={cn('mb-3 px-4 py-2.5 rounded-lg border text-sm', NOTE_BANNER_CLASS[variant])} data-testid={testId}>
      <DisclosureToggle open={open} onToggle={() => setOpen(o => !o)} controls={bodyId} data-testid={`${testId}-toggle`}>
        {title}
      </DisclosureToggle>
      {open && <p id={bodyId} className="mt-1 whitespace-pre-wrap wrap-anywhere">{text}</p>}
    </div>
  )
}
