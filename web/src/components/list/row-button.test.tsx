import { describe, it, expect, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { rowButtonProps } from './row-button'

function Row({ onActivate, onNested }: { onActivate?: () => void; onNested?: () => void }) {
  return (
    <div {...rowButtonProps(onActivate)} data-testid="row">
      <span>Name</span>
      <button type="button" onClick={e => { e.stopPropagation(); onNested?.() }}>Delete</button>
    </div>
  )
}

describe('rowButtonProps', () => {
  it('makes the element a focusable button', () => {
    render(<Row onActivate={() => {}} />)
    const row = screen.getByTestId('row')
    expect(row).toHaveAttribute('role', 'button')
    expect(row).toHaveAttribute('tabindex', '0')
  })

  it('gives a read-only element no button semantics', () => {
    render(<Row />)
    const row = screen.getByTestId('row')
    expect(row).not.toHaveAttribute('role')
    expect(row).not.toHaveAttribute('tabindex')
    // No handler: neither a click nor a key throws or does anything.
    fireEvent.click(row)
    fireEvent.keyDown(row, { key: 'Enter' })
  })

  it('activates on click', async () => {
    const onActivate = vi.fn()
    render(<Row onActivate={onActivate} />)
    await userEvent.click(screen.getByText('Name'))
    expect(onActivate).toHaveBeenCalledTimes(1)
  })

  it('activates on Enter and Space when the element is focused', async () => {
    const onActivate = vi.fn()
    render(<Row onActivate={onActivate} />)
    screen.getByTestId('row').focus()
    await userEvent.keyboard('{Enter}')
    expect(onActivate).toHaveBeenCalledTimes(1)
    await userEvent.keyboard(' ')
    expect(onActivate).toHaveBeenCalledTimes(2)
  })

  it('ignores other keys', () => {
    const onActivate = vi.fn()
    render(<Row onActivate={onActivate} />)
    const row = screen.getByTestId('row')
    for (const key of ['a', 'Tab', 'Escape', 'ArrowDown', 'Space']) {
      expect(fireEvent.keyDown(row, { key })).toBe(true)
    }
    expect(onActivate).not.toHaveBeenCalled()
  })

  it('prevents the default action of Enter and Space, so Space does not scroll', () => {
    render(<Row onActivate={() => {}} />)
    const row = screen.getByTestId('row')
    // fireEvent returns false when a handler called preventDefault.
    expect(fireEvent.keyDown(row, { key: ' ' })).toBe(false)
    expect(fireEvent.keyDown(row, { key: 'Enter' })).toBe(false)
  })

  it('leaves Enter and Space on a nested button to that button', async () => {
    const onActivate = vi.fn()
    const onNested = vi.fn()
    render(<Row onActivate={onActivate} onNested={onNested} />)
    const nested = screen.getByRole('button', { name: 'Delete' })

    // The bubbled keydown is not cancelled, so the button's native click runs.
    expect(fireEvent.keyDown(nested, { key: 'Enter' })).toBe(true)
    expect(fireEvent.keyDown(nested, { key: ' ' })).toBe(true)
    expect(onActivate).not.toHaveBeenCalled()

    nested.focus()
    await userEvent.keyboard('{Enter}')
    await userEvent.keyboard(' ')
    expect(onNested).toHaveBeenCalledTimes(2)
    expect(onActivate).not.toHaveBeenCalled()
  })
})
