// The Workflow tab's deadline autosave.
//
// Steps themselves are library-only; deadlines are the one
// thing still editable here, and they save with no button. What that has to get
// right, and what these pin:
//   - An edit that nets to no change must NOT write — a blank override row
//     added and removed sends the same list back, and writing it would re-date
//     the project's live jobs for nothing.
//   - One write per settled edit, after the debounce, not one per keystroke.
//   - Leaving mid-debounce still saves.
//   - A rejected save drops the draft, so the field shows what is stored rather
//     than a date the server refused.

import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { ComponentProps } from 'react'
import { WorkflowTab } from './WorkflowTab'
import type { WorkflowStep } from '../WorkflowStepsEditor'
import { pickDeadline, PICKER_FLOW_TIMEOUT } from '@/test/deadline-picker'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const steps: WorkflowStep[] = [
  { step_definition_id: 1, name: 'Translation', step_type: 'human', deadline: null },
]

beforeEach(() => vi.useFakeTimers())
afterEach(() => { vi.useRealTimers(); cleanup() })

const pretranslateStep: WorkflowStep = { step_definition_id: 5, name: 'Pre-translation', step_type: 'pretranslate', deadline: null }

function renderTab(props: Partial<ComponentProps<typeof WorkflowTab>> = {}, workflows: unknown[] = []) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['workflows'], workflows)
  qc.setQueryData(['workflows', 'pretranslate-only'], { id: 99, name: 'Pre-translation', steps: [pretranslateStep] })
  qc.setQueryData(['members'], [])
  qc.setQueryData(['ai-routers'], [])
  qc.setQueryData(['languages'], [])
  const ui = (p: Partial<ComponentProps<typeof WorkflowTab>>) => (
    <QueryClientProvider client={qc}>
      <MemoryRouter>
      <WorkflowTab
        steps={steps}
        workflowId={7}
        workflowName="Standard"
        onApplyWorkflow={vi.fn().mockResolvedValue(undefined)}
        {...p}
      />
      </MemoryRouter>
    </QueryClientProvider>
  )
  const { rerender } = render(ui(props))
  return { rerender: (p: Partial<ComponentProps<typeof WorkflowTab>>) => rerender(ui(p)) }
}

function setup(onSaveSteps = vi.fn().mockResolvedValue(undefined)) {
  renderTab({ sourceLang: 'en', targetLangs: ['de', 'fr'], onSaveSteps })
  return onSaveSteps
}

/** Set the step's deadline through its picker: "yyyy-MM-ddTHH:mm", in the browser's zone. */
function setDeadline(value: string) {
  const [day, time] = value.split('T')
  pickDeadline('workflow-deadline-input-0', day, time)
}

describe('WorkflowTab deadline autosave', () => {
  it('does not write when an edit nets to no change', async () => {
    const onSaveSteps = setup()
    // Opening the per-language section seeds a blank override row; removing it
    // reports a list identical to the stored one.
    act(() => { fireEvent.click(screen.getByTestId('workflow-deadline-langs-toggle-0')) })
    act(() => { fireEvent.click(screen.getByTestId('workflow-deadline-override-remove-0-0')) })
    await act(async () => { vi.advanceTimersByTime(5000) })
    expect(onSaveSteps, 'wrote a list identical to the stored one').not.toHaveBeenCalled()
  })

  it('writes once, after the debounce, for a settled edit', async () => {
    const onSaveSteps = setup()
    setDeadline('2027-03-15T12:00')
    expect(onSaveSteps, 'wrote before the debounce elapsed').not.toHaveBeenCalled()

    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(onSaveSteps).toHaveBeenCalledTimes(1)
    expect(onSaveSteps.mock.calls[0][0][0].deadline).toContain('2027-03-15')
  }, PICKER_FLOW_TIMEOUT)

  it('collapses rapid edits into one write carrying the last value', async () => {
    const onSaveSteps = setup()
    setDeadline('2027-03-15T12:00')
    setDeadline('2027-03-16T12:00')
    setDeadline('2027-03-17T12:00')
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(onSaveSteps).toHaveBeenCalledTimes(1)
    expect(onSaveSteps.mock.calls[0][0][0].deadline).toContain('2027-03-17')
  }, PICKER_FLOW_TIMEOUT)

  it('flushes a pending edit when the tab unmounts', async () => {
    const onSaveSteps = setup()
    setDeadline('2027-03-15T12:00')
    expect(onSaveSteps).not.toHaveBeenCalled()
    await act(async () => { cleanup() })
    expect(onSaveSteps, 'the last edit was lost on unmount').toHaveBeenCalledTimes(1)
  }, PICKER_FLOW_TIMEOUT)

  it('drops the draft when the save is rejected, so the field stops showing a date the server refused', async () => {
    const onSaveSteps = vi.fn().mockRejectedValue(new Error('nope'))
    setup(onSaveSteps)
    setDeadline('2027-03-15T12:00')
    expect(screen.getByTestId('workflow-deadline-input-0'))
      .toHaveAttribute('data-value', new Date('2027-03-15T12:00').toISOString())

    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(onSaveSteps).toHaveBeenCalledTimes(1)
    // Back to the stored list, which has no deadline.
    expect(screen.getByTestId('workflow-deadline-input-0')).toHaveAttribute('data-value', '')
  }, PICKER_FLOW_TIMEOUT)
})

describe('WorkflowTab mode toggle', () => {
  const library = [
    { id: 7, name: 'Standard', steps: [{ step_definition_id: 1, name: 'Translation', step_type: 'human' }] },
    { id: 8, name: 'Auto APE', steps: [{ step_definition_id: 2, name: 'Auto APE', step_type: 'ai' }] },
  ]

  it('is not offered on a project', () => {
    setup()
    expect(screen.queryByTestId('workflow-mode')).not.toBeInTheDocument()
  })

  it('is offered on a template, reading Workflow for steps a person works', () => {
    renderTab({}, library)
    expect(screen.getByTestId('workflow-mode-workflow')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('workflow-mode-raw_mt')).toHaveAttribute('aria-checked', 'false')
  })

  it('keeps the picker of a Raw MT project locked, with no toggle, and lists Pre-translation once', () => {
    renderTab({ steps: [pretranslateStep], workflowId: 99, workflowName: 'Pre-translation', sourceLang: 'en', targetLangs: ['de'], mtOnUpload: true, mtRouterName: 'Default MT router' }, library)
    expect(screen.queryByTestId('workflow-mode')).not.toBeInTheDocument()
    expect(screen.getByTestId('use-workflow-select')).toBeDisabled()
    // The built-in step is step 1 of the rail; a Pre-translation header row on
    // top of it would name it twice.
    expect(screen.queryByTestId('workflow-pretranslation')).not.toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-badge-0')).toHaveTextContent('Pre-translation')
  })

  it('heads a template workflow with the Pre-translation row while MT runs on upload', () => {
    renderTab({ mtOnUpload: true, mtRouterName: 'Default MT router' }, library)
    expect(screen.getByTestId('workflow-pretranslation')).toHaveTextContent('Default MT router')
    expect(screen.getByTestId('use-workflow-select')).toBeInTheDocument()
  })

  it('shows no Pre-translation row while MT on upload is off, or no router is picked', () => {
    renderTab({ mtOnUpload: false, mtRouterName: 'Default MT router' }, library)
    expect(screen.queryByTestId('workflow-pretranslation')).not.toBeInTheDocument()
    cleanup()
    renderTab({ mtOnUpload: true, mtRouterName: null }, library)
    expect(screen.queryByTestId('workflow-pretranslation')).not.toBeInTheDocument()
  })

  it('on a template, Raw MT and back each ask first, and Workflow returns to the workflow left', async () => {
    vi.useRealTimers()
    const onApplyWorkflow = vi.fn().mockResolvedValue(undefined)
    // Start on 8, not library[0]: falling back to the first library workflow
    // would otherwise look like the remembered choice.
    const { rerender } = renderTab({ workflowId: 8, workflowName: 'Auto APE', onApplyWorkflow }, library)

    fireEvent.click(screen.getByTestId('workflow-mode-raw_mt'))
    expect(onApplyWorkflow).not.toHaveBeenCalled()
    fireEvent.click(await screen.findByRole('button', { name: 'Use Raw MT' }))
    await act(async () => {})
    expect(onApplyWorkflow).toHaveBeenLastCalledWith(99)

    // The steps change under the card, and the pane under the toggle with them.
    rerender({ steps: [pretranslateStep], workflowId: 99, workflowName: 'Pre-translation', onApplyWorkflow })
    fireEvent.click(screen.getByTestId('workflow-mode-workflow'))
    fireEvent.click(await screen.findByRole('button', { name: 'Use workflow' }))
    await act(async () => {})
    expect(onApplyWorkflow).toHaveBeenLastCalledWith(8)
  })

  it('shows a Raw MT template the Pre-translation row instead of the dropdown', () => {
    renderTab({ steps: [pretranslateStep], workflowId: 99, workflowName: 'Pre-translation', mtRouterName: 'Default MT router' }, library)
    expect(screen.getByTestId('workflow-mode-raw_mt')).toHaveAttribute('aria-checked', 'true')
    expect(screen.queryByTestId('use-workflow-select')).not.toBeInTheDocument()
    expect(screen.getByTestId('raw-mt-router-link')).toHaveTextContent('Default MT router')
  })
})
