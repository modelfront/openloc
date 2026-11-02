import { describe, it, expect, vi } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { CreateButton } from './create-button'

describe('CreateButton', () => {
  it('renders the label after a leading plus icon, as an outline button without the primary colour', () => {
    const { getByRole } = render(<CreateButton>New project</CreateButton>)
    const btn = getByRole('button', { name: 'New project' })
    expect(btn.querySelector('[data-icon="inline-start"] svg.lucide-plus')).toBeInTheDocument()
    expect(btn.className).toMatch(/(^|\s)border-border(\s|$)/)
    expect(btn.className).not.toMatch(/(^|\s)(text|border)-primary(\S*)?(\s|$)/)
    expect(btn.className).not.toMatch(/(^|\s)bg-primary(\s|$)/)
  })

  it('forwards clicks', () => {
    const onClick = vi.fn()
    const { getByRole } = render(<CreateButton onClick={onClick}>New TM</CreateButton>)
    fireEvent.click(getByRole('button'))
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('stays disabled when disabled is set', () => {
    const onClick = vi.fn()
    const { getByRole } = render(<CreateButton disabled onClick={onClick}>New step</CreateButton>)
    fireEvent.click(getByRole('button'))
    expect(getByRole('button')).toBeDisabled()
    expect(onClick).not.toHaveBeenCalled()
  })
})
