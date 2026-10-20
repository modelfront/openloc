// WorkflowNameDialog: the name is validated inline (zod + FormField), the
// caller owns the request, and a rejected request keeps the dialog open.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { WorkflowNameDialog } from './WorkflowNameDialog'

afterEach(cleanup)

function setup(onSubmit = vi.fn().mockResolvedValue(undefined)) {
  const onClose = vi.fn()
  render(<WorkflowNameDialog open onClose={onClose} title="Save as workflow" submitLabel="Save" onSubmit={onSubmit} />)
  return { onSubmit, onClose }
}

describe('WorkflowNameDialog', () => {
  it('refuses an empty or whitespace name inline and never calls onSubmit', async () => {
    const user = userEvent.setup()
    const { onSubmit, onClose } = setup()
    expect(screen.getByTestId('save-as-workflow-submit')).toBeEnabled()
    await user.click(screen.getByTestId('save-as-workflow-submit'))
    expect(await screen.findByText('Workflow name is required')).toBeInTheDocument()
    await user.type(screen.getByTestId('save-as-workflow-name'), '   ')
    await user.click(screen.getByTestId('save-as-workflow-submit'))
    expect(screen.getByText('Workflow name is required')).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('submits the trimmed name and closes on success', async () => {
    const user = userEvent.setup()
    const { onSubmit, onClose } = setup()
    await user.type(screen.getByTestId('save-as-workflow-name'), '  Standard review  ')
    await user.click(screen.getByTestId('save-as-workflow-submit'))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(onSubmit).toHaveBeenCalledWith('Standard review')
  })

  it('stays open when the caller rejects', async () => {
    const user = userEvent.setup()
    const { onSubmit, onClose } = setup(vi.fn().mockRejectedValue(new Error('name taken')))
    await user.type(screen.getByTestId('save-as-workflow-name'), 'Review')
    await user.click(screen.getByTestId('save-as-workflow-submit'))
    await waitFor(() => expect(onSubmit).toHaveBeenCalled())
    expect(onClose).not.toHaveBeenCalled()
  })
})
