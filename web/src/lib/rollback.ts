// Sending a job back (Reject): which earlier step it may go to, and whether
// that step's AI runs again. The server enforces the same rules.

import type { JobStepAssignment } from '../api'

type Step = Pick<JobStepAssignment, 'id' | 'step_type'>

/** Why a step cannot be picked as the target, or undefined when it can. */
export function rollbackBlockReason(steps: readonly Step[], index: number, currentIndex: number, isManager: boolean): string | undefined {
  if (index === currentIndex) return 'The current step'
  if (index > currentIndex) return 'Comes after the current step'
  const type = steps[index].step_type
  if (type === 'pretranslate') return 'Pre-translation cannot be run again from here'
  if (type === 'ai' && !isManager) return 'Only a manager can run the AI step again'
  return undefined
}

/** The step Reject picks first: the nearest earlier human or hybrid step. */
export function defaultRollbackTarget(steps: readonly Step[], currentIndex: number): number | null {
  for (let i = currentIndex - 1; i >= 0; i--) {
    if (steps[i].step_type === 'human' || steps[i].step_type === 'hybrid') return i
  }
  return null
}

/** Whether the user can send the job back anywhere from this step. */
export function canRollback(steps: readonly Step[], currentIndex: number, isManager: boolean): boolean {
  for (let i = 0; i < currentIndex; i++) {
    if (!rollbackBlockReason(steps, i, currentIndex, isManager)) return true
  }
  return false
}

/** How the target step's AI behaves: an AI step always runs again, a manager
 *  may choose to run a hybrid step's AI again, anything else never runs it. */
export function rerunChoice(step: Step | undefined, isManager: boolean): 'always' | 'optional' | 'never' {
  if (step?.step_type === 'ai') return 'always'
  if (step?.step_type === 'hybrid' && isManager) return 'optional'
  return 'never'
}
