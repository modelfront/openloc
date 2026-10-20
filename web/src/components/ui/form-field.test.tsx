import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { FormField } from './form-field'

const label = (text: string) => screen.getByText(text, { selector: 'label' })

describe('FormField', () => {
  it('puts an action at the right end of the label row', () => {
    render(<FormField label="Columns" action={<a href="#all">Select all</a>}><input /></FormField>)
    const row = label('Columns').parentElement!
    expect(row).toHaveTextContent('ColumnsSelect all')
    expect(screen.getByRole('link', { name: 'Select all' })).toBeInTheDocument()
  })

  it('renders the label alone without an action', () => {
    const { container } = render(<FormField label="Name"><input /></FormField>)
    expect(container.firstElementChild!.firstElementChild!.tagName).toBe('LABEL')
  })

  it('marks a required field with an asterisk', () => {
    render(<FormField label="Project name" required><input /></FormField>)
    expect(label('Project name').textContent).toBe('Project name*')
  })

  it('shows the error text and tints the control border', () => {
    const { container } = render(
      <FormField label="Email" error="Enter a valid email address"><input /></FormField>,
    )
    expect(screen.getByText('Enter a valid email address')).toBeInTheDocument()
    expect(container.querySelector('[class*="border-red-400"]')).not.toBeNull()
  })

  it('renders no error paragraph when there is no error', () => {
    render(<FormField label="Email"><input /></FormField>)
    expect(screen.queryByText(/Enter a valid/)).not.toBeInTheDocument()
  })

  // The dense spell-fix panel stacks four of these in a popover; the caps
  // micro-label keeps them from out-shouting the text they describe.
  it('renders the caps micro-label smaller and upper-case', () => {
    render(<FormField label="Not in dictionary" labelVariant="caps"><p>x</p></FormField>)
    const cls = label('Not in dictionary').className
    expect(cls).toContain('uppercase')
    expect(cls).toContain('text-[11px]')
  })

  it('defaults to the normal label, so existing forms are untouched', () => {
    render(<FormField label="Project name"><input /></FormField>)
    const cls = label('Project name').className
    expect(cls).toContain('text-xs')
    expect(cls).not.toContain('uppercase')
  })
})
