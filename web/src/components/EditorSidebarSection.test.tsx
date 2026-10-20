import { describe, it, expect, vi, afterEach } from 'vitest'
import { useState, type ReactNode } from 'react'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { EditorSidebarSection } from './EditorSidebarSection'

afterEach(cleanup)

function Harness({ meta, action }: { meta?: ReactNode; action?: ReactNode }) {
  const [open, setOpen] = useState(true)
  return (
    <EditorSidebarSection data-testid="demo-section" title="Demo" open={open} onToggle={() => setOpen(v => !v)} meta={meta} action={action}>
      <p>Body text</p>
    </EditorSidebarSection>
  )
}

describe('EditorSidebarSection', () => {
  it('points the toggle at the body and hides the body when collapsed', async () => {
    render(<Harness />)
    const toggle = screen.getByRole('button', { name: /Demo/ })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const body = document.getElementById(toggle.getAttribute('aria-controls')!)
    expect(body).toHaveTextContent('Body text')

    await userEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('Body text')).toBeNull()
    // The section itself stays, so its testid marks it open or closed.
    expect(screen.getByTestId('demo-section')).toBeInTheDocument()
  })

  it('shows a count in the header and leaves a zero count out', () => {
    const { rerender } = render(<Harness meta={3} />)
    expect(screen.getByRole('button', { name: /Demo/ }).textContent).toBe('Demo3')
    rerender(<Harness meta={0} />)
    expect(screen.getByRole('button', { name: /Demo/ }).textContent).toBe('Demo')
  })

  it('keeps the header action outside the toggle', async () => {
    const onAction = vi.fn()
    render(<Harness action={<button type="button" onClick={onAction}>Act</button>} />)
    await userEvent.click(screen.getByRole('button', { name: 'Act' }))
    expect(onAction).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: /Demo/ })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Body text')).toBeInTheDocument()
  })
})
