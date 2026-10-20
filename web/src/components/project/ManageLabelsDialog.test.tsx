import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ManageLabelsDialog } from './ManageLabelsDialog'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listLabels = vi.fn()
const createLabel = vi.fn()
const deleteLabelValue = vi.fn()
vi.mock('../../api', () => ({
  listLabels: (...a: unknown[]) => listLabels(...a),
  createLabel: (...a: unknown[]) => createLabel(...a),
  deleteLabel: vi.fn(),
  createLabelValue: vi.fn(),
  deleteLabelValue: (...a: unknown[]) => deleteLabelValue(...a),
}))

function renderDialog(props: Partial<React.ComponentProps<typeof ManageLabelsDialog>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ManageLabelsDialog open onClose={vi.fn()} {...props} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  listLabels.mockResolvedValue([
    { id: 1, name: 'Country', in_use: false, values: [{ id: 10, value: 'Germany', in_use: false, project_count: 0 }, { id: 11, value: 'Japan', in_use: false, project_count: 0 }] },
  ])
  createLabel.mockResolvedValue({ id: 2, name: 'Industry', values: [] })
})

describe('ManageLabelsDialog', () => {
  it('stays closed when open is false', () => {
    renderDialog({ open: false })
    expect(screen.queryByTestId('manage-labels-dialog')).not.toBeInTheDocument()
  })

  it('lists each label with its values', async () => {
    renderDialog()
    expect(await screen.findByTestId('label-row-1')).toBeInTheDocument()
    // The name is read-only text: a label's name is fixed once it exists, so
    // there is no pencil even for someone who may edit everything else here.
    expect(screen.getByTestId('label-row-1')).toHaveTextContent('Country')
    expect(screen.queryByTestId('label-rename-1')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /rename/i })).not.toBeInTheDocument()
    // Both values show as chips in the field: everything a label offers is
    // "selected", and clearing one is what retires it.
    expect(screen.getByTestId('label-values-1')).toHaveTextContent('Germany')
    expect(screen.getByTestId('label-values-1')).toHaveTextContent('Japan')
  })

  it('adds a label through the name field', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(await screen.findByTestId('add-label-button'))
    await user.type(screen.getByTestId('new-label-name'), 'Industry')
    await user.click(screen.getByTestId('new-label-submit'))
    await waitFor(() => expect(createLabel).toHaveBeenCalledWith('Industry'))
  })

  it('refuses a blank label name without calling the API', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(await screen.findByTestId('add-label-button'))
    await user.type(screen.getByTestId('new-label-name'), '   ')
    await user.click(screen.getByTestId('new-label-submit'))
    expect(createLabel).not.toHaveBeenCalled()
  })

  it('offers the delete bin only to an owner', async () => {
    // Deleting a whole label is owner-only server-side. A manager who can edit
    // everything else must not be shown a bin the API would refuse.
    const { unmount } = renderDialog({ canEdit: true, canDelete: false })
    expect(await screen.findByTestId('label-row-1')).toBeInTheDocument()
    expect(screen.queryByTestId('label-delete-1')).not.toBeInTheDocument()
    unmount()

    renderDialog({ canEdit: true, canDelete: true })
    expect(await screen.findByTestId('label-delete-1')).toBeInTheDocument()
  })

  // Asserted on the option rows in the open picker, not on the trigger's chips:
  // jsdom reports every width as 0, so the chips collapse into "+N" and a test
  // looking for their × passes whether or not the × would exist.
  it('lets a manager add values but not retire them', async () => {
    const user = userEvent.setup()
    renderDialog({ canEdit: true, canDelete: false })
    await user.click(await screen.findByTestId('label-values-1'))
    expect(screen.getByTestId('label-values-1-option-10')).toHaveAttribute('aria-disabled', 'true')
    // Adding is still a manager's to do.
    await user.type(screen.getByPlaceholderText('Type to add a new option'), 'Spain')
    expect(screen.getByTestId('label-values-1-action-create-option')).toBeInTheDocument()
  })

  it('deletes a value nothing uses without asking', async () => {
    const user = userEvent.setup()
    deleteLabelValue.mockResolvedValue(undefined)
    renderDialog({ canEdit: true, canDelete: true })
    await user.click(await screen.findByTestId('label-values-1'))
    await user.click(screen.getByTestId('label-values-1-option-10'))
    await waitFor(() => expect(deleteLabelValue).toHaveBeenCalledWith(1, 10))
    expect(screen.queryByText('Delete value?')).not.toBeInTheDocument()
  })

  it('asks before deleting a value projects use, and deletes only on confirm', async () => {
    const user = userEvent.setup()
    deleteLabelValue.mockResolvedValue(undefined)
    listLabels.mockResolvedValue([
      { id: 1, name: 'Country', in_use: true, values: [
        { id: 10, value: 'Germany', in_use: true, project_count: 2 },
        { id: 11, value: 'Japan', in_use: false, project_count: 0 },
      ] },
    ])
    renderDialog({ canEdit: true, canDelete: true })
    await user.click(await screen.findByTestId('label-values-1'))
    // In use no longer locks the value for an admin.
    expect(screen.getByTestId('label-values-1-option-10')).not.toHaveAttribute('aria-disabled', 'true')
    await user.click(screen.getByTestId('label-values-1-option-10'))

    expect(await screen.findByText('Germany is set on 2 projects. Deleting it removes it from them.')).toBeInTheDocument()
    expect(deleteLabelValue).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deleteLabelValue).toHaveBeenCalledWith(1, 10))
    expect(deleteLabelValue).toHaveBeenCalledTimes(1)
  })

  it('keeps the value when the confirm is cancelled', async () => {
    const user = userEvent.setup()
    listLabels.mockResolvedValue([
      { id: 1, name: 'Country', in_use: true, values: [{ id: 10, value: 'Germany', in_use: true, project_count: 1 }] },
    ])
    renderDialog({ canEdit: true, canDelete: true })
    await user.click(await screen.findByTestId('label-values-1'))
    await user.click(screen.getByTestId('label-values-1-option-10'))
    expect(await screen.findByText('Germany is set on 1 project. Deleting it removes it from them.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByText('Delete value?')).not.toBeInTheDocument())
    expect(deleteLabelValue).not.toHaveBeenCalled()
  })

  it('disables the bin while a value is in use', async () => {
    // The server refuses it, so the bin is disabled rather than an action that
    // fails; the tooltip says which values hold it (label-delete.test.ts).
    listLabels.mockResolvedValue([
      { id: 1, name: 'Country', in_use: true, values: [{ id: 10, value: 'Germany', in_use: true, project_count: 1 }] },
    ])
    renderDialog({ canDelete: true })
    expect(await screen.findByTestId('label-delete-1')).toBeDisabled()
  })

  it('enables the bin once no remaining value is in use', async () => {
    renderDialog({ canDelete: true })
    expect(await screen.findByTestId('label-delete-1')).toBeEnabled()
  })

  it('hides every editing control when canEdit is false', async () => {
    renderDialog({ canEdit: false })
    expect(await screen.findByTestId('label-row-1')).toBeInTheDocument()
    expect(screen.queryByTestId('add-label-button')).not.toBeInTheDocument()
    expect(screen.queryByTestId('label-delete-1')).not.toBeInTheDocument()
  })

  it('closes on the Close button', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    renderDialog({ onClose })
    await user.click(await screen.findByTestId('manage-labels-close'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
