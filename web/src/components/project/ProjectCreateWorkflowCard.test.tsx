// The New Project wizard's Workflow card: the steps come from a library
// workflow or from the built-in Pre-translation one, and "New workflow"
// creates one from the steps on screen and uses it straight away.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement, type ReactNode } from 'react'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const createWorkflow = vi.fn().mockResolvedValue({ id: 42, name: 'My WF' })
const builtIn = {
  id: 77, name: 'Pre-translation', system_key: 'pretranslate_only',
  steps: [{ step_definition_id: 9, name: 'Pre-translation', step_type: 'pretranslate', step_order: 0 }],
}
vi.mock('../../api', () => ({
  createWorkflow: (...a: unknown[]) => createWorkflow(...a),
  getPretranslateWorkflow: () => Promise.resolve(builtIn),
}))

// Dialog stub: a button submits the name plus the steps it was seeded with,
// which is what the real form does after the user edits them.
vi.mock('../workflow/WorkflowFormDialog', () => ({
  WorkflowFormDialog: ({ open, onSubmit, initialSteps }: {
    open: boolean
    onSubmit: (input: { name: string; steps: unknown[] }) => Promise<unknown>
    initialSteps: unknown[]
  }) => open
    ? <button data-testid="submit-new-workflow" onClick={() => void onSubmit({ name: 'My WF', steps: initialSteps })}>create</button>
    : null,
}))
// Picker stub: the pinned "New template" action, the heading, and the rows by name.
vi.mock('../workflow/WorkflowPicker', () => ({
  WorkflowPicker: ({ actions, heading, workflows, children }: {
    actions: { key: string; onSelect: () => void }[]
    heading?: ReactNode
    workflows: { id: number; name: string }[]
    children: ReactNode
  }) => (
    <div data-testid="workflow-picker">
      <button data-testid="create-workflow" onClick={() => actions[0].onSelect()}>new workflow</button>
      {heading}
      <ul data-testid="picker-rows">{workflows.map(w => <li key={w.id}>{w.name}</li>)}</ul>
      {children}
    </div>
  ),
}))
vi.mock('../workflow/RawMTPane', () => ({
  RawMTPane: ({ heading, mtRouterName }: { heading: ReactNode; mtRouterName?: string }) => (
    <div data-testid="raw-mt-pane">{heading}<span data-testid="raw-mt-router">{mtRouterName ?? 'none'}</span></div>
  ),
}))
vi.mock('../workflow/WorkflowStepChips', () => ({ WorkflowStepChips: () => null }))

import { ProjectCreateWorkflowCard } from './ProjectCreateWorkflowCard'
import type { WorkflowListItem } from '../../api'

const steps = [{ step_definition_id: 5, name: 'Proofread', step_type: 'human' as const }]
const preSteps = [{ step_definition_id: 9, name: 'Pre-translation', step_type: 'pretranslate' as const }]
const library = [
  { id: 3, name: 'Default', steps: [{ step_definition_id: 1, name: 'Translation', step_type: 'human' }] },
  { id: 4, name: 'Review only', steps: [{ step_definition_id: 2, name: 'Review', step_type: 'human' }] },
  { id: 6, name: 'Auto APE', steps: [{ step_definition_id: 7, name: 'Auto APE', step_type: 'ai' }] },
] as unknown as WorkflowListItem[]

type CardProps = Partial<Parameters<typeof ProjectCreateWorkflowCard>[0]>

function renderCard(onPickWorkflow = vi.fn().mockResolvedValue(undefined), props: CardProps = {}) {
  const qc = new QueryClient()
  const card = (p: CardProps) => createElement(
    QueryClientProvider,
    { client: qc },
    createElement(ProjectCreateWorkflowCard, {
      workflows: [],
      workflowId: '',
      steps,
      onPickWorkflow,
      targetLanguages: ['de'],
      onStepsChange: vi.fn(),
      ...p,
    }),
  )
  const { rerender } = render(card(props))
  return { onPickWorkflow, rerender: (p: CardProps) => rerender(card(p)) }
}

/** A mode option, once the built-in workflow has loaded and enabled the toggle. */
async function modeOption(mode: 'workflow' | 'raw_mt') {
  const option = screen.getByTestId(`workflow-mode-${mode}`)
  await waitFor(() => expect(option).toBeEnabled())
  return option
}

const rowNames = () => Array.from(screen.getByTestId('picker-rows').querySelectorAll('li')).map(li => li.textContent)

describe('ProjectCreateWorkflowCard — New workflow', () => {
  beforeEach(() => vi.clearAllMocks())

  it('creates the workflow from the steps on screen and uses it for the project', async () => {
    const { onPickWorkflow } = renderCard()
    fireEvent.click(screen.getByTestId('create-workflow'))
    fireEvent.click(screen.getByTestId('submit-new-workflow'))

    await waitFor(() => expect(createWorkflow).toHaveBeenCalled())
    expect(createWorkflow).toHaveBeenCalledWith({
      name: 'My WF',
      steps: [{ step_definition_id: 5, default_assignee: null }],
    })
    await waitFor(() => expect(onPickWorkflow).toHaveBeenCalledWith('42'))
  })
})

describe('ProjectCreateWorkflowCard — mode toggle', () => {
  beforeEach(() => vi.clearAllMocks())

  it('reads Workflow for library steps, with the whole library in the dropdown', () => {
    renderCard(undefined, { workflows: library, workflowId: '3' })
    expect(screen.getByTestId('workflow-mode-workflow')).toHaveAttribute('aria-checked', 'true')
    expect(rowNames()).toEqual(['Default', 'Review only', 'Auto APE'])
    expect(screen.queryByTestId('raw-mt-pane')).not.toBeInTheDocument()
  })

  it('choosing Raw MT picks the built-in workflow, handing over its detail', async () => {
    const { onPickWorkflow } = renderCard(undefined, { workflows: library, workflowId: '3' })
    fireEvent.click(await modeOption('raw_mt'))
    await waitFor(() => expect(onPickWorkflow).toHaveBeenCalledWith('77', builtIn))
  })

  it('reads Raw MT for the Pre-translation step: no dropdown, the router on the pane', () => {
    renderCard(undefined, { workflows: library, workflowId: '77', steps: preSteps, mtRouterName: 'Default MT router' })
    expect(screen.getByTestId('workflow-mode-raw_mt')).toHaveAttribute('aria-checked', 'true')
    expect(screen.queryByTestId('workflow-picker')).not.toBeInTheDocument()
    expect(screen.getByTestId('raw-mt-router')).toHaveTextContent('Default MT router')
  })

  it('choosing Workflow goes back to the workflow left for Raw MT', async () => {
    const { onPickWorkflow, rerender } = renderCard(undefined, { workflows: library, workflowId: '4' })
    fireEvent.click(await modeOption('raw_mt'))
    await waitFor(() => expect(onPickWorkflow).toHaveBeenCalledWith('77', builtIn))
    rerender({ workflows: library, workflowId: '77', steps: preSteps })
    fireEvent.click(await modeOption('workflow'))
    await waitFor(() => expect(onPickWorkflow).toHaveBeenLastCalledWith('4', undefined))
  })

  it('with no earlier choice, Workflow goes to the first library workflow', async () => {
    const { onPickWorkflow } = renderCard(undefined, { workflows: library, workflowId: '77', steps: preSteps })
    fireEvent.click(await modeOption('workflow'))
    await waitFor(() => expect(onPickWorkflow).toHaveBeenCalledWith('3', undefined))
  })

  it('offers no way back with an empty library', async () => {
    renderCard(undefined, { workflows: [], workflowId: '77', steps: preSteps })
    await waitFor(() => expect(screen.getByTestId('workflow-mode-raw_mt')).toBeEnabled())
    expect(screen.getByTestId('workflow-mode-workflow')).toBeDisabled()
  })
})
