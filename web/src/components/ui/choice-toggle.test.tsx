import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ChoiceToggle } from './choice-toggle'

const options = [
  { value: 'scratch', label: 'From scratch' },
  { value: 'template', label: 'From template' },
] as const
type Mode = (typeof options)[number]['value']

describe('ChoiceToggle', () => {
  it('renders a radiogroup with nothing checked when value is null', () => {
    render(<ChoiceToggle<Mode> aria-label="Start from" value={null} onChange={() => {}} options={[...options]} data-testid="start" />)
    expect(screen.getByRole('radiogroup', { name: 'Start from' })).toBeInTheDocument()
    const radios = screen.getAllByRole('radio')
    expect(radios).toHaveLength(2)
    for (const r of radios) expect(r).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByTestId('start-scratch')).toHaveTextContent('From scratch')
  })

  it('checks exactly the current value', () => {
    render(<ChoiceToggle<Mode> aria-label="Start from" value="template" onChange={() => {}} options={[...options]} />)
    expect(screen.getByRole('radio', { name: 'From template' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: 'From scratch' })).toHaveAttribute('aria-checked', 'false')
  })

  it('reports the clicked option, including re-clicking the checked one', async () => {
    const onChange = vi.fn()
    render(<ChoiceToggle<Mode> aria-label="Start from" value="template" onChange={onChange} options={[...options]} />)
    await userEvent.click(screen.getByRole('radio', { name: 'From scratch' }))
    await userEvent.click(screen.getByRole('radio', { name: 'From template' }))
    expect(onChange.mock.calls).toEqual([['scratch'], ['template']])
  })

  it('does not fire for a disabled option', async () => {
    const onChange = vi.fn()
    render(
      <ChoiceToggle<Mode>
        aria-label="Start from"
        value={null}
        onChange={onChange}
        options={[options[0], { ...options[1], disabled: true }]}
      />,
    )
    await userEvent.click(screen.getByRole('radio', { name: 'From template' }))
    expect(onChange).not.toHaveBeenCalled()
  })
})

describe('ChoiceToggle caption', () => {
  it('renders each option\'s caption inside its radio, so it is part of the name', () => {
    render(
      <ChoiceToggle
        aria-label="Workflow mode"
        value="a"
        onChange={() => {}}
        options={[{ value: 'a', label: 'Workflow', caption: 'Human and AI steps' }, { value: 'b', label: 'Raw MT', caption: 'Pre-translation' }]}
      />,
    )
    expect(screen.getByRole('radio', { name: 'Workflow Human and AI steps' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: 'Raw MT Pre-translation' })).toHaveAttribute('aria-checked', 'false')
  })
})
