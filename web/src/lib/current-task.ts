// The task the CAT editor works on, and what the user may do with it.
//
// A task is one job's assignment for one workflow step. The workflow is linear
// and at most one task per job is in progress; every task of a job edits the
// same translation rows. The job's CURRENT task is the in-progress one, else
// the completed one furthest along the workflow, else none.
//
// The backend applies the same rule to every text write (internal/taskedit),
// so a mode that lets the editor write is one the server accepts:
//   - in progress → editable, except while the AI step runs, or while
//     pre-translation runs on a Pre-translation step;
//   - completed → a manager's edit reopens it; anyone else reads only;
//   - the job's automation failed → nobody edits, whatever the task's status,
//     until the automation is re-run or the task is completed;
//   - nothing started → read only.

import type { FailedPhase, Job, JobStepAssignment } from '../api'
import { finalStepTooltip } from './completion-copy'
import { isAIStepRunning, isPipelineRunning, jobInFirstStep } from './project-logic'
import { isAutomatedStep } from './workflow-steps'

export type TaskMode =
  | 'loading'
  | 'editable'
  | 'reopenable'
  | 'errored'
  | 'automation_running'
  | 'read_only'
  | 'none'

export interface CurrentTask {
  /** The task's step assignment; null without a workflow or before any step started. */
  step: JobStepAssignment | null
  mode: TaskMode
}

type StepLike = Pick<JobStepAssignment, 'status' | 'step_order'>

/** The in-progress step, else the completed step with the highest step_order, else null. */
export function currentStep<S extends StepLike>(steps: readonly S[]): S | null {
  const active = steps.find(s => s.status === 'in_progress')
  if (active) return active
  let last: S | null = null
  for (const s of steps) {
    if (s.status === 'completed' && (last === null || s.step_order > last.step_order)) last = s
  }
  return last
}

/** The title over a task's hand-off note: the editor banner, the Tasks tab. */
export const HANDOFF_NOTE_TITLE = 'Comment from the previous step'

/** The step that sent the job back, and why ('' when no reason was given). */
export function sentBack<S extends StepLike & Pick<JobStepAssignment, 'step_name' | 'rejection_reason'>>(
  steps: readonly S[],
): { stepName: string; reason: string } | null {
  const s = steps.find(st => st.status === 'rejected')
  return s ? { stepName: s.step_name, reason: s.rejection_reason } : null
}

type JobState = Pick<Job, 'pipeline_status' | 'current_step'>

/**
 * The current task and its mode. `steps` and `job` are undefined while they
 * load; a job with no workflow steps is freely editable.
 */
export function currentTask(
  steps: JobStepAssignment[] | undefined,
  job: JobState | undefined,
  isManager: boolean,
): CurrentTask {
  if (!steps || !job) return { step: null, mode: 'loading' }
  if (steps.length === 0) return { step: null, mode: 'editable' }
  const step = currentStep(steps)
  if (!step) return { step: null, mode: 'none' }
  if (job.pipeline_status === 'failed') return { step, mode: 'errored' }
  if (step.status === 'in_progress') {
    return { step, mode: automationRunning(step, job) ? 'automation_running' : 'editable' }
  }
  return { step, mode: isManager ? 'reopenable' : 'read_only' }
}

/** True while the job's automation owns the text: the AI step's run, or the
 *  pre-translation a Pre-translation step completes itself after. */
function automationRunning(step: Pick<JobStepAssignment, 'step_type'>, job: JobState): boolean {
  return isAIStepRunning(job) || (step.step_type === 'pretranslate' && isPipelineRunning(job))
}

/** True for the modes in which the editor writes translation text. */
export function canEditTask(mode: TaskMode): boolean {
  return mode === 'editable' || mode === 'reopenable'
}

/**
 * Re-run is offered on a failed job. An AI-step failure needs a manager, like
 * the AI step's own re-run; a pre-translation failure is open to anyone who can
 * open the editor and, like Apply TM/MT, only in the first workflow step.
 */
export function canRerunTask(
  job: Pick<Job, 'pipeline_status' | 'failed_phase'> & { workflow_step?: { step_order: number } | null },
  isManager: boolean,
): boolean {
  if (job.pipeline_status !== 'failed') return false
  if (job.failed_phase === 'ai_step') return isManager
  return job.failed_phase === 'pretranslation' && jobInFirstStep(job)
}

/**
 * Skip is offered on a hybrid step whose AI run failed: the error is cleared
 * and the person works the step without the AI pass. `step` is the job's
 * current task. A manager decides, like the AI step's re-run. An `ai` step has
 * nobody working after the AI, so Complete step is the way on there.
 */
export function canSkipAIStep(
  job: Pick<Job, 'failed_phase'>,
  step: { status: string; step_type: string } | null,
  isManager: boolean,
): boolean {
  return isManager && job.failed_phase === 'ai_step' && step?.status === 'in_progress' && step.step_type === 'hybrid'
}

// --- Copy ---

const AUTOMATED_STEP_TEXT: Record<string, string> = {
  ai: 'AI step',
  pretranslate: 'pre-translation step',
}

const FAILED_PHASE_TEXT: Record<FailedPhase, string> = {
  ai_step: 'the AI step failed',
  pretranslation: 'pre-translation failed',
}

export type TooltipJob = Pick<Job, 'counts' | 'failed_phase'>

/** Tooltip of the breadcrumb chip: the task's state, then what it means now. */
export function taskStepTooltip(task: CurrentTask, step: JobStepAssignment, job: TooltipJob, isFinalStep: boolean): string {
  switch (task.mode) {
    case 'editable':
    case 'automation_running': {
      // Only a running automated step completes itself. One that is open
      // without a run (reopened by a manager, or a failed self-completion)
      // waits for a person.
      const head = !isAutomatedStep(step.step_type)
        ? `In progress${step.assignee_name ? ` — assigned to ${step.assignee_name}` : ''}`
        : `In progress — ${AUTOMATED_STEP_TEXT[step.step_type]}, ${task.mode === 'automation_running' ? 'completes itself' : 'complete it by hand'}`
      // The due date is the deadline chip's, beside this one.
      const final = isFinalStep
        ? `. ${finalStepTooltip({ approvedCount: job.counts.approved })}`
        : ''
      return head + final
    }
    case 'reopenable':
      return 'Completed — edit a segment to reopen it'
    case 'read_only':
      return 'Completed — read-only'
    case 'errored':
      return `Error — ${job.failed_phase ? FAILED_PHASE_TEXT[job.failed_phase] : 'the automation failed'}`
    default:
      return 'Not started'
  }
}
