// WorkflowEditDialog: the name is validated inline (zod + FormField) and the
// save goes through useMutationWithToast — success closes the dialog and
// invalidates the workflow + step-definition lists.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkflowEditDialog } from './WorkflowEditDialog'
import type { WorkflowDetail, WorkflowDefinitionStepRow } from '../../api'

const step: WorkflowDefinitionStepRow = {
  id: 1, workflow_id: 7, step_order: 1, default_assignee: null, step_definition_id: 3,
  name: 'Translation', step_type: 'human', role_required: 'translator', ai_router_id: null,
  definition_deleted: false, assignee_name: null,
}
const workflow: WorkflowDetail = {
  id: 7, tenant_id: 1, name: 'Standard', created_at: '', updated_at: '', deleted_at: '', steps: [step],
}

const getWorkflow = vi.fn()
const updateWorkflow = vi.fn()
vi.mock('../../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api')>()
  return {
    ...actual,
    getWorkflow: (...args: unknown[]) => getWorkflow(...args),
    updateWorkflow: (...args: unknown[]) => updateWorkflow(...args),
  }
})
// The steps editor is its own component; here the name field is what's under test.
vi.mock('../WorkflowStepsEditor', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../WorkflowStepsEditor')>()
  return { ...actual, WorkflowStepsEditor: () => <div data-testid="steps-editor-stub" /> }
})

beforeEach(() => {
  getWorkflow.mockReset().mockResolvedValue(workflow)
  updateWorkflow.mockReset().mockResolvedValue(workflow)
})
afterEach(cleanup)

function setup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const invalidate = vi.spyOn(qc, 'invalidateQueries')
  const onClose = vi.fn()
  render(
    <QueryClientProvider client={qc}>
      <WorkflowEditDialog workflowId={7} onClose={onClose} />
    </QueryClientProvider>,
  )
  return { onClose, invalidate }
}

/**
 * The name input renders as soon as the query resolves, but its value arrives
 * one tick later from `reset({ name })` in a `useEffect`. `findByTestId`
 * therefore can return the element while it still holds the `''` default, so
 * every test waits for the SEEDED value rather than just for existence.
 *
 * Do not simplify this back to a bare `findByTestId` + one-shot assertion:
 * testing-library usually flushes the effect first, so the empty-value window
 * only opens under load, where it is intermittent rather than reproducible.
 */
async function seededNameInput(): Promise<HTMLElement> {
  const name = await screen.findByTestId('workflow-edit-name')
  await waitFor(() => expect(name).toHaveValue('Standard'))
  return name
}

describe('WorkflowEditDialog', () => {
  it('seeds the name from the workflow and refuses an empty name inline', async () => {
    const user = userEvent.setup()
    const { onClose } = setup()
    const name = await seededNameInput()
    await user.clear(name)
    await user.click(screen.getByTestId('workflow-edit-save'))
    expect(await screen.findByText('Workflow name is required')).toBeInTheDocument()
    expect(updateWorkflow).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('saves the trimmed name with the steps, invalidates lists and closes', async () => {
    const user = userEvent.setup()
    const { onClose, invalidate } = setup()
    const name = await seededNameInput()
    await user.clear(name)
    await user.type(name, '  Renamed  ')
    await user.click(screen.getByTestId('workflow-edit-save'))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(updateWorkflow).toHaveBeenCalledWith(7, {
      name: 'Renamed',
      steps: [{ step_definition_id: 3, default_assignee: null }],
    })
    const keys = invalidate.mock.calls.map(c => c[0]?.queryKey)
    expect(keys).toContainEqual(['workflows'])
    expect(keys).toContainEqual(['step-definitions'])
  })

  it('stays open when the save is rejected', async () => {
    updateWorkflow.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    const { onClose } = setup()
    await seededNameInput()
    await user.click(screen.getByTestId('workflow-edit-save'))
    await waitFor(() => expect(updateWorkflow).toHaveBeenCalled())
    await waitFor(() => expect(screen.getByTestId('workflow-edit-save')).toBeEnabled())
    expect(onClose).not.toHaveBeenCalled()
  })
})
