import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SaveAsTemplateDialog } from './SaveAsTemplateDialog'

describe('SaveAsTemplateDialog', () => {
  it('starts from the given name and saves the trimmed value', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined)
    render(<SaveAsTemplateDialog open onOpenChange={vi.fn()} defaultName="Nordwind summer" onSave={onSave} />)
    const name = screen.getByTestId('save-as-template-name')
    expect(name).toHaveValue('Nordwind summer')
    await userEvent.clear(name)
    await userEvent.type(name, '  Summer template ')
    await userEvent.click(screen.getByTestId('save-as-template-confirm'))
    await waitFor(() => expect(onSave).toHaveBeenCalledWith('Summer template'))
  })

  it('refuses an empty name without calling onSave', async () => {
    const onSave = vi.fn()
    render(<SaveAsTemplateDialog open onOpenChange={vi.fn()} defaultName="" onSave={onSave} />)
    await userEvent.click(screen.getByTestId('save-as-template-confirm'))
    expect(await screen.findByText('Template name is required')).toBeInTheDocument()
    expect(onSave).not.toHaveBeenCalled()
  })

  it('Cancel closes through onOpenChange', async () => {
    const onOpenChange = vi.fn()
    render(<SaveAsTemplateDialog open onOpenChange={onOpenChange} defaultName="x" onSave={vi.fn()} />)
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
