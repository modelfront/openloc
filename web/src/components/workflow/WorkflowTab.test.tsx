// The Workflow tab: the mode toggle a template gets, the Pre-translation row,
// and the workflow picker shut while a project's steps are being edited (the
// editing itself: ProjectWorkflowSteps.test.tsx).

import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { ComponentProps } from 'react'
import { WorkflowTab } from './WorkflowTab'
import type { WorkflowStep } from '../WorkflowStepsEditor'

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

describe('WorkflowTab mode toggle', () => {
  const library = [
    { id: 7, name: 'Standard', steps: [{ step_definition_id: 1, name: 'Translation', step_type: 'human' }] },
    { id: 8, name: 'Auto APE', steps: [{ step_definition_id: 2, name: 'Auto APE', step_type: 'ai' }] },
  ]

  it('is not offered on a project', () => {
    renderTab({ sourceLang: 'en', targetLangs: ['de', 'fr'], onSaveSteps: vi.fn(), onPreviewSteps: vi.fn(), onRefresh: vi.fn() })
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

  it("ends the picker's line with an icon that opens the chosen workflow in the library, in a new tab", () => {
    renderTab({}, library)
    const open = screen.getByTestId('workflow-open')
    expect(open).toHaveAttribute('href', '/workflows?edit=7')
    expect(open).toHaveAttribute('target', '_blank')
    expect(open).toHaveAccessibleName('Open workflow (opens in a new tab)')
  })

  it('offers no open icon for a built-in workflow the library does not hold', () => {
    renderTab({ steps: [pretranslateStep], workflowId: 99, workflowName: 'Pre-translation', sourceLang: 'en', targetLangs: ['de'], mtOnUpload: true, mtRouterName: 'Default MT router' }, library)
    expect(screen.queryByTestId('workflow-open')).not.toBeInTheDocument()
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

describe('WorkflowTab while project steps are edited', () => {
  it('shuts the workflow picker and says why, so an applied workflow cannot hide under the draft', () => {
    renderTab({ sourceLang: 'en', targetLangs: ['de'], onSaveSteps: vi.fn(), onPreviewSteps: vi.fn(), onRefresh: vi.fn() })
    expect(screen.getByTestId('use-workflow-select')).toBeEnabled()
    act(() => { fireEvent.click(screen.getByTestId('workflow-steps-edit')) })
    expect(screen.getByTestId('use-workflow-select')).toBeDisabled()
    expect(screen.getByTestId('workflow-picker-disabled-reason')).toHaveTextContent('Save or discard your changes')
    act(() => { fireEvent.click(screen.getByTestId('workflow-steps-discard')) })
    expect(screen.getByTestId('use-workflow-select')).toBeEnabled()
  })
})
