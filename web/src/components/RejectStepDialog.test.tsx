// The Reject dialog shows the whole workflow and lets the user pick any
// earlier step the server accepts; the AI re-run choice follows the picked step.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  listAIRouters: vi.fn().mockResolvedValue([]),
  listMembers: vi.fn().mockResolvedValue([]),
}))

import type { JobStepAssignment, StepType } from '../api'
import { RejectStepDialog } from './RejectStepDialog'

afterEach(cleanup)

function step(id: number, step_type: StepType, status: string, ai_router_id: number | null = null): JobStepAssignment {
  return {
    id, job_id: 1, workflow_step_id: 100 + id, assigned_to: null, status, started_at: null, completed_at: null,
    created_at: '2026-10-01T00:00:00Z', step_name: `Step ${id}`, step_order: id - 1, role_required: 'linguist',
    step_type, ai_router_id, assignee_name: null, assignee_email: null, rejection_reason: '', handoff_note: null,
    deadline: null,
  }
}

// Pre-translation, Translation, Auto (AI), Check (hybrid), Review (current), Final.
const steps = [
  step(1, 'pretranslate', 'completed'),
  step(2, 'human', 'completed'),
  step(3, 'ai', 'completed', 9),
  step(4, 'hybrid', 'completed', 9),
  step(5, 'human', 'in_progress'),
  step(6, 'human', 'pending'),
]

function renderDialog(isManager: boolean) {
  const onConfirm = vi.fn()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RejectStepDialog open onOpenChange={() => {}} steps={steps} currentIndex={4} isManager={isManager} pending={false} onConfirm={onConfirm} />
    </QueryClientProvider>,
  )
  return onConfirm
}

const pick = (i: number) => screen.getByTestId(`workflow-step-pick-${i}`) as HTMLInputElement

describe('RejectStepDialog', () => {
  it('shows every step with its state and picks the nearest earlier human or hybrid step', () => {
    renderDialog(false)
    for (let i = 0; i < steps.length; i++) expect(screen.getByTestId(`workflow-step-option-${i}`)).toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-option-4')).toHaveTextContent('In progress')
    expect(screen.getByTestId('workflow-step-option-5')).toHaveTextContent('Not started')
    expect(pick(3).checked).toBe(true)
  })

  it('blocks pre-translation, the current and later steps, and AI steps for a non-manager', () => {
    renderDialog(false)
    expect([0, 1, 2, 3, 4, 5].map(i => pick(i).disabled)).toEqual([true, false, true, false, true, true])
    expect(screen.getByTestId('workflow-step-option-2')).toHaveAttribute('title', 'Only a manager can run the AI step again')
    // A non-manager is not offered the AI re-run on a hybrid step.
    expect(screen.queryByTestId('reject-rerun-ai')).toBeNull()
  })

  it('a manager may re-run a hybrid step’s AI; an AI step offers no choice, as it always runs again', async () => {
    const onConfirm = renderDialog(true)
    await userEvent.click(screen.getByTestId('reject-rerun-ai'))
    await userEvent.type(screen.getByLabelText(/rejection reason/i), 'Engines changed')
    await userEvent.click(screen.getByTestId('reject-confirm'))
    expect(onConfirm).toHaveBeenLastCalledWith({ reason: 'Engines changed', toStepId: 4, rerunAI: true })

    await userEvent.click(pick(2))
    expect(screen.queryByTestId('reject-rerun-ai')).toBeNull()
    expect(screen.getByTestId('reject-confirm')).toHaveTextContent('Send back to Step 3')

    await userEvent.click(pick(1))
    expect(screen.getByTestId('reject-confirm')).toHaveTextContent('Send back to Step 2')
    await userEvent.click(screen.getByTestId('reject-confirm'))
    expect(onConfirm).toHaveBeenLastCalledWith({ reason: 'Engines changed', toStepId: 2, rerunAI: false })
  })
})
