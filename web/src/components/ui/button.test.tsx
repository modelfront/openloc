import { describe, it, expect, vi } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { Button } from './button'
import { DeleteButton } from './delete-button'

describe('Button loading state', () => {
  it('shows a spinner, disables the button, and sets aria-busy when loading', () => {
    const { getByRole } = render(<Button loading>Save</Button>)
    const btn = getByRole('button')
    expect(btn).toBeDisabled()
    expect(btn).toHaveAttribute('aria-busy', 'true')
    expect(btn.querySelector('svg.animate-spin')).toBeInTheDocument()
    // label stays visible next to the spinner
    expect(btn).toHaveTextContent('Save')
  })

  it('renders no spinner and is enabled when not loading', () => {
    const { getByRole } = render(<Button>Save</Button>)
    const btn = getByRole('button')
    expect(btn).not.toBeDisabled()
    expect(btn).not.toHaveAttribute('aria-busy')
    expect(btn.querySelector('svg.animate-spin')).not.toBeInTheDocument()
  })

  it('stays disabled when either loading or disabled is set', () => {
    const { getByRole } = render(<Button disabled>Save</Button>)
    expect(getByRole('button')).toBeDisabled()
  })
})

describe('Button color / hoverColor', () => {
  it('applies the resting and hover text-color tokens', () => {
    const { getByRole } = render(<Button color="primary" hoverColor="red">x</Button>)
    const btn = getByRole('button')
    expect(btn.className).toContain('text-primary')
    expect(btn.className).toContain('hover:text-red-600')
  })

  it('adds no color class for the default token', () => {
    const { getByRole } = render(<Button color="default">x</Button>)
    const btn = getByRole('button')
    expect(btn.className).not.toContain('text-amber-700')
    expect(btn.className).not.toContain('text-red-600')
  })
})

describe('Button hover background', () => {
  // A `<button>` is the common case, so its hover must not hang off a variant
  // that only matches anchors (`[a]:hover:…`).
  it('gives the primary variant a hover background on a real button', () => {
    const { getByRole } = render(<Button>Create project</Button>)
    // Anchored on both sides: `[a]:hover:bg-primary/80` would satisfy a plain
    // substring check while never firing on a button.
    expect(getByRole('button').className).toMatch(/(^|\s)hover:bg-primary\/80(\s|$)/)
  })

  it.each(['outline', 'ghost', 'destructive'] as const)(
    'gives the %s variant a hover treatment too',
    variant => {
      const { getByRole } = render(<Button variant={variant}>x</Button>)
      expect(getByRole('button').className).toMatch(/(^|\s)hover:/)
    },
  )
})

describe('Button start/end icons', () => {
  it('renders a start icon marked data-icon=inline-start before the label', () => {
    const { getByRole } = render(
      <Button startIcon={<svg data-testid="start" />}>New</Button>,
    )
    const btn = getByRole('button')
    expect(btn.querySelector('[data-icon="inline-start"]')).toBeInTheDocument()
    expect(btn.querySelector('[data-testid="start"]')).toBeInTheDocument()
    expect(btn).toHaveTextContent('New')
  })

  it('renders an end icon marked data-icon=inline-end', () => {
    const { getByRole } = render(
      <Button endIcon={<svg data-testid="end" />}>Menu</Button>,
    )
    const btn = getByRole('button')
    expect(btn.querySelector('[data-icon="inline-end"]')).toBeInTheDocument()
    expect(btn.querySelector('[data-testid="end"]')).toBeInTheDocument()
  })

  it('replaces the start icon with the spinner while loading', () => {
    const { getByRole } = render(
      <Button loading startIcon={<svg data-testid="start" />}>Save</Button>,
    )
    const btn = getByRole('button')
    expect(btn.querySelector('[data-testid="start"]')).not.toBeInTheDocument()
    expect(btn.querySelector('svg.animate-spin')).toBeInTheDocument()
  })
})

describe('Button icon size', () => {
  it('replaces the icon with the spinner while loading (no room for both)', () => {
    const { getByRole } = render(
      <Button size="icon" loading aria-label="Copy">
        <svg data-testid="icon" />
      </Button>,
    )
    const btn = getByRole('button')
    expect(btn).toBeDisabled()
    expect(btn).toHaveAttribute('aria-busy', 'true')
    expect(btn.querySelector('svg.animate-spin')).toBeInTheDocument()
    // the icon child is hidden while loading — the square only fits one glyph
    expect(btn.querySelector('[data-testid="icon"]')).not.toBeInTheDocument()
  })

  it('renders the icon child and no spinner when not loading', () => {
    const { getByRole } = render(
      <Button size="icon" aria-label="Copy">
        <svg data-testid="icon" />
      </Button>,
    )
    const btn = getByRole('button')
    expect(btn.querySelector('[data-testid="icon"]')).toBeInTheDocument()
    expect(btn.querySelector('svg.animate-spin')).not.toBeInTheDocument()
  })

  it('a text-size button keeps its label beside the spinner while loading', () => {
    const { getByRole } = render(<Button loading>Save</Button>)
    const btn = getByRole('button')
    expect(btn.querySelector('svg.animate-spin')).toBeInTheDocument()
    expect(btn).toHaveTextContent('Save')
  })
})

describe('DeleteButton', () => {
  it('renders a trash icon with a Delete label and gray→red hover classes', () => {
    const { getByRole } = render(<DeleteButton onClick={() => {}} />)
    const btn = getByRole('button')
    expect(btn).toHaveAttribute('aria-label', 'Delete')
    expect(btn.querySelector('svg')).toBeInTheDocument()
    expect(btn.className).toContain('text-gray-400')
    expect(btn.className).toContain('hover:text-red-600')
  })

  it('forwards a custom aria-label and onClick', () => {
    const onClick = vi.fn()
    const { getByRole } = render(<DeleteButton aria-label="Delete project" onClick={onClick} />)
    const btn = getByRole('button', { name: 'Delete project' })
    expect(btn).toBeEnabled()
    fireEvent.click(btn)
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('forwards disabled', () => {
    const { getByRole } = render(<DeleteButton disabled onClick={() => {}} />)
    expect(getByRole('button')).toBeDisabled()
  })
})
