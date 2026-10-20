import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DisclosureToggle } from './disclosure-toggle'

describe('DisclosureToggle', () => {
  it('exposes the state and the controlled body, and toggles on click', () => {
    const onToggle = vi.fn()
    render(<DisclosureToggle open={false} onToggle={onToggle} controls="body-1" summary="2 items">Show details</DisclosureToggle>)
    const btn = screen.getByRole('button', { name: /show details/i })
    expect(btn).toHaveTextContent('2 items')
    expect(btn).toHaveAttribute('aria-expanded', 'false')
    expect(btn).toHaveAttribute('aria-controls', 'body-1')
    fireEvent.click(btn)
    expect(onToggle).toHaveBeenCalledTimes(1)
  })

  it('reports open state', () => {
    render(<DisclosureToggle open onToggle={() => {}}>Hide</DisclosureToggle>)
    expect(screen.getByRole('button')).toHaveAttribute('aria-expanded', 'true')
  })
})
