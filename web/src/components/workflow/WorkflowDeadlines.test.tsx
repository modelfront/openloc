// WorkflowStepsWithDeadlines: when each step is due, per target language.
//
// Pinned (the resolution rule itself is server-side — see
// internal/file/step_deadline_inheritance_test.go):
//   - Each step's own date renders on its row, and a language's override in its
//     own row, without the two being confused for each other.
//   - AI steps get a chip but never a deadline: they run by themselves.
//   - An edit lands at the step's position in the FULL list, AI steps included.
//   - The per-language rows stay collapsed behind the globe toggle, and open
//     showing whatever is already stored.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { WorkflowStepsWithDeadlines } from './WorkflowDeadlines'
import { clearDeadline, pickDeadline, PICKER_FLOW_TIMEOUT } from '@/test/deadline-picker'
import type { WorkflowStep } from '../WorkflowStepsEditor'

afterEach(() => cleanup())

const translation: WorkflowStep = {
  step_definition_id: 1, name: 'Translation', step_type: 'human',
  deadline: '2027-02-01T17:00:00Z',
  language_deadlines: { de: '2027-01-20T09:00:00Z' },
}
const review: WorkflowStep = { step_definition_id: 2, name: 'Review', step_type: 'human', deadline: null }
const autoAPE: WorkflowStep = { step_definition_id: 3, name: 'Auto APE', step_type: 'ai', ai_router_id: 9 }

// The merged list the wizard and an attached project workflow show: the step
// chips with each step's deadline on the same row.
describe('WorkflowStepsWithDeadlines', () => {
  function setupMerged(
    steps: WorkflowStep[] = [translation, review, autoAPE],
    children?: React.ReactNode,
    targetLanguages = ['de', 'fr'],
  ) {
    const onChange = vi.fn()
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    qc.setQueryData(['members'], [])
    qc.setQueryData(['ai-routers'], [])
    qc.setQueryData(['languages'], [])
    render(
      <MemoryRouter>
      <QueryClientProvider client={qc}>
        <WorkflowStepsWithDeadlines
          steps={steps}
          targetLanguages={targetLanguages}
          onChange={onChange}
        >
          {children}
        </WorkflowStepsWithDeadlines>
      </QueryClientProvider>
      </MemoryRouter>,
    )
    return onChange
  }

  it('puts each step\'s deadline on the step\'s own row', () => {
    setupMerged()
    // One list: the step chip and its field share a row. The exact value, not
    // just "non-empty" — de's override must not leak into the step's field.
    expect(screen.getByTestId('workflow-step-badge-0')).toHaveTextContent('Translation')
    expect(screen.getByTestId('workflow-deadline-input-0'))
      .toHaveAttribute('data-value', new Date(translation.deadline!).toISOString())
    expect(screen.getByTestId('workflow-step-badge-1')).toHaveTextContent('Review')
    expect(screen.getByTestId('workflow-deadline-input-1')).toHaveAttribute('data-value', '')
  })

  it('gives an AI step a chip but no field — nobody is waiting on it', () => {
    setupMerged()
    expect(screen.getByTestId('workflow-step-badge-2')).toHaveTextContent('Auto APE')
    expect(screen.queryByTestId('workflow-deadline-input-2')).toBeNull()
    expect(screen.queryByTestId('workflow-deadline-langs-toggle-2')).toBeNull()
  })

  it('keeps the per-language rows collapsed under their step', () => {
    setupMerged()
    expect(screen.queryByTestId('workflow-deadline-override-input-0-0')).toBeNull()
    fireEvent.click(screen.getByTestId('workflow-deadline-langs-toggle-0'))
    expect(screen.getByTestId('workflow-deadline-override-input-0-0')).toBeInTheDocument()
    // Opening one step leaves the others alone.
    expect(screen.queryByTestId('workflow-deadline-override-input-1-0')).toBeNull()
  })

  it('writes an edit back at the step\'s position in the FULL list', () => {
    const onChange = setupMerged()
    pickDeadline('workflow-deadline-input-1', '2027-03-15', '12:00')
    const next = onChange.mock.calls[0][0] as WorkflowStep[]
    expect(next[1].deadline).toContain('2027-03-15')
    expect(next[0]).toEqual(translation)
    expect(next[2]).toEqual(autoAPE)
  }, PICKER_FLOW_TIMEOUT)

  it('renders the caller\'s actions under the list', () => {
    setupMerged([translation], <span data-testid="save-row">Saving…</span>)
    expect(screen.getByTestId('save-row')).toBeInTheDocument()
  })

  it('opens on the stored overrides, or on one empty row when there are none', () => {
    setupMerged()
    fireEvent.click(screen.getByTestId('workflow-deadline-langs-toggle-0'))
    // Translation carries de's date — the override's own value, not the step's.
    expect(screen.getByTestId('workflow-deadline-override-input-0-0'))
      .toHaveAttribute('data-value', new Date(translation.language_deadlines!.de!).toISOString())

    // Review carries none: one empty row, ready to fill in.
    fireEvent.click(screen.getByTestId('workflow-deadline-langs-toggle-1'))
    expect(screen.getByTestId('workflow-deadline-override-input-1-0')).toHaveAttribute('data-value', '')
  })

  it('writes a language date back onto the step it belongs to', () => {
    const onChange = setupMerged()
    fireEvent.click(screen.getByTestId('workflow-deadline-langs-toggle-0'))
    pickDeadline('workflow-deadline-override-input-0-0', '2027-03-15', '12:00')

    const next = onChange.mock.calls[0][0] as WorkflowStep[]
    // The row carried de, so the new date lands on de and nothing else.
    expect(next[0].language_deadlines?.de).toContain('2027-03-15')
    expect(next[0].language_deadlines?.fr).toBeUndefined()
    expect(next[1]).toEqual(review)
  }, PICKER_FLOW_TIMEOUT)

  it('clearing a row\'s date removes the override rather than storing an empty one', () => {
    const onChange = setupMerged()
    fireEvent.click(screen.getByTestId('workflow-deadline-langs-toggle-0'))
    clearDeadline('workflow-deadline-override-input-0-0')

    const next = onChange.mock.calls[0][0] as WorkflowStep[]
    expect(next[0].language_deadlines).toEqual({})
  })

  it('drops the per-language sections when the project has no target languages yet', () => {
    setupMerged([translation], undefined, [])
    expect(screen.getByTestId('workflow-deadline-input-0')).toBeInTheDocument()
    expect(screen.queryByTestId('workflow-deadline-langs-toggle-0')).toBeNull()
  })
})
