// The editor's current task + mode. This is the rule every write gate in the
// CAT editor reads, and the backend's internal/taskedit applies the same one —
// so a wrong row here is an editor that writes where the server refuses, or a
// read-only editor the server would have accepted.
//
// Anti-tautology RED proofs (each verified by editing current-task.ts):
//   - pick the FIRST completed step instead of the highest step_order → the
//     "last completed" currentStep case fails.
//   - check `failed` after the status branches → "failed + completed" fails
//     (the manager sees reopenable).
//   - drop `isManager` from the completed branch → "completed, linguist" fails.

import { describe, it, expect } from 'vitest'
import type { JobStepAssignment } from '../api'
import { canEditTask, canRerunTask, canSkipAIStep, currentStep, currentTask, sentBack, type TaskMode } from './current-task'

function step(id: number, step_order: number, status: string, overrides: Partial<JobStepAssignment> = {}): JobStepAssignment {
  return {
    id, job_id: 1, workflow_step_id: 100 + id, assigned_to: null, status, started_at: null,
    completed_at: null, created_at: '2026-09-01T00:00:00Z', step_name: `step ${id}`, step_order,
    role_required: 'linguist', step_type: 'human', ai_router_id: null, assignee_name: null, assignee_email: null,
    rejection_reason: '', handoff_note: null, deadline: null, ...overrides,
  }
}

const idle = { pipeline_status: 'in_progress', current_step: 'Pre-translation complete' }
const failed = { pipeline_status: 'failed', current_step: 'MT: 3 / 9 segments' }
const aiRunning = { pipeline_status: 'in_progress', current_step: 'AI step (QE+APE): 3 / 9' }
const aiDone = { pipeline_status: 'in_progress', current_step: 'AI step complete' }
const preQueued = { pipeline_status: 'queued', current_step: 'Queued for pre-translation' }
const preRunning = { pipeline_status: 'in_progress', current_step: 'MT: 3 / 9 segments' }
const preStep = (status: string) => step(9, 0, status, { step_type: 'pretranslate', step_name: 'Pre-translation' })

describe('currentStep', () => {
  it.each([
    ['the in-progress step wins over completed ones',
      [step(1, 0, 'completed'), step(2, 1, 'in_progress'), step(3, 2, 'pending')], 2],
    ['the in-progress step wins even when a later step completed before a rejection',
      [step(1, 0, 'in_progress'), step(2, 1, 'rejected'), step(3, 2, 'completed')], 1],
    ['with nothing in progress, the completed step with the highest step_order',
      [step(1, 0, 'completed'), step(3, 2, 'completed'), step(2, 1, 'completed')], 3],
    ['a rejected or pending step is never current',
      [step(1, 0, 'rejected'), step(2, 1, 'pending')], null],
    ['no steps', [], null],
  ])('%s', (_name, steps, wantId) => {
    expect(currentStep(steps)?.id ?? null).toBe(wantId)
  })
})

describe('sentBack', () => {
  it('names the step that sent the job back, with its reason', () => {
    const steps = [step(1, 0, 'in_progress'), step(2, 1, 'rejected', { step_name: 'Review', rejection_reason: 'Fix the terms.' })]
    expect(sentBack(steps)).toEqual({ stepName: 'Review', reason: 'Fix the terms.' })
  })
  it('a send-back without a reason still counts', () => {
    expect(sentBack([step(1, 0, 'in_progress'), step(2, 1, 'rejected', { step_name: 'Review' })]))
      .toEqual({ stepName: 'Review', reason: '' })
  })
  it('none once nothing is rejected', () => {
    expect(sentBack([step(1, 0, 'completed'), step(2, 1, 'in_progress')])).toBeNull()
  })
})

describe('currentTask', () => {
  const inProgress = [step(1, 0, 'completed'), step(2, 1, 'in_progress')]
  const allDone = [step(1, 0, 'completed'), step(2, 1, 'completed')]
  const notStarted = [step(1, 0, 'pending'), step(2, 1, 'pending')]

  it.each<[string, JobStepAssignment[] | undefined, typeof idle | undefined, boolean, TaskMode, number | null]>([
    ['steps still loading', undefined, idle, true, 'loading', null],
    ['job still loading', inProgress, undefined, true, 'loading', null],
    ['no workflow steps', [], idle, false, 'editable', null],
    ['in progress, linguist', inProgress, idle, false, 'editable', 2],
    ['in progress, manager', inProgress, idle, true, 'editable', 2],
    ['in progress while the AI step runs', inProgress, aiRunning, true, 'automation_running', 2],
    ['in progress after the AI step finished', inProgress, aiDone, false, 'editable', 2],
    ['completed, manager reopens by editing', allDone, idle, true, 'reopenable', 2],
    ['completed, linguist reads only', allDone, idle, false, 'read_only', 2],
    ['failed + in progress', inProgress, failed, true, 'errored', 2],
    ['failed + completed: the server refuses the reopen too', allDone, failed, true, 'errored', 2],
    ['nothing started', notStarted, idle, true, 'none', null],
    ['nothing started on a failed job', notStarted, failed, true, 'none', null],
    // The Pre-translation step's work IS the pipeline: locked while it runs,
    // and it then completes itself. A human first step stays editable.
    ['Pre-translation step while pre-translation is queued', [preStep('in_progress')], preQueued, true, 'automation_running', 9],
    ['Pre-translation step while pre-translation runs', [preStep('in_progress')], preRunning, true, 'automation_running', 9],
    ['human first step while pre-translation runs', [step(1, 0, 'in_progress')], preRunning, false, 'editable', 1],
    ['Pre-translation step reopened by a manager', [preStep('in_progress')], idle, true, 'editable', 9],
    ['Pre-translation step completed, manager reopens by editing', [preStep('completed')], idle, true, 'reopenable', 9],
    ['Pre-translation step completed, linguist reads only', [preStep('completed')], idle, false, 'read_only', 9],
  ])('%s', (_name, steps, job, isManager, wantMode, wantStepId) => {
    const task = currentTask(steps, job, isManager)
    expect(task.mode).toBe(wantMode)
    expect(task.step?.id ?? null).toBe(wantStepId)
  })
})

describe('canEditTask', () => {
  it.each<[TaskMode, boolean]>([
    ['editable', true],
    ['reopenable', true],
    ['loading', false],
    ['errored', false],
    ['automation_running', false],
    ['read_only', false],
    ['none', false],
  ])('%s → %s', (mode, want) => {
    expect(canEditTask(mode)).toBe(want)
  })
})

describe('canRerunTask', () => {
  it.each<[string, { pipeline_status: string; failed_phase?: 'ai_step' | 'pretranslation'; workflow_step?: { step_order: number } }, boolean, boolean]>([
    ['AI-step error, manager', { pipeline_status: 'failed', failed_phase: 'ai_step' }, true, true],
    ['AI-step error on a later step, manager', { pipeline_status: 'failed', failed_phase: 'ai_step', workflow_step: { step_order: 1 } }, true, true],
    // Re-running pre-translation has Apply TM/MT's first-step gate (server 403s otherwise).
    ['pre-translation error in the first step', { pipeline_status: 'failed', failed_phase: 'pretranslation', workflow_step: { step_order: 0 } }, true, true],
    ['pre-translation error on a later step', { pipeline_status: 'failed', failed_phase: 'pretranslation', workflow_step: { step_order: 1 } }, true, false],
    ['AI-step error, linguist', { pipeline_status: 'failed', failed_phase: 'ai_step' }, false, false],
    ['pre-translation error, linguist', { pipeline_status: 'failed', failed_phase: 'pretranslation' }, false, true],
    ['pre-translation error, manager', { pipeline_status: 'failed', failed_phase: 'pretranslation' }, true, true],
    ['failed without a phase', { pipeline_status: 'failed' }, true, false],
    ['not failed', { pipeline_status: 'in_progress', failed_phase: 'ai_step' }, true, false],
  ])('%s → %s', (_name, job, isManager, want) => {
    expect(canRerunTask(job, isManager)).toBe(want)
  })
})

describe('canSkipAIStep', () => {
  const hybrid = step(2, 1, 'in_progress', { step_type: 'hybrid' })
  it.each<[string, JobStepAssignment | null, 'ai_step' | 'pretranslation' | undefined, boolean, boolean]>([
    ['AI-step error on a hybrid step, manager', hybrid, 'ai_step', true, true],
    ['AI-step error on a hybrid step, linguist', hybrid, 'ai_step', false, false],
    // Nobody works after an AI-only step: Complete step is the way on.
    ['AI-step error on an AI-only step', step(2, 1, 'in_progress', { step_type: 'ai' }), 'ai_step', true, false],
    ['pre-translation error on a hybrid step', hybrid, 'pretranslation', true, false],
    ['hybrid step no longer in progress', step(2, 1, 'completed', { step_type: 'hybrid' }), 'ai_step', true, false],
    ['no error on the job', hybrid, undefined, true, false],
    ['no current step', null, 'ai_step', true, false],
  ])('%s', (_name, s, failed_phase, isManager, want) => {
    expect(canSkipAIStep({ failed_phase }, s, isManager)).toBe(want)
  })
})
