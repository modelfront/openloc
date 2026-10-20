// TaskActions, TaskNotes, TaskDeadlineChip + TaskStepChip: what the CAT editor
// shows for each mode of the job's current task, and which actions it offers.
// The actions are the load-bearing part — a Reject on an errored task, or a
// Complete on a task a linguist may only read, is a button the server refuses.
//
// Anti-tautology RED proofs:
//   - render the editable body for 'errored' → "errored: no Reject" fails.
//   - drop the `step?.status === 'in_progress'` gate on the errored Complete →
//     "errored + completed task" fails.
//   - map read_only to the reopenable body → "read_only" fails (the reopen
//     hint appears for someone who cannot reopen).

import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import { TaskActions, TaskDeadlineChip, TaskNotes, TaskStepChip, type TaskActionsProps } from './TaskControls'
import { TooltipProvider } from '@/components/ui/tooltip'
import { chipSurfaceClass, chipVariants } from '@/components/ui/chip'
import { buttonVariants } from '@/components/ui/button'
import type { JobStepAssignment } from '../api'
import { taskStepTooltip, type CurrentTask, type TaskMode } from '../lib/current-task'
import { makeJob } from '@/test/fixtures/job'

afterEach(cleanup)

function step(overrides: Partial<JobStepAssignment> = {}): JobStepAssignment {
  return {
    id: 2, job_id: 1, workflow_step_id: 102, assigned_to: null, status: 'in_progress', started_at: null,
    completed_at: null, created_at: '2026-09-01T00:00:00Z', step_name: 'Review', step_order: 1,
    role_required: 'linguist', step_type: 'human', ai_router_id: null, assignee_name: null, assignee_email: null,
    rejection_reason: '', handoff_note: null, deadline: null, ...overrides,
  }
}

function renderActions(task: CurrentTask, extra: Partial<TaskActionsProps> = {}) {
  const props: TaskActionsProps = {
    task,
    isFinalStep: false,
    canRerun: true,
    canReject: true,
    onRerun: vi.fn(),
    onComplete: vi.fn(),
    onReject: vi.fn(),
    ...extra,
  }
  render(<TaskActions {...props} />)
  return props
}

const button = (name: RegExp) => screen.queryByRole('button', { name })

describe('TaskActions', () => {
  it('editable: Reject when an earlier step can take the job back, Complete step', async () => {
    const props = renderActions({ mode: 'editable', step: step() })
    await userEvent.click(button(/reject/i)!)
    await userEvent.click(button(/^complete step →$/i)!)
    expect(props.onReject).toHaveBeenCalledTimes(1)
    expect(props.onComplete).toHaveBeenCalledTimes(1)
    expect(button(/re-run/i)).toBeNull()
  })

  it('editable with nowhere to send the job back: no Reject; on the final step: Complete final step', () => {
    renderActions({ mode: 'editable', step: step({ step_order: 0 }) }, { isFinalStep: true, canReject: false })
    expect(button(/reject/i)).toBeNull()
    expect(button(/complete final step/i)).toBeInTheDocument()
  })

  it('the buttons take their colour from the Button tones: final, success, destructive', () => {
    // The class that carries each tone's colour in button.tsx.
    const TONE_CLASS = { final: 'bg-violet-600', success: 'bg-green-600', destructive: 'text-destructive' } as const
    const hasTone = (el: HTMLElement | null, variant: keyof typeof TONE_CLASS) => {
      expect(buttonVariants({ variant })).toContain(TONE_CLASS[variant])
      expect(el).toHaveClass(TONE_CLASS[variant])
    }
    renderActions({ mode: 'editable', step: step() }, { isFinalStep: true })
    hasTone(button(/complete final step/i), 'final')
    hasTone(button(/reject/i), 'destructive')
    cleanup()
    renderActions({ mode: 'editable', step: step() })
    hasTone(button(/^complete step →$/i), 'success')
  })

  it('reopenable: Completed with the reopen hint, and nothing to click', () => {
    renderActions({ mode: 'reopenable', step: step({ status: 'completed' }) })
    expect(screen.getByText('Completed')).toBeInTheDocument()
    expect(screen.getByText('Edit a segment to reopen this task.')).toBeInTheDocument()
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })

  it('read_only: Completed and Read-only, no reopen hint, nothing to click', () => {
    renderActions({ mode: 'read_only', step: step({ status: 'completed' }) })
    expect(screen.getByText('Completed')).toBeInTheDocument()
    expect(screen.getByText('Read-only')).toBeInTheDocument()
    expect(screen.queryByText(/reopen/i)).toBeNull()
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })

  it('errored: Re-run and Complete — no Reject, even past the first step', async () => {
    const props = renderActions({ mode: 'errored', step: step({ step_order: 2 }) })
    expect(button(/reject/i)).toBeNull()
    await userEvent.click(button(/^re-run$/i)!)
    await userEvent.click(button(/complete step/i)!)
    expect(props.onRerun).toHaveBeenCalledTimes(1)
    expect(props.onComplete).toHaveBeenCalledTimes(1)
  })

  it('errored without the right to re-run: Complete only', () => {
    renderActions({ mode: 'errored', step: step() }, { canRerun: false })
    expect(button(/re-run/i)).toBeNull()
    expect(button(/complete step/i)).toBeInTheDocument()
  })

  it('errored + completed task: no Complete, there is no step left to complete', () => {
    renderActions({ mode: 'errored', step: step({ status: 'completed' }) })
    expect(button(/complete/i)).toBeNull()
    expect(button(/^re-run$/i)).toBeInTheDocument()
  })

  it('none: Not started, nothing to click', () => {
    renderActions({ mode: 'none', step: null })
    expect(screen.getByText('Not started')).toBeInTheDocument()
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })

  it('loading: renders nothing', () => {
    renderActions({ mode: 'loading', step: null })
    expect(screen.queryByTestId('task-actions')).toBeNull()
  })
})

describe('TaskNotes', () => {
  const steps = [step({ id: 1, step_order: 0, step_name: 'Translation', status: 'completed' }), step()]

  it('errored: shows the job\'s error, or a fallback without one', () => {
    render(<TaskNotes task={{ mode: 'errored', step: step() }} steps={steps} errorMessage="The MT engine did not respond." />)
    expect(screen.getByTestId('task-error')).toHaveTextContent('The MT engine did not respond.')
    cleanup()
    render(<TaskNotes task={{ mode: 'errored', step: step() }} steps={steps} />)
    expect(screen.getByTestId('task-error')).toHaveTextContent('The automation failed.')
  })

  it('errored with a skip offered: Skip AI step sits in the error row and calls back', async () => {
    const onSkipAIStep = vi.fn()
    render(<TooltipProvider><TaskNotes task={{ mode: 'errored', step: step() }} steps={steps} errorMessage="The AI engine failed." onSkipAIStep={onSkipAIStep} /></TooltipProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Skip AI step' }))
    expect(onSkipAIStep).toHaveBeenCalledOnce()
    expect(screen.getByTestId('task-error')).toContainElement(screen.getByTestId('task-skip-ai-btn'))
  })

  it('errored without a skip offered: no Skip AI step', () => {
    render(<TaskNotes task={{ mode: 'errored', step: step() }} steps={steps} errorMessage="The AI engine failed." />)
    expect(button(/Skip AI step/)).toBeNull()
  })

  it('no error row outside the errored mode', () => {
    render(<TaskNotes task={{ mode: 'editable', step: step() }} steps={steps} errorMessage="stale message" />)
    expect(screen.queryByTestId('task-error')).toBeNull()
  })

  it('shows why a later step sent the job back; the toggle hides and shows it, keeping line breaks', async () => {
    const reason = 'Wrong terms\nSee the glossary'
    render(<TaskNotes task={{ mode: 'editable', step: step({ step_order: 0 }) }} steps={[
      step({ step_order: 0 }), step({ id: 3, step_order: 1, step_name: 'Review', status: 'rejected', rejection_reason: reason }),
    ]} />)
    const toggle = screen.getByRole('button', { name: /sent back from review/i })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const body = screen.getByText(/Wrong terms/)
    expect(body.textContent).toBe(reason)
    expect(body).toHaveClass('whitespace-pre-wrap')
    await userEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText(/Wrong terms/)).toBeNull()
    await userEvent.click(toggle)
    expect(screen.getByText(/Wrong terms/)).toBeInTheDocument()
  })

  it('shows the hand-off note of the task in progress; the toggle hides and shows it', async () => {
    render(<TaskNotes task={{ mode: 'editable', step: step({ handoff_note: 'Please check terminology' }) }} steps={steps} />)
    const toggle = screen.getByRole('button', { name: /comment from the previous step/i })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByTestId('task-handoff-note')).toHaveTextContent('Please check terminology')
    await userEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('Please check terminology')).toBeNull()
    await userEvent.click(toggle)
    expect(screen.getByText('Please check terminology')).toBeInTheDocument()
  })

  it('no hand-off banner without a note, or once the task is no longer in progress', () => {
    render(<TaskNotes task={{ mode: 'editable', step: step() }} steps={steps} />)
    expect(screen.queryByTestId('task-handoff-note')).toBeNull()
    cleanup()
    render(<TaskNotes task={{ mode: 'read_only', step: step({ status: 'completed', handoff_note: 'Old note' }) }} steps={steps} />)
    expect(screen.queryByTestId('task-handoff-note')).toBeNull()
  })

  // Each banner is a surface carrying a state, so its background is the chip
  // palette's, not a local colour.
  it.each<[string, 'red' | 'blue', () => void]>([
    ['task-error', 'red', () => render(<TaskNotes task={{ mode: 'errored', step: step() }} steps={steps} errorMessage="Failed." />)],
    ['task-sent-back', 'red', () => render(<TaskNotes task={{ mode: 'editable', step: step({ step_order: 0 }) }} steps={[
      step({ step_order: 0 }), step({ id: 3, step_order: 1, step_name: 'Review', status: 'rejected', rejection_reason: 'Wrong terms' }),
    ]} />)],
    ['task-handoff-note', 'blue', () => render(<TaskNotes task={{ mode: 'editable', step: step({ handoff_note: 'Check terms' }) }} steps={steps} />)],
  ])('%s takes its surface from the %s chip palette', (testId, color, renderBanner) => {
    renderBanner()
    const banner = screen.getByTestId(testId)
    for (const cls of chipSurfaceClass(color).split(' ')) expect(banner).toHaveClass(cls)
  })
})

describe('TaskDeadlineChip', () => {
  const renderChip = (task: CurrentTask) => render(<TooltipProvider><TaskDeadlineChip task={task} /></TooltipProvider>)

  it('a human task in progress shows its deadline, with the date in a tooltip rather than a title', () => {
    renderChip({ mode: 'editable', step: step({ deadline: '2030-01-15T12:00:00Z' }) })
    const chip = screen.getByTestId('cat-step-deadline')
    expect(chip).toHaveTextContent(/^Due /)
    expect(chip).not.toHaveAttribute('title')
  })

  it('no deadline chip on an AI step, or once the task is no longer in progress', () => {
    renderChip({ mode: 'editable', step: step({ step_type: 'ai' }) })
    expect(screen.queryByTestId('cat-step-deadline')).toBeNull()
    for (const mode of ['reopenable', 'read_only', 'errored', 'none'] as const) {
      cleanup()
      renderChip({ mode, step: mode === 'none' ? null : step({ status: 'completed' }) })
      expect(screen.queryByTestId('cat-step-deadline')).toBeNull()
    }
  })
})

describe('TaskStepChip', () => {
  const job = makeJob({ failed_phase: 'ai_step' })
  const steps = [step({ id: 1, step_order: 0, step_name: 'Translation', status: 'pending' }), step({ status: 'pending' })]

  it.each<[TaskMode, 'blue' | 'green' | 'red' | 'grey']>([
    ['editable', 'blue'],
    ['automation_running', 'blue'],
    ['reopenable', 'green'],
    ['read_only', 'green'],
    ['errored', 'red'],
  ])('%s → a %s chip naming the step', (mode, color) => {
    render(<TooltipProvider><TaskStepChip task={{ mode, step: step() }} steps={steps} job={job} isFinalStep={false} /></TooltipProvider>)
    const chip = screen.getByTestId('cat-current-step')
    expect(chip).toHaveTextContent('Review')
    for (const cls of chipVariants({ color }).split(' ')) expect(chip).toHaveClass(cls)
  })

  it('before anything starts, names the first step in grey', () => {
    render(<TooltipProvider><TaskStepChip task={{ mode: 'none', step: null }} steps={[...steps].reverse()} job={job} isFinalStep={false} /></TooltipProvider>)
    const chip = screen.getByTestId('cat-current-step')
    expect(chip).toHaveTextContent('Translation')
    for (const cls of chipVariants({ color: 'grey' }).split(' ')) expect(chip).toHaveClass(cls)
  })
})

describe('taskStepTooltip', () => {
  const job = makeJob({ failed_phase: 'ai_step' })
  const ai = step({ step_type: 'ai', step_name: 'QE' })

  it('only a running AI step claims it completes itself', () => {
    expect(taskStepTooltip({ mode: 'automation_running', step: ai }, ai, job, false)).toBe('In progress — AI step, completes itself')
    // Reopened by a manager, or left open after a failed self-completion: a
    // person completes it.
    expect(taskStepTooltip({ mode: 'editable', step: ai }, ai, job, false)).toBe('In progress — AI step, complete it by hand')
    expect(taskStepTooltip({ mode: 'errored', step: ai }, ai, job, false)).toBe('Error — the AI step failed')
    expect(taskStepTooltip({ mode: 'read_only', step: ai }, ai, job, false)).toBe('Completed — read-only')
    expect(taskStepTooltip({ mode: 'reopenable', step: ai }, ai, job, false)).toBe('Completed — edit a segment to reopen it')
  })

  it('a Pre-translation step completes itself while its run is on, else waits for a person', () => {
    const pre = step({ step_type: 'pretranslate', step_name: 'Pre-translation' })
    expect(taskStepTooltip({ mode: 'automation_running', step: pre }, pre, job, false)).toBe('In progress — pre-translation step, completes itself')
    expect(taskStepTooltip({ mode: 'editable', step: pre }, pre, job, false)).toBe('In progress — pre-translation step, complete it by hand')
  })

  it('a human step in progress names its assignee; the deadline chip beside it has the date', () => {
    const s = step({ assignee_name: 'Ana', deadline: '2026-10-03T12:00:00Z' })
    expect(taskStepTooltip({ mode: 'editable', step: s }, s, job, false)).toBe('In progress — assigned to Ana')
    expect(taskStepTooltip({ mode: 'editable', step: s }, s, job, true)).toMatch(/^In progress — assigned to Ana\. Final step/)
  })

  it('names the failed automation, or falls back when the phase is unknown', () => {
    const s = step()
    expect(taskStepTooltip({ mode: 'errored', step: s }, s, makeJob({ failed_phase: 'pretranslation' }), false))
      .toBe('Error — pre-translation failed')
    expect(taskStepTooltip({ mode: 'errored', step: s }, s, makeJob(), false)).toBe('Error — the automation failed')
  })
})
