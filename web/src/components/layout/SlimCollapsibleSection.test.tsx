import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { SlimCollapsibleSection } from './SlimCollapsibleSection'

afterEach(cleanup)

const header = () => screen.getByRole('button', { name: /Excel/ })

describe('SlimCollapsibleSection', () => {
  it('keeps its own open state without `open`', () => {
    render(<SlimCollapsibleSection title="Excel"><p>Body</p></SlimCollapsibleSection>)
    expect(screen.queryByText('Body')).toBeNull()
    fireEvent.click(header())
    expect(screen.getByText('Body')).toBeInTheDocument()
  })

  it('with `open`, shows what the page says and reports a click instead of toggling itself', () => {
    const onOpenChange = vi.fn()
    const { rerender } = render(
      <SlimCollapsibleSection title="Excel" open={false} onOpenChange={onOpenChange}><p>Body</p></SlimCollapsibleSection>,
    )
    fireEvent.click(header())
    expect(onOpenChange).toHaveBeenCalledWith(true)
    expect(screen.queryByText('Body')).toBeNull()
    rerender(<SlimCollapsibleSection title="Excel" open onOpenChange={onOpenChange}><p>Body</p></SlimCollapsibleSection>)
    expect(screen.getByText('Body')).toBeInTheDocument()
    expect(header()).toHaveAttribute('aria-expanded', 'true')
  })
})
