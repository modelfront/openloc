import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { SegmentRowAction } from './SegmentRowAction'

describe('SegmentRowAction', () => {
  it('runs its action when enabled', () => {
    const onClick = vi.fn()
    render(<SegmentRowAction onClick={onClick}>✂ Split</SegmentRowAction>)
    fireEvent.click(screen.getByRole('button', { name: '✂ Split' }))
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('says why it is disabled, and does nothing on a click', async () => {
    const onClick = vi.fn()
    render(
      <SegmentRowAction onClick={onClick} disabled disabledReason="First click in the source text." title="Split">
        ✂ Split
      </SegmentRowAction>,
    )
    const button = screen.getByRole('button', { name: '✂ Split' })
    expect(button.getAttribute('aria-disabled')).toBe('true')
    expect(button.hasAttribute('title')).toBe(false)
    fireEvent.click(button)
    expect(onClick).not.toHaveBeenCalled()
    fireEvent.focus(button)
    expect(await screen.findByText('First click in the source text.')).toBeTruthy()
  })
})
