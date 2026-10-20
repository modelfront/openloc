import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CheckboxField } from './checkbox-field'

describe('CheckboxField', () => {
  it('renders label + description and toggles via the whole row', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    render(<CheckboxField checked={false} onChange={onChange} label="Task assignment" description="When assigned" />)

    expect(screen.getByText('Task assignment')).toBeInTheDocument()
    expect(screen.getByText('When assigned')).toBeInTheDocument()

    // Clicking the label text (not just the box) toggles — the label wraps the input.
    await user.click(screen.getByText('Task assignment'))
    expect(onChange).toHaveBeenCalledWith(true)
  })

  it('does not fire when disabled', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    render(<CheckboxField checked disabled onChange={onChange} label="Disabled" />)
    await user.click(screen.getByText('Disabled'))
    expect(onChange).not.toHaveBeenCalled()
  })

  it('compact: one clickable label beside the box, no description', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    render(<CheckboxField size="compact" checked={false} onChange={onChange} label="Aa" description="Not shown" />)
    expect(screen.queryByText('Not shown')).not.toBeInTheDocument()
    await user.click(screen.getByText('Aa'))
    expect(onChange).toHaveBeenCalledWith(true)
    expect(screen.getByRole('checkbox', { name: 'Aa' })).toBeInTheDocument()
  })
})
